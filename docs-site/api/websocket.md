---
title: WebSocket Streaming Search
description: Persistent WebSocket connection for typeahead title search with server-side cancellation via GET /v1/ws.
---

# WebSocket Streaming Search

`GET /v1/ws` upgrades an HTTP connection to WebSocket and delivers the same title-search results as `GET /v1/titles/search`, optimised for typeahead use. The connection is persistent, the server cancels superseded queries automatically, and only one search can be in flight per socket at a time.

## When to Use WebSocket vs REST

| | REST `/v1/titles/search` | WebSocket `/v1/ws` |
|---|---|---|
| Connection overhead | One HTTP round-trip per query | Single Upgrade, then frames |
| Server-side cancellation | No | Yes — prior query is cancelled automatically |
| Best for | One-off or low-frequency lookups | Typeahead / keystroke-driven search |
| Auth header on each request | Yes | Only on the Upgrade request |

## Connecting

### Node.js (`ws` library)

The `ws` library lets you set arbitrary HTTP headers on the Upgrade request, so you can pass `X-API-Key` directly:

```js
import WebSocket from 'ws'

const ws = new WebSocket('wss://api.nexioapp.org/v1/ws', {
  headers: {
    'X-API-Key': 'abcdef12.00112233445566778899aabbccddeeff0011223344556677'
  }
})

ws.on('open', () => {
  ws.send(JSON.stringify({ type: 'search', seq: 1, q: 'matrix', types: ['movie', 'tvSeries'] }))
})

ws.on('message', (data) => {
  const frame = JSON.parse(data)
  console.log(frame)
})
```

### `wscat` (CLI)

```bash
wscat \
  --connect wss://api.nexioapp.org/v1/ws \
  --header "X-API-Key: abcdef12.00112233445566778899aabbccddeeff0011223344556677"
```

Once connected, send frames as JSON strings:

```
> {"type":"search","seq":1,"q":"matrix","types":["movie","tvSeries"]}
< {"type":"result","seq":1,"results":[...],"meta":{"snapshotId":42,"count":1}}
```

### Browser — known limitation

Browsers cannot set arbitrary HTTP headers on `new WebSocket(url)`. The `Sec-WebSocket-Protocol` subprotocol field is the only header browsers can influence, and using it to carry an API key is non-standard and not supported by this server.

**Recommended browser patterns:**

1. **Session cookie** — sign in through the portal. The portal's auth flow sets a short-lived session cookie that the browser forwards on the Upgrade request automatically.
2. **Authorization proxy** — deploy a thin proxy (e.g. Caddy, nginx, a small edge function) that reads the key from a cookie or a first-frame token and injects `X-API-Key` before forwarding to the API.

Node.js, Deno, and CLI clients can set `X-API-Key` directly as shown above and are not affected by this limitation.

## Authentication

The Upgrade request is authenticated with the same mechanism as every `/v1/*` REST route:

```
GET /v1/ws HTTP/1.1
Host: api.nexioapp.org
Upgrade: websocket
Connection: Upgrade
Sec-WebSocket-Key: <base64-nonce>
Sec-WebSocket-Version: 13
X-API-Key: abcdef12.00112233445566778899aabbccddeeff0011223344556677
```

Or with a bearer token:

```
Authorization: Bearer abcdef12.00112233445566778899aabbccddeeff0011223344556677
```

See [API Authentication](authentication.md) for the key format and validation flow.

## Rate Limits

- The Upgrade request counts as **one call** against the same token-bucket quota used by REST requests. A `429` on the Upgrade means the bucket was empty.
- A maximum of **5 concurrent open WebSocket connections** per API key. A 6th concurrent connection is rejected with `429` at Upgrade time.
- After a successful Upgrade, individual client messages are **not** metered against the REST quota. Each socket has a local safety ceiling of **20 messages per second**. Excess messages receive an `{"type":"error","code":"rate_limited"}` frame; the socket remains open.

## Message Protocol

All frames are UTF-8 text frames carrying JSON objects. Binary frames are not used.

### Client → Server

#### `search`

Submit or replace the current query. Sending a new `search` frame while a previous query is in flight cancels the previous query server-side.

```json
{"type":"search","seq":1,"q":"matrix","types":["movie","tvSeries"]}
```

The WebSocket endpoint always returns at most 10 results. Clients needing larger result sets should use `GET /v1/titles/search`.

| Field   | Type          | Required | Default                  | Notes                                                        |
|---------|---------------|----------|--------------------------|--------------------------------------------------------------|
| `type`  | string        | yes      | —                        | Must be `"search"`.                                          |
| `seq`   | uint64        | yes      | —                        | Client-supplied monotonic counter. Echoed verbatim in reply. |
| `q`     | string        | yes      | —                        | Search query. Trimmed. Minimum 2 characters.                 |
| `types` | array[string] | no       | `["movie","tvSeries"]`   | Allowed values: `"movie"`, `"tvSeries"`.                     |

