package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"nexio-imdb/apps/api/internal/auth"
	"nexio-imdb/apps/api/internal/imdb"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) GetAPIKeyByPrefix(ctx context.Context, prefix string) (auth.KeyRecord, error) {
	var record auth.KeyRecord
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, name, key_prefix, key_hash, revoked_at, expires_at
		FROM api_keys
		WHERE key_prefix = $1
		LIMIT 1
	`, prefix).Scan(
		&record.ID,
		&record.UserID,
		&record.Name,
		&record.Prefix,
		&record.Hash,
		&record.RevokedAt,
		&record.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.KeyRecord{}, auth.ErrInvalidAPIKey
		}
		return auth.KeyRecord{}, fmt.Errorf("get api key by prefix: %w", err)
	}
	return record, nil
}

func (s *Store) TouchAPIKeyLastUsed(ctx context.Context, id int64, lastUsedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE api_keys
		SET last_used_at = $2
		WHERE id = $1
	`, id, lastUsedAt.UTC())
	if err != nil {
		return fmt.Errorf("touch api key last used: %w", err)
	}
	return nil
}

func (s *Store) ListSnapshots(ctx context.Context) ([]imdb.Snapshot, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			id,
			dataset_name,
			status,
			sync_mode,
			COALESCE(dataset_version, ''),
			imported_at,
			source_updated_at,
			COALESCE(source_etag, ''),
			is_active,
			rating_count,
			episode_count,
			COALESCE(notes, ''),
			COALESCE(source_url, ''),
			completed_at
		FROM imdb_snapshots
		ORDER BY imported_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	defer rows.Close()

	var items []imdb.Snapshot
	for rows.Next() {
		var item imdb.Snapshot
		if err := rows.Scan(
			&item.ID,
			&item.Dataset,
			&item.Status,
			&item.SyncMode,
			&item.DatasetVersion,
			&item.ImportedAt,
			&item.SourceUpdatedAt,
			&item.SourceETag,
			&item.IsActive,
			&item.RatingCount,
			&item.EpisodeCount,
			&item.Notes,
			&item.SourceURL,
			&item.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate snapshots: %w", err)
	}
	return items, nil
}

func (s *Store) GetStats(ctx context.Context) (imdb.Stats, error) {
	var stats imdb.Stats
	err := s.pool.QueryRow(ctx, `
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
	`).Scan(&stats.Ratings, &stats.Episodes, &stats.Snapshots)
	if err != nil {
		return imdb.Stats{}, fmt.Errorf("get stats: %w", err)
	}
	return stats, nil
}

func (s *Store) GetRating(ctx context.Context, tconst string) (imdb.Rating, error) {
	var rating imdb.Rating
	err := s.pool.QueryRow(ctx, `
		SELECT tconst, average_rating, num_votes
		FROM title_ratings
		WHERE tconst = $1
	`, tconst).Scan(&rating.Tconst, &rating.AverageRating, &rating.NumVotes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return imdb.Rating{}, imdb.ErrNotFound
		}
		return imdb.Rating{}, fmt.Errorf("get rating: %w", err)
	}
	return rating, nil
}

func (s *Store) GetRatingWithEpisodes(ctx context.Context, tconst string) (imdb.RatingWithEpisodes, error) {
	result := imdb.RatingWithEpisodes{
		RequestTconst: tconst,
		Episodes:      []imdb.EpisodeRating{},
	}

	rating, err := s.GetRating(ctx, tconst)
	switch {
	case err == nil:
		result.Rating = &rating
	case !errors.Is(err, imdb.ErrNotFound):
		return imdb.RatingWithEpisodes{}, err
	}

	parentTconst, isEpisode, err := s.getEpisodeParentTconst(ctx, tconst)
	if err != nil {
		return imdb.RatingWithEpisodes{}, err
	}
	if isEpisode {
		result.EpisodesParentTconst = parentTconst
		result.Episodes, err = s.listEpisodeRatings(ctx, parentTconst)
		if err != nil {
			return imdb.RatingWithEpisodes{}, err
		}
		return result, nil
	}

	hasEpisodes, err := s.hasEpisodesParent(ctx, tconst)
	if err != nil {
		return imdb.RatingWithEpisodes{}, err
	}
	if hasEpisodes {
		result.EpisodesParentTconst = tconst
		result.Episodes, err = s.listEpisodeRatings(ctx, tconst)
		if err != nil {
			return imdb.RatingWithEpisodes{}, err
		}
		return result, nil
	}

	if result.Rating != nil {
		return result, nil
	}

	return imdb.RatingWithEpisodes{}, imdb.ErrNotFound
}

func (s *Store) getEpisodeParentTconst(ctx context.Context, tconst string) (string, bool, error) {
	var parentTconst string
	err := s.pool.QueryRow(ctx, `
		SELECT parent_tconst
		FROM title_episodes
		WHERE tconst = $1
	`, tconst).Scan(&parentTconst)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get episode parent tconst: %w", err)
	}
	return parentTconst, true, nil
}

func (s *Store) hasEpisodesParent(ctx context.Context, tconst string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM title_episodes
			WHERE parent_tconst = $1
		)
	`, tconst).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check episodes parent: %w", err)
	}
	return exists, nil
}

func (s *Store) listEpisodeRatings(ctx context.Context, parentTconst string) ([]imdb.EpisodeRating, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			e.tconst,
			e.parent_tconst,
			e.season_number,
			e.episode_number,
			r.average_rating,
			r.num_votes
		FROM title_episodes e
		JOIN title_ratings r ON r.tconst = e.tconst
		WHERE e.parent_tconst = $1
		ORDER BY e.season_number NULLS FIRST, e.episode_number NULLS FIRST, e.tconst
	`, parentTconst)
	if err != nil {
		return nil, fmt.Errorf("list episode ratings: %w", err)
	}
	defer rows.Close()

	var items []imdb.EpisodeRating
	for rows.Next() {
		var item imdb.EpisodeRating
		if err := rows.Scan(
			&item.Tconst,
			&item.ParentTconst,
			&item.SeasonNumber,
			&item.EpisodeNumber,
			&item.AverageRating,
			&item.NumVotes,
		); err != nil {
			return nil, fmt.Errorf("scan episode rating: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate episode ratings: %w", err)
	}
	if items == nil {
		return []imdb.EpisodeRating{}, nil
	}
	return items, nil
}
