import type { Context } from "hono";
import type { ContentfulStatusCode } from "hono/utils/http-status";

export function apiError(
  c: Context,
  status: ContentfulStatusCode,
  code: string,
  message: string,
  extraHeaders?: Record<string, string>,
) {
  if (extraHeaders) {
    for (const [k, v] of Object.entries(extraHeaders)) c.header(k, v);
  }
  return c.json({ error: { code, message } }, status);
}

export const errBadRequest = (c: Context, message = "request parameters are invalid") =>
  apiError(c, 400, "invalid_request", message);

export const errUnauthorizedMissing = (c: Context) =>
  apiError(c, 401, "missing_api_key", "api key required");

export const errUnauthorizedInvalid = (c: Context) =>
  apiError(c, 401, "invalid_api_key", "api key invalid");

export const errNotFound = (c: Context) =>
  apiError(c, 404, "not_found", "resource not found");

export const errRateLimited = (c: Context, retryAfterSeconds?: number) =>
  apiError(
    c,
    429,
    "rate_limited",
    "rate limit exceeded",
    retryAfterSeconds ? { "Retry-After": String(retryAfterSeconds) } : undefined,
  );

export const errInternal = (c: Context) =>
  apiError(c, 500, "internal_error", "internal server error");