#### `ping`

Keepalive probe. The server replies with a `pong` frame echoing the same `seq`.

```json
{"type":"ping","seq":2}
```

### Server → Client

#### `result`

Search completed. `results` and `meta` are identical in shape to the REST `/v1/titles/search` response.

```json
{
  "type": "result",
  "seq": 1,
  "results": [
    {
      "tconst": "tt0133093",
      "titleType": "movie",
      "primaryTitle": "The Matrix",
      "startYear": 1999
    }
  ],
  "meta": {
    "snapshotId": 42,
    "count": 1
  }
}
```

#### `cancelled`

Emitted when a new `search` frame arrived before the previous query finished. `seq` echoes the **cancelled** query's sequence number.

```json
{"type":"cancelled","seq":1}
```

#### `error`

The request was rejected or a server fault occurred. `seq` echoes the triggering message's sequence number.

```json
{"type":"error","seq":1,"code":"invalid_request","message":"q must be at least 2 characters"}
```

| `code`            | Meaning                                                                         |
|-------------------|---------------------------------------------------------------------------------|
| `invalid_request` | Bad message shape, `q` too short, or unknown `types` value.                     |
| `rate_limited`    | Local per-socket 20 msg/s ceiling exceeded. Socket stays open.                  |
| `internal_error`  | Unexpected server fault.                                                        |

#### `pong`

Reply to a `ping`. `seq` echoes the ping's sequence number.

```json
{"type":"pong","seq":2}
```

## In-Flight Query Semantics

Only one `search` can be in flight per socket at a time. When the client sends a second `search` before the first has resolved:

1. The server cancels the first query.
2. The server emits `{"type":"cancelled","seq":<prior-seq>}`.
3. The server begins processing the new query and eventually emits `{"type":"result","seq":<new-seq>,...}`.

## WebSocket Close Codes

| Code   | Meaning                                                                                                       |
|--------|---------------------------------------------------------------------------------------------------------------|
| `1000` | Normal closure. Client or server closed the connection cleanly.                                               |
| `1001` | Going away. Server is shutting down or the ping keepalive timed out.                                          |
| `1008` | Policy violation. Client was sending faster than the 20 msg/s safety ceiling and did not back off after repeated `rate_limited` errors. |

## Client-Side Patterns

### Debounce

Avoid sending a `search` frame on every keystroke. A 75 ms debounce reduces unnecessary frames without perceptible latency for humans:

```js
let debounceTimer = null

function onInput(value) {
  clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    if (value.trim().length >= 2) {
      sendSearch(value)
    }
  }, 75)
}
```

### Monotonic `seq` and stale-result dropping

Use a monotonically increasing integer for `seq` and track the latest `seq` you sent. Discard any `result` frame whose `seq` is less than your current watermark — it belongs to a superseded query:

```js
let latestSeq = 0

function sendSearch(q) {
  latestSeq += 1
  const seq = latestSeq
  ws.send(JSON.stringify({ type: 'search', seq, q }))
}

ws.on('message', (data) => {
  const frame = JSON.parse(data)
  if (frame.type === 'result' && frame.seq < latestSeq) {
    return // stale — discard
  }
  // handle frame
})
```

The server also cancels superseded queries server-side, but the client watermark handles race conditions where a `result` arrives in transit after the next `search` was already sent.

### Full example (Node.js)

```js
import WebSocket from 'ws'

const ws = new WebSocket('wss://api.nexioapp.org/v1/ws', {
  headers: { 'X-API-Key': 'abcdef12.00112233445566778899aabbccddeeff0011223344556677' }
})

let latestSeq = 0
let debounceTimer = null

ws.on('open', () => {
  // simulate typing "matrix" with 75 ms debounce
  typeahead('matrix')
})

function typeahead(q) {
  clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    if (q.trim().length < 2) return
    latestSeq += 1
    ws.send(JSON.stringify({ type: 'search', seq: latestSeq, q, types: ['movie', 'tvSeries'] }))
  }, 75)
}

ws.on('message', (data) => {
  const frame = JSON.parse(data)

  if (frame.type === 'result') {
    if (frame.seq < latestSeq) return // stale
    console.log('results:', frame.results)
  } else if (frame.type === 'cancelled') {
    console.log('query cancelled, seq:', frame.seq)
  } else if (frame.type === 'error') {
    console.error('error:', frame.code, frame.message)
  }
})

ws.on('close', (code, reason) => {
  console.log('closed', code, reason.toString())
})
```
