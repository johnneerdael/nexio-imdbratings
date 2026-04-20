import postgres from "postgres";
import type { Env } from "./types";

// Create a new postgres client per request. Hyperdrive pools the underlying
// TCP connections, so client creation is cheap. The client itself keeps a
// small local pool (max: 5) for parallel queries within a single request.
export function newClient(env: Env) {
  return postgres(env.HYPERDRIVE.connectionString, {
    max: 5,
    fetch_types: false,
    idle_timeout: 20,
  });
}

export async function withDb<T>(
  env: Env,
  ctx: ExecutionContext | undefined,
  fn: (sql: ReturnType<typeof newClient>) => Promise<T>,
): Promise<T> {
  const sql = newClient(env);
  try {
    return await fn(sql);
  } finally {
    if (ctx) ctx.waitUntil(sql.end({ timeout: 5 }));
    else await sql.end({ timeout: 5 });
  }
}
