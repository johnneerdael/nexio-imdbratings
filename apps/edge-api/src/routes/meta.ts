import { Hono } from "hono";
import type { AppBindings } from "../types";
import { withDb } from "../db";
import { errInternal } from "../errors";

const app = new Hono<AppBindings>();

app.get("/snapshots", async (c) => {
  try {
    const items = await withDb(c.env, c.executionCtx, async (sql) => {
      const rows = await sql<Array<Record<string, any>>>`
        SELECT
          id, dataset_name, status, sync_mode,
          COALESCE(dataset_version, '') AS dataset_version,
          imported_at, source_updated_at,
          COALESCE(source_etag, '') AS source_etag,
          is_active, rating_count, episode_count,
          COALESCE(notes, '') AS notes,
          COALESCE(source_url, '') AS source_url,
          completed_at
        FROM imdb_snapshots
        ORDER BY imported_at DESC, id DESC
      `;
      return rows.map((r) => {
        const item: Record<string, unknown> = {
          id: Number(r.id),
          dataset: r.dataset_name,
          status: r.status,
          importedAt: r.imported_at,
          isActive: r.is_active,
          ratingCount: Number(r.rating_count),
          episodeCount: Number(r.episode_count),
        };
        if (r.sync_mode) item.syncMode = r.sync_mode;
        if (r.dataset_version) item.datasetVersion = r.dataset_version;
        if (r.source_updated_at) item.sourceUpdatedAt = r.source_updated_at;
        if (r.source_etag) item.sourceETag = r.source_etag;
        if (r.notes) item.notes = r.notes;
        if (r.source_url) item.sourceUrl = r.source_url;
        if (r.completed_at) item.completedAt = r.completed_at;
        return item;
      });
    });
    return c.json({ items });
  } catch (e) {
    console.error("list snapshots failed", e);
    return errInternal(c);
  }
});

app.get("/stats", async (c) => {
  try {
    const stats = await withDb(c.env, c.executionCtx, async (sql) => {
      const rows = await sql<Array<{ ratings: string | number; episodes: string | number; snapshots: string | number }>>`
        WITH active_snapshot AS (
          SELECT rating_count, episode_count
          FROM imdb_snapshots
          WHERE is_active = TRUE
          ORDER BY imported_at DESC, id DESC
          LIMIT 1
        ),
        table_estimates AS (
          SELECT
            COALESCE(MAX(CASE WHEN relname = 'title_ratings' THEN n_live_tup::bigint END), 0) AS ratings,
            COALESCE(MAX(CASE WHEN relname = 'title_episodes' THEN n_live_tup::bigint END), 0) AS episodes
          FROM pg_stat_all_tables
          WHERE schemaname = 'public'
            AND relname IN ('title_ratings', 'title_episodes')
        )
        SELECT
          COALESCE(NULLIF((SELECT rating_count FROM active_snapshot), 0), (SELECT ratings FROM table_estimates), 0) AS ratings,
          COALESCE(NULLIF((SELECT episode_count FROM active_snapshot), 0), (SELECT episodes FROM table_estimates), 0) AS episodes,
          (SELECT COUNT(*) FROM imdb_snapshots) AS snapshots
      `;
      const r = rows[0];
      return {
        ratings: Number(r.ratings),
        episodes: Number(r.episodes),
        snapshots: Number(r.snapshots),
      };
    });
    return c.json(stats);
  } catch (e) {
    console.error("get stats failed", e);
    return errInternal(c);
  }
});

export default app;
