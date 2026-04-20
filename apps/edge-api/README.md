# nexio-edge-api

Cloudflare Worker drop-in for the Go API at `apps/api`. Serves the same routes,
JSON shapes, WebSocket protocol, auth, and weighted rate-limit semantics.

## Layout

- `src/index.ts` — Hono app, auth + rate-limit middleware, route mounting
- `src/auth.ts` — API key middleware, SHA-256 + pepper hash, timing-safe compare
- `src/db.ts` — Postgres client factory (new client per request; Hyperdrive pools TCP)
- `src/routes/meta.ts` — `/v1/meta/snapshots`, `/v1/meta/stats`
- `src/routes/ratings.ts` — `/v1/ratings/{tconst}`, `/v1/ratings/bulk` (incl. `?episodes=true`)
- `src/routes/titles.ts` — `/v1/titles/search`
- `src/routes/ws.ts` — `/v1/ws` upgrade, forwards to Durable Object
- `src/durable/session.ts` — `ApiKeySession` DO: weighted REST bucket, hibernatable WS, per-socket rate bucket, per-key 5-socket cap, in-flight search cancellation

## Deploy

```bash
cd apps/edge-api
npm install

# Secret (shared with the Go API — must match apps/api API_KEY_PEPPER)
npx wrangler secret put API_KEY_PEPPER

# First deploy creates the Durable Object class via the v1 migration in wrangler.toml
npx wrangler deploy
```

Smoke test:

```bash
curl -H "X-API-Key: <key>" https://nexio-edge-api.<subdomain>.workers.dev/v1/meta/stats
```

WebSocket test (with `websocat`):

```bash
websocat \
  -H "X-API-Key: <key>" \
  "wss://nexio-edge-api.<subdomain>.workers.dev/v1/ws"
# then send: {"type":"search","seq":1,"q":"matrix"}
```

## Notes vs. Go API

- **Rate limiting** lives in a single Durable Object per API key (named by `key_prefix`). The REST token bucket persists across isolates via DO storage; the per-socket bucket lives in the WebSocket's serialized attachment.
- **WebSockets** use Cloudflare's hibernatable API (`state.acceptWebSocket`). Idle sockets don't consume CPU between messages. Ping/pong stays app-level (`{type:"ping"}` / `{type:"pong"}`) so the client protocol is unchanged.
- **In-flight search cancellation** uses an in-memory `AbortController` map. If the DO hibernates between messages (rare when a socket is active) the prior search is already complete or garbage-collected — behavior is equivalent to the Go cancellation.
- **Auth** reads `api_keys` directly from Postgres per request. Consider adding a short KV cache later if latency matters.
- **Custom domain**: add a route in `wrangler.toml` like `routes = [{ pattern = "api.thepi.es", custom_domain = true }]` once DNS is ready.
