---
title: API Overview
description: Endpoints, authentication, response conventions, and ratings-wrapper behavior for the IMDb API.
---

# API Overview

The Go API serves the public contract for the platform. It is mounted under `/v1/*` and shares a host with the portal and health checks.

## Endpoint Families

The API is organized by resource:

- `GET /healthz` and `GET /readyz` for health checks
- `GET /v1/meta/snapshots` for dataset snapshot metadata
- `GET /v1/meta/stats` for aggregate counts
- `GET /v1/ratings/{tconst}` for ratings lookup
- `POST /v1/ratings/bulk` for synchronous small-batch ratings lookups
- `GET /v1/titles/search` for prefix-based title search across movies and TV series
- `GET /v1/ws` for WebSocket streaming search — a persistent-connection alternative to `/v1/titles/search` with server-side cancellation, optimised for typeahead

Every `/v1/*` route requires an API key. The health routes do not.

## Authentication Model

The API accepts the key in either of these headers:

- `X-API-Key: prefix.secret`
- `Authorization: Bearer prefix.secret`

The server validates the prefix first, loads the stored record, rejects revoked or expired keys, and then checks the full secret with the configured pepper. See [API authentication](authentication.md) for the exact flow.

## Response Shapes

The API uses a small number of response conventions:

- single-resource endpoints return the resource directly
- snapshot listings return `{ "items": [...] }`
- bulk ratings endpoints return `{ "results": [...], "missing": [...] }`
- `episodes=true` switches the ratings endpoints to the wrapper shape with `requestTconst`, optional `rating`, optional `episodesParentTconst`, and `episodes`

Some responses are intentionally strict. For example, ratings bulk requests reject empty identifier lists, blank identifiers, and more than 250 identifiers.

## Ratings Wrapper

Set `episodes=true` on `GET /v1/ratings/{tconst}` or `POST /v1/ratings/bulk` when you want the wrapper form. For single lookups the wrapper preserves the original `tconst`, returns the direct `rating` when available, and adds `episodes` for the relevant series or episode parent. For bulk lookups each item in `results` uses that same wrapper shape, while `missing` still records identifiers that were not found.

## Health Checks

- `GET /healthz` returns `200` with `{"status":"ok"}`
- `GET /readyz` returns `200` with `{"status":"ready"}` only after the service can answer through its repository layer

These endpoints are designed for proxy and orchestration probes, not for authenticated client use.

## Practical Usage

For most clients the sequence is:

1. Mint an API key in the portal.
2. Store the key in your client secret manager.
3. Send requests to `/v1/*` with `X-API-Key` or a bearer token.
4. Use `episodes=true` only when you need the wrapper response.

If you are deploying the platform yourself, read [Self-hosting overview](../self-hosting/overview.md) after this page so the endpoint behavior lines up with your proxy routing.
