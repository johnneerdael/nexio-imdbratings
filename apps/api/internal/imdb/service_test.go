package imdb

import (
	"context"
	"errors"
	"testing"
)

func TestGetRatingWithEpisodesReturnsSeriesExpansionWithoutRating(t *testing.T) {
	t.Parallel()

	service := NewService(stubRepository{
		getRating: func(context.Context, string) (Rating, error) {
			return Rating{}, ErrNotFound
		},
		getEpisodeParentTconst: func(context.Context, string) (string, bool, error) {
			return "", false, nil
		},
		hasEpisodesParent: func(_ context.Context, tconst string) (bool, error) {
			if tconst != "tt-series" {
				t.Fatalf("unexpected tconst %q", tconst)
			}
			return true, nil
		},
		listEpisodeRatings: func(_ context.Context, parentTconst string) ([]EpisodeRating, error) {
			if parentTconst != "tt-series" {
				t.Fatalf("unexpected parent %q", parentTconst)
			}
			return []EpisodeRating{
				{
					Tconst:        "tt-episode-1",
					ParentTconst:  "tt-series",
					SeasonNumber:  intPtr(1),
					EpisodeNumber: intPtr(1),
					AverageRating: 8.7,
					NumVotes:      250,
				},
			}, nil
		},
	})

	result, err := service.GetRatingWithEpisodes(context.Background(), "tt-series")
	if err != nil {
		t.Fatalf("GetRatingWithEpisodes returned error: %v", err)
	}

	if result.RequestTconst != "tt-series" {
		t.Fatalf("unexpected request tconst %#v", result)
	}
	if result.Rating != nil {
		t.Fatalf("expected rating to be omitted, got %#v", result.Rating)
	}
	if result.EpisodesParentTconst != "tt-series" {
		t.Fatalf("unexpected parent %#v", result)
	}
	if len(result.Episodes) != 1 || result.Episodes[0].Tconst != "tt-episode-1" {
		t.Fatalf("unexpected episodes %#v", result.Episodes)
	}
}

func TestGetRatingWithEpisodesResolvesEpisodeParentForSiblingLookup(t *testing.T) {
	t.Parallel()

	service := NewService(stubRepository{
		getRating: func(_ context.Context, tconst string) (Rating, error) {
			if tconst != "tt-episode-1" {
				t.Fatalf("unexpected rating lookup %q", tconst)
			}
			return Rating{
				Tconst:        "tt-episode-1",
				AverageRating: 8.9,
				NumVotes:      900,
			}, nil
		},
		getEpisodeParentTconst: func(_ context.Context, tconst string) (string, bool, error) {
			if tconst != "tt-episode-1" {
				t.Fatalf("unexpected episode lookup %q", tconst)
			}
			return "tt-series", true, nil
		},
		hasEpisodesParent: func(context.Context, string) (bool, error) {
			t.Fatal("hasEpisodesParent should not be called for episode lookups")
			return false, nil
		},
		listEpisodeRatings: func(_ context.Context, parentTconst string) ([]EpisodeRating, error) {
			if parentTconst != "tt-series" {
				t.Fatalf("unexpected parent %q", parentTconst)
			}
			return []EpisodeRating{
				{
					Tconst:        "tt-episode-1",
					ParentTconst:  "tt-series",
					SeasonNumber:  intPtr(1),
					EpisodeNumber: intPtr(1),
					AverageRating: 8.9,
					NumVotes:      900,
				},
				{
					Tconst:        "tt-episode-2",
					ParentTconst:  "tt-series",
					SeasonNumber:  intPtr(1),
					EpisodeNumber: intPtr(2),
					AverageRating: 9.0,
					NumVotes:      850,
				},
			}, nil
		},
	})

	result, err := service.GetRatingWithEpisodes(context.Background(), "tt-episode-1")
	if err != nil {
		t.Fatalf("GetRatingWithEpisodes returned error: %v", err)
	}

	if result.RequestTconst != "tt-episode-1" {
		t.Fatalf("unexpected request tconst %#v", result)
	}
	if result.Rating == nil || result.Rating.Tconst != "tt-episode-1" {
		t.Fatalf("unexpected rating %#v", result.Rating)
	}
	if result.EpisodesParentTconst != "tt-series" {
		t.Fatalf("unexpected parent %#v", result)
	}
	if len(result.Episodes) != 2 || result.Episodes[1].Tconst != "tt-episode-2" {
		t.Fatalf("unexpected episodes %#v", result.Episodes)
	}
}

