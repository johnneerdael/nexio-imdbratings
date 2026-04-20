import { Hono } from "hono";
import type { AppBindings, Principal } from "../types";
import { errInternal, errRateLimited, errUnauthorizedMissing } from "../errors";

const app = new Hono<AppBindings>();

// /v1/ws — WebSocket upgrade. Routes the upgrade to the per-key Durable
// Object, which owns rate-limit state and all concurrent sockets for that
// API key. The DO enforces:
//   - handshake cost (1 token from REST bucket)
//   - per-key concurrency cap (5)
//   - per-socket message rate (20/s, burst 20)
//   - in-flight search cancellation on new search frame
app.get("/", async (c) => {
  if (c.req.header("upgrade") !== "websocket") {
    return c.json({ error: { code: "invalid_request", message: "expected websocket upgrade" } }, 400);
  }

  const principal = c.get("principal") as Principal | undefined;
  if (!principal) return errUnauthorizedMissing(c);

  const id = c.env.API_KEY_SESSION.idFromName(principal.prefix);
  const stub = c.env.API_KEY_SESSION.get(id);

  const doUrl = new URL("https://do.internal/ws/accept");
  doUrl.searchParams.set("keyId", String(principal.keyId));
  doUrl.searchParams.set("prefix", principal.prefix);
  if (principal.name) doUrl.searchParams.set("name", principal.name);

  try {
    return await stub.fetch(doUrl.toString(), {
      headers: c.req.raw.headers,
      method: "GET",
    });
  } catch (e) {
    console.error("ws upgrade to DO failed", e);
    return errInternal(c);
  }
});

export default app;

// Helper used by rest routes middleware: hit the DO to deduct tokens.
// Returns { allowed, retryAfter } where retryAfter is seconds.
export async function askRateLimit(
  env: AppBindings["Bindings"],
  principal: Principal,
  cost: number,
): Promise<{ allowed: boolean; retryAfter?: number }> {
  const id = env.API_KEY_SESSION.idFromName(principal.prefix);
  const stub = env.API_KEY_SESSION.get(id);
  const url = new URL("https://do.internal/rate/allow");
  url.searchParams.set("cost", String(cost));
  const res = await stub.fetch(url.toString(), { method: "POST" });
  if (res.status === 200) return { allowed: true };
  if (res.status === 429) {
    const retry = parseInt(res.headers.get("Retry-After") ?? "1", 10);
    return { allowed: false, retryAfter: Number.isFinite(retry) ? retry : 1 };
  }
  // On internal errors, fail open — preserves availability over strict
  // accounting. The Go API returns 500 here; we log and allow.
  console.warn("rate limiter DO returned unexpected status", res.status);
  return { allowed: true };
}
