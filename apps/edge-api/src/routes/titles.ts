import { Hono } from "hono";
import type { AppBindings } from "../types";
import { newClient, withDb } from "../db";
import { errBadRequest, errInternal } from "../errors";

const ALLOWED_TITLE_TYPES = new Set(["movie", "tvSeries"]);

type Sql = ReturnType<typeof newClient>;

export async function searchTitlesQuery(
  sql: Sql,
  q: string,
  types: string[],
  limit: number,
) {
  const snapshotRows = await sql<Array<{ id: number | string }>>`
    SELECT id
    FROM imdb_snapshots
    WHERE is_active = TRUE
    ORDER BY imported_at DESC, id DESC
    LIMIT 1
  `;
  const snapshotId = snapshotRows.length === 0 ? 0 : Number(snapshotRows[0].id);

  const pattern = `%${q}%`;
  const typeCond = types
    .map((t) => sql`title_type = ${t}`)
    .reduce((acc, cur) => sql`${acc} OR ${cur}`);
  const rows = await sql<Array<{
    tconst: string;
    title_type: string;
    primary_title: string;
    start_year: number | null;
  }>>`
    SELECT tconst, title_type, primary_title, start_year
    FROM title_basics
    WHERE num_votes > 0
      AND (${typeCond})
      AND primary_title ILIKE ${pattern}
    ORDER BY
      num_votes DESC,
      start_year DESC NULLS LAST,
      primary_title ASC
    LIMIT ${limit}
  `;

  const results = rows.map((r) => {
    const item: Record<string, unknown> = {
      tconst: r.tconst,
      titleType: r.title_type,
      primaryTitle: r.primary_title,
    };
    if (r.start_year !== null) item.startYear = r.start_year;
    return item;
  });

  return {
    results,
    meta: { snapshotId, count: results.length },
  };
}

const app = new Hono<AppBindings>();

app.get("/search", async (c) => {
  const q = (c.req.query("q") ?? "").trim();
  if (q.length < 2) return errBadRequest(c, "q must be at least 2 characters");

  let types = ["movie", "tvSeries"];
  const rawTypes = (c.req.query("types") ?? "").trim();
  if (rawTypes !== "") {
    const parts = rawTypes.split(",").map((p) => p.trim());
    types = [];
    for (const p of parts) {
      if (!ALLOWED_TITLE_TYPES.has(p)) {
        return errBadRequest(c, "types must be movie and/or tvSeries");
      }
      types.push(p);
    }
    if (types.length === 0) return errBadRequest(c, "types must be movie and/or tvSeries");
  }

  let limit = 10;
  const rawLimit = (c.req.query("limit") ?? "").trim();
  if (rawLimit !== "") {
    const parsed = parseInt(rawLimit, 10);
    if (!Number.isFinite(parsed) || parsed < 1 || parsed > 50) {
      return errBadRequest(c, "limit must be an integer between 1 and 50");
    }
    limit = parsed;
  }

  try {
    const response = await withDb(c.env, c.executionCtx, (sql) =>
      searchTitlesQuery(sql, q, types, limit),
    );
    c.header("Cache-Control", "public, max-age=60, stale-while-revalidate=300");
    return c.json(response);
  } catch (e) {
    console.error("search titles failed", e);
    return errInternal(c);
  }
});

export default app;
