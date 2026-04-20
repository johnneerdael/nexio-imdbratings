import { Hono } from "hono";
import type { AppBindings, Env, Principal } from "./types";
import { withDb } from "./db";
import { requireApiKey } from "./auth";
import { errInternal, errRateLimited } from "./errors";
import meta from "./routes/meta";
import ratings from "./routes/ratings";
import titles from "./routes/titles";
import ws from "./routes/ws";
import { askRateLimit } from "./routes/ws";

export { ApiKeySession } from "./durable/session";

const app = new Hono<AppBindings>();

app.get("/healthz", (c) => c.json({ status: "ok" }));

app.get("/readyz", async (c) => {
  try {
    await withDb(c.env, c.executionCtx, async (sql) => {
      await sql`SELECT 1`;
    });
    return c.json({ status: "ready" });
  } catch (e) {
    console.error("readyz failed", e);
    return c.json({ error: { code: "not_ready", message: "service is not ready" } }, 503);
  }
});

// v1 — auth + weighted rate limit.
const v1 = new Hono<AppBindings>();
v1.use("*", requireApiKey());

// Rate limit middleware (per-request cost via DO).
v1.use("*", async (c, next) => {
  const principal = c.get("principal") as Principal;
  let cost = 1;

  const episodes = (c.req.query("episodes") ?? "").trim().toLowerCase() === "true";
  if (episodes) {
    cost = parseInt(c.env.RATE_LIMIT_EPISODES_COST, 10) || 8;
  } else if (c.req.method === "POST" && c.req.path.endsWith("/v1/ratings/bulk")) {
    try {
      const cloned = c.req.raw.clone();
      const body = (await cloned.json()) as { identifiers?: unknown };
      if (Array.isArray(body.identifiers)) {
        const divisor = parseInt(c.env.RATE_LIMIT_BULK_DIVISOR, 10) || 25;
        cost = Math.max(1, Math.ceil(body.identifiers.length / divisor));
      }
    } catch {
      // Let the route handler return the real 400; charge 1 token.
    }
  }

  // WebSocket upgrades deduct inside the DO (via handshake) — skip here.
  if (c.req.path.endsWith("/v1/ws")) {
    await next();
    return;
  }

  const decision = await askRateLimit(c.env, principal, cost);
  if (!decision.allowed) return errRateLimited(c, decision.retryAfter);
  await next();
});

v1.route("/meta", meta);
v1.route("/ratings", ratings);
v1.route("/titles", titles);
v1.route("/ws", ws);

app.route("/v1", v1);

app.onError((err, c) => {
  console.error("unhandled error", err);
  return errInternal(c);
});

export default {
  fetch: app.fetch,
} satisfies ExportedHandler<Env>;