func TestGetRatingWithEpisodesReturnsPlainRatingWhenNoEpisodeRelationExists(t *testing.T) {
	t.Parallel()

	service := NewService(stubRepository{
		getRating: func(context.Context, string) (Rating, error) {
			return Rating{
				Tconst:        "tt-movie",
				AverageRating: 7.1,
				NumVotes:      45,
			}, nil
		},
		getEpisodeParentTconst: func(context.Context, string) (string, bool, error) {
			return "", false, nil
		},
		hasEpisodesParent: func(context.Context, string) (bool, error) {
			return false, nil
		},
	})

	result, err := service.GetRatingWithEpisodes(context.Background(), "tt-movie")
	if err != nil {
		t.Fatalf("GetRatingWithEpisodes returned error: %v", err)
	}

	if result.RequestTconst != "tt-movie" {
		t.Fatalf("unexpected request tconst %#v", result)
	}
	if result.Rating == nil || result.Rating.Tconst != "tt-movie" {
		t.Fatalf("unexpected rating %#v", result.Rating)
	}
	if result.EpisodesParentTconst != "" {
		t.Fatalf("expected no episodes parent, got %#v", result)
	}
	if len(result.Episodes) != 0 {
		t.Fatalf("expected empty episodes, got %#v", result.Episodes)
	}
}

func TestGetRatingWithEpisodesReturnsNotFoundWithoutRatingOrEpisodeRelation(t *testing.T) {
	t.Parallel()

	service := NewService(stubRepository{
		getRating: func(context.Context, string) (Rating, error) {
			return Rating{}, ErrNotFound
		},
		getEpisodeParentTconst: func(context.Context, string) (string, bool, error) {
			return "", false, nil
		},
		hasEpisodesParent: func(context.Context, string) (bool, error) {
			return false, nil
		},
	})

	_, err := service.GetRatingWithEpisodes(context.Background(), "tt-missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

type stubRepository struct {
	ping                   func(context.Context) error
	listSnapshots          func(context.Context) ([]Snapshot, error)
	getStats               func(context.Context) (Stats, error)
	getRating              func(context.Context, string) (Rating, error)
	getEpisodeParentTconst func(context.Context, string) (string, bool, error)
	hasEpisodesParent      func(context.Context, string) (bool, error)
	listEpisodeRatings     func(context.Context, string) ([]EpisodeRating, error)
}

func (s stubRepository) Ping(ctx context.Context) error {
	if s.ping != nil {
		return s.ping(ctx)
	}
	return nil
}

func (s stubRepository) ListSnapshots(ctx context.Context) ([]Snapshot, error) {
	if s.listSnapshots != nil {
		return s.listSnapshots(ctx)
	}
	return nil, nil
}

func (s stubRepository) GetStats(ctx context.Context) (Stats, error) {
	if s.getStats != nil {
		return s.getStats(ctx)
	}
	return Stats{}, nil
}

func (s stubRepository) GetRating(ctx context.Context, tconst string) (Rating, error) {
	if s.getRating != nil {
		return s.getRating(ctx, tconst)
	}
	return Rating{}, nil
}

func (s stubRepository) GetEpisodeParentTconst(ctx context.Context, tconst string) (string, bool, error) {
	if s.getEpisodeParentTconst != nil {
		return s.getEpisodeParentTconst(ctx, tconst)
	}
	return "", false, nil
}

func (s stubRepository) HasEpisodesParent(ctx context.Context, tconst string) (bool, error) {
	if s.hasEpisodesParent != nil {
		return s.hasEpisodesParent(ctx, tconst)
	}
	return false, nil
}

func (s stubRepository) ListEpisodeRatings(ctx context.Context, parentTconst string) ([]EpisodeRating, error) {
	if s.listEpisodeRatings != nil {
		return s.listEpisodeRatings(ctx, parentTconst)
	}
	return nil, nil
}

func intPtr(v int) *int { return &v }
