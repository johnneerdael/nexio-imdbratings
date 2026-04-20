import { Hono } from "hono";
import type { AppBindings } from "../types";
import { newClient, withDb } from "../db";
import { errBadRequest, errInternal, errNotFound } from "../errors";

const app = new Hono<AppBindings>();

interface RatingRow {
  tconst: string;
  average_rating: string | number;
  num_votes: number;
}

interface EpisodeRatingRow {
  tconst: string;
  parent_tconst: string;
  season_number: number | null;
  episode_number: number | null;
  average_rating: string | number;
  num_votes: number;
}

type Sql = ReturnType<typeof newClient>;

async function fetchRating(sql: Sql, tconst: string) {
  const rows = await sql<RatingRow[]>`
    SELECT tconst, average_rating, num_votes
    FROM title_ratings
    WHERE tconst = ${tconst}
  `;
  if (rows.length === 0) return null;
  const r = rows[0];
  return {
    tconst: r.tconst,
    averageRating: Number(r.average_rating),
    numVotes: r.num_votes,
  };
}

async function fetchEpisodeParent(sql: Sql, tconst: string): Promise<string | null> {
  const rows = await sql<Array<{ parent_tconst: string }>>`
    SELECT parent_tconst
    FROM title_episodes
    WHERE tconst = ${tconst}
  `;
  return rows.length === 0 ? null : rows[0].parent_tconst;
}

async function hasEpisodesParent(sql: Sql, tconst: string): Promise<boolean> {
  const rows = await sql<Array<{ exists: boolean }>>`
    SELECT EXISTS(
      SELECT 1 FROM title_episodes WHERE parent_tconst = ${tconst}
    ) AS exists
  `;
  return rows[0].exists;
}

async function listEpisodeRatings(sql: Sql, parentTconst: string) {
  const rows = await sql<EpisodeRatingRow[]>`
    SELECT
      e.tconst, e.parent_tconst, e.season_number, e.episode_number,
      r.average_rating, r.num_votes
    FROM title_episodes e
    JOIN title_ratings r ON r.tconst = e.tconst
    WHERE e.parent_tconst = ${parentTconst}
    ORDER BY e.season_number NULLS FIRST, e.episode_number NULLS FIRST, e.tconst
  `;
  return rows.map((r) => {
    const item: Record<string, unknown> = {
      tconst: r.tconst,
      parentTconst: r.parent_tconst,
      averageRating: Number(r.average_rating),
      numVotes: r.num_votes,
    };
    if (r.season_number !== null) item.seasonNumber = r.season_number;
    if (r.episode_number !== null) item.episodeNumber = r.episode_number;
    return item;
  });
}

async function resolveRatingWithEpisodes(sql: Sql, tconst: string) {
  const rating = await fetchRating(sql, tconst);
  const parent = await fetchEpisodeParent(sql, tconst);

  const result: Record<string, unknown> = {
    requestTconst: tconst,
    episodes: [] as unknown[],
  };
  if (rating) result.rating = rating;

  if (parent !== null) {
    result.episodesParentTconst = parent;
    result.episodes = await listEpisodeRatings(sql, parent);
    return { found: true, result };
  }

  if (await hasEpisodesParent(sql, tconst)) {
    result.episodesParentTconst = tconst;
    result.episodes = await listEpisodeRatings(sql, tconst);
    return { found: true, result };
  }

  if (rating) return { found: true, result };
  return { found: false, result: null };
}

function wantsEpisodes(c: { req: { query: (k: string) => string | undefined } }): boolean {
  const v = c.req.query("episodes");
  return typeof v === "string" && v.trim().toLowerCase() === "true";
}

app.get("/:tconst", async (c) => {
  const tconst = c.req.param("tconst");
  try {
    return await withDb(c.env, c.executionCtx, async (sql) => {
      if (wantsEpisodes(c)) {
        const { found, result } = await resolveRatingWithEpisodes(sql, tconst);
        if (!found || !result) return errNotFound(c);
        return c.json(result);
      }
      const rating = await fetchRating(sql, tconst);
      if (!rating) return errNotFound(c);
      return c.json(rating);
    });
  } catch (e) {
    console.error("get rating failed", e);
    return errInternal(c);
  }
});

app.post("/bulk", async (c) => {
  let body: unknown;
  try {
    body = await c.req.json();
  } catch {
    return errBadRequest(c);
  }
  if (!body || typeof body !== "object") return errBadRequest(c);
  const identifiersRaw = (body as { identifiers?: unknown }).identifiers;
  if (!Array.isArray(identifiersRaw) || identifiersRaw.length === 0 || identifiersRaw.length > 250) {
    return errBadRequest(c);
  }
  const identifiers: string[] = [];
  for (const id of identifiersRaw) {
    if (typeof id !== "string") return errBadRequest(c);
    const trimmed = id.trim();
    if (trimmed === "") return errBadRequest(c);
    identifiers.push(trimmed);
  }

  const episodes = wantsEpisodes(c);

  try {
    return await withDb(c.env, c.executionCtx, async (sql) => {
      const missing: string[] = [];
      if (episodes) {
        const results: unknown[] = [];
        for (const id of identifiers) {
          const { found, result } = await resolveRatingWithEpisodes(sql, id);
          if (!found || !result) {
            missing.push(id);
            continue;
          }
          results.push(result);
        }
        return c.json({ results, missing });
      }
      const results: unknown[] = [];
      for (const id of identifiers) {
        const rating = await fetchRating(sql, id);
        if (!rating) {
          missing.push(id);
          continue;
        }
        results.push(rating);
      }
      return c.json({ results, missing });
    });
  } catch (e) {
    console.error("bulk ratings failed", e);
    return errInternal(c);
  }
});

export default app;
