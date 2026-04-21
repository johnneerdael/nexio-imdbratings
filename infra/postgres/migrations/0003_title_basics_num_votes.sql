ALTER TABLE title_basics ADD COLUMN IF NOT EXISTS num_votes INTEGER NOT NULL DEFAULT 0;

UPDATE title_basics b
SET num_votes = r.num_votes
FROM title_ratings r
WHERE r.tconst = b.tconst
  AND b.num_votes <> r.num_votes;

CREATE INDEX IF NOT EXISTS idx_title_basics_primary_title_trgm_popular
    ON title_basics USING gin (primary_title gin_trgm_ops)
    WHERE num_votes > 0;
