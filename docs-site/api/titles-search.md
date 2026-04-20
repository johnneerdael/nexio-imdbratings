---
title: Title Search
description: Search IMDb titles by prefix across movies and TV series using the /v1/titles/search endpoint.
---

# Title Search

`GET /v1/titles/search` returns a ranked list of IMDb titles matching a prefix query. Results include movies and TV series. The endpoint is optimised for typeahead use — call it per-keystroke and cache results client-side.

## Request

```
GET /v1/titles/search?q=matrix&types=movie,tvSeries&limit=5
X-API-Key: abcdef12.00112233445566778899aabbccddeeff0011223344556677
```

### Query Parameters

| Parameter | Type   | Required | Default           | Notes                                                    |
|-----------|--------|----------|-------------------|----------------------------------------------------------|
| `q`       | string | yes      | —                 | Search query. Minimum 2 characters.                      |
| `types`   | string | no       | `movie,tvSeries`  | Comma-separated content types. Allowed: `movie`, `tvSeries`. |
| `limit`   | number | no       | `10`              | Maximum results. Range 1–50.                             |

### Authentication

Every `/v1/*` route requires an API key. Pass it in either header:

- `X-API-Key: prefix.secret`
- `Authorization: Bearer prefix.secret`

See [API Authentication](authentication.md) for the key format and validation flow.

## Response

### 200 OK

```json
{
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

#### Result fields

| Field          | Type    | Notes                                      |
|----------------|---------|--------------------------------------------|
| `tconst`       | string  | IMDb title identifier, e.g. `tt0133093`.   |
| `titleType`    | string  | `movie` or `tvSeries`.                     |
| `primaryTitle` | string  | Primary display title from IMDb.           |
| `startYear`    | integer | Release or premiere year. May be `null`.   |

#### Meta fields

| Field        | Type    | Notes                                          |
|--------------|---------|------------------------------------------------|
| `snapshotId` | integer | ID of the active dataset snapshot used.        |
| `count`      | integer | Number of results returned in this response.   |

### 400 Bad Request

Returned when:

- `q` is shorter than 2 characters
- `types` contains a value other than `movie` or `tvSeries`
- `limit` is outside the range 1–50

```json
{
  "error": {
    "code": "invalid_request",
    "message": "request parameters are invalid"
  }
}
```

### 401 Unauthorized

Returned when the API key header is missing or the key fails validation.

### 429 Too Many Requests

Returned when the per-key rate limit is exceeded.

## Ranking

Results are ordered as follows:

1. Exact title match (case-insensitive)
2. Prefix match
3. Most recent `startYear` descending
4. `primaryTitle` ascending as a tie-breaker

This means an exact match always appears first, and among prefix matches newer titles rank higher.

## Practical Usage

Filter by type to reduce noise:

```bash
curl -H "X-API-Key: abcdef12.00..." \
  "https://api.nexioapp.org/v1/titles/search?q=matrix&types=movie&limit=10"
```

Use both types for a combined typeahead dropdown:

```bash
curl -H "X-API-Key: abcdef12.00..." \
  "https://api.nexioapp.org/v1/titles/search?q=breaking&limit=10"
```

The response carries `Cache-Control: public, max-age=60, stale-while-revalidate=300` so repeated identical queries are served from cache at the proxy layer.
