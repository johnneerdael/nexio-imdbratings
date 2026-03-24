---
title: Operations Runbook
description: Day-two operational checks, troubleshooting, and failure handling.
---

# Operations Runbook

This runbook focuses on the things you actually do after the platform is deployed: check health, inspect logs, debug auth, and verify ratings lookups and snapshot imports.

## Routine Checks

Run these checks after startup or deployment:

- `GET /healthz`
- `GET /readyz`
- portal login with an allowed Google account
- API key creation from the portal
- a sample authenticated `/v1/meta/stats` request
- a sample authenticated `/v1/ratings/tt1375666` request
- the same ratings request with `?episodes=true`

If any of those steps fail, you usually have a routing, auth, or database issue rather than a data issue.

## Log Locations

For systemd deployments, the services log to their configured log files.

For Compose deployments, inspect the service logs directly:

```bash
docker compose --env-file .env.compose -f docker-compose.deploy.yml logs -f api
docker compose --env-file .env.compose -f docker-compose.deploy.yml logs -f worker
docker compose --env-file .env.compose -f docker-compose.deploy.yml logs -f web
```

Add the proxy service if you are debugging routing at the edge.

## Database Checks

Use the database when you need to confirm the state of the world:

- `users` for Google-linked portal users
- `web_sessions` for browser sessions
- `api_keys` for API key lifecycle
- `imdb_snapshots` for the latest imported dataset

## Worker Troubleshooting

If snapshot imports stop or lag behind:

1. check that the worker is running
2. check that the database is reachable
3. check the worker logs for import failures
4. verify the upstream IMDb dataset endpoints are reachable

If the worker reports a failed import, inspect the log output for the dataset and snapshot ID involved before retrying the deployment or the import window.

## Auth Troubleshooting

If Google login fails:

- confirm the redirect URI in Google Console
- confirm `GOOGLE_REDIRECT_URL`
- confirm the allowed email is in `ALLOWED_GOOGLE_EMAILS`
- confirm the Google account is verified
- confirm the session cookie secret is stable

If the portal login works but API calls fail:

- check that the API key was copied exactly once
- confirm the key was sent in `X-API-Key` or `Authorization: Bearer`
- confirm the key was not revoked
- confirm `API_KEY_PEPPER` has not changed

## Proxy Troubleshooting

If the portal works but `/v1/*` does not:

- the proxy routing split is wrong
- the API is not reachable on its internal port
- the API container is unhealthy

If `/healthz` and `/readyz` reach the portal instead of the API, the edge proxy is misrouted. See [Proxy choices](../self-hosting/proxies.md).

## Emergency Checklist

When in doubt, verify in this order:

1. edge proxy routing
2. API readiness
3. database connectivity
4. portal session/auth state
5. API key validity
6. worker health

That sequence matches the dependency chain of the stack and usually narrows the problem quickly.
