import type { Context, MiddlewareHandler } from "hono";
import type { AppBindings, Env, Principal } from "./types";
import { newClient } from "./db";
import { errUnauthorizedInvalid, errUnauthorizedMissing } from "./errors";

export function splitApiKey(raw: string): { prefix: string; secret: string } | null {
  const trimmed = raw.trim();
  const dot = trimmed.indexOf(".");
  if (dot <= 0 || dot >= trimmed.length - 1) return null;
  return { prefix: trimmed.slice(0, dot), secret: trimmed.slice(dot + 1) };
}

export async function hashApiKey(rawKey: string, pepper: string): Promise<string> {
  const bytes = new TextEncoder().encode(`${pepper}:${rawKey}`);
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

function timingSafeEqualHex(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let result = 0;
  for (let i = 0; i < a.length; i++) result |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return result === 0;
}

interface ApiKeyRow {
  id: number;
  user_id: number | null;
  name: string;
  key_prefix: string;
  key_hash: string;
  revoked_at: Date | null;
  expires_at: Date | null;
}

export async function authenticate(
  env: Env,
  ctx: ExecutionContext,
  rawKey: string,
): Promise<Principal | null> {
  const parsed = splitApiKey(rawKey);
  if (!parsed) return null;

  const sql = newClient(env);
  try {
    const rows = await sql<ApiKeyRow[]>`
      SELECT id, user_id, name, key_prefix, key_hash, revoked_at, expires_at
      FROM api_keys
      WHERE key_prefix = ${parsed.prefix}
      LIMIT 1
    `;
    if (rows.length === 0) return null;
    const rec = rows[0];
    if (rec.revoked_at !== null) return null;
    if (rec.expires_at && rec.expires_at.getTime() < Date.now()) return null;

    const actualHash = await hashApiKey(rawKey, env.API_KEY_PEPPER);
    if (!timingSafeEqualHex(actualHash, rec.key_hash)) return null;

    // Fire-and-forget last_used_at update.
    ctx.waitUntil(
      (async () => {
        const touchSql = newClient(env);
        try {
          await touchSql`
            UPDATE api_keys SET last_used_at = NOW() WHERE id = ${rec.id}
          `;
        } finally {
          await touchSql.end({ timeout: 5 });
        }
      })(),
    );

    return {
      keyId: Number(rec.id),
      userId: rec.user_id !== null ? Number(rec.user_id) : null,
      name: rec.name,
      prefix: rec.key_prefix,
    };
  } finally {
    ctx.waitUntil(sql.end({ timeout: 5 }));
  }
}

export function extractApiKey(c: Context): string | null {
  const headerKey = c.req.header("x-api-key")?.trim();
  if (headerKey) return headerKey;
  const authHeader = c.req.header("authorization")?.trim();
  if (authHeader && authHeader.toLowerCase().startsWith("bearer ")) {
    return authHeader.slice(7).trim();
  }
  return null;
}

export function requireApiKey(): MiddlewareHandler<AppBindings> {
  return async (c, next) => {
    const raw = extractApiKey(c);
    if (!raw) return errUnauthorizedMissing(c);

    const principal = await authenticate(c.env, c.executionCtx, raw);
    if (!principal) return errUnauthorizedInvalid(c);

    c.set("principal", principal);
    await next();
  };
}
