CREATE TABLE IF NOT EXISTS title_basics (
    tconst       TEXT PRIMARY KEY,
    title_type   TEXT NOT NULL,
    primary_title TEXT NOT NULL,
    start_year   INTEGER
);

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS idx_title_basics_primary_title_trgm
    ON title_basics USING gin (primary_title gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_title_basics_type_year
    ON title_basics (title_type, start_year DESC);

ALTER TABLE imdb_snapshots ADD COLUMN IF NOT EXISTS title_basics_count BIGINT NOT NULL DEFAULT 0;
