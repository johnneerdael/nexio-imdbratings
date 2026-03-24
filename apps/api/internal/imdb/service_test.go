package imdb

import (
	"context"
	"errors"
	"testing"
)

func TestGetRatingWithEpisodesReturnsSeriesExpansionWithoutRating(t *testing.T) {
	t.Parallel()

	service := NewService(stubRepository{
		getRatingWithEpisodes: func(_ context.Context, tconst string) (RatingWithEpisodes, error) {
			if tconst != "tt-series" {
				t.Fatalf("unexpected tconst %q", tconst)
			}
			return RatingWithEpisodes{
				RequestTconst:        "tt-series",
				EpisodesParentTconst: "tt-series",
				Episodes: []EpisodeRating{
					{
						Tconst:        "tt-episode-1",
						ParentTconst:  "tt-series",
						SeasonNumber:  intPtr(1),
						EpisodeNumber: intPtr(1),
						AverageRating: 8.7,
						NumVotes:      250,
					},
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
		getRatingWithEpisodes: func(_ context.Context, tconst string) (RatingWithEpisodes, error) {
			if tconst != "tt-episode-1" {
				t.Fatalf("unexpected lookup %q", tconst)
			}
			return RatingWithEpisodes{
				RequestTconst:        "tt-episode-1",
				EpisodesParentTconst: "tt-series",
				Rating: &Rating{
					Tconst:        "tt-episode-1",
					AverageRating: 8.9,
					NumVotes:      900,
				},
				Episodes: []EpisodeRating{
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
		getRatingWithEpisodes: func(context.Context, string) (RatingWithEpisodes, error) {
			return RatingWithEpisodes{
				RequestTconst: "tt-movie",
				Rating: &Rating{
					Tconst:        "tt-movie",
					AverageRating: 7.1,
					NumVotes:      45,
				},
				Episodes: []EpisodeRating{},
			}, nil
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
		getRatingWithEpisodes: func(context.Context, string) (RatingWithEpisodes, error) {
			return RatingWithEpisodes{}, ErrNotFound
		},
	})

	_, err := service.GetRatingWithEpisodes(context.Background(), "tt-missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

type stubRepository struct {
	ping                  func(context.Context) error
	listSnapshots         func(context.Context) ([]Snapshot, error)
	getStats              func(context.Context) (Stats, error)
	getRating             func(context.Context, string) (Rating, error)
	getRatingWithEpisodes func(context.Context, string) (RatingWithEpisodes, error)
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

func (s stubRepository) GetRatingWithEpisodes(ctx context.Context, tconst string) (RatingWithEpisodes, error) {
	if s.getRatingWithEpisodes != nil {
		return s.getRatingWithEpisodes(ctx, tconst)
	}
	return RatingWithEpisodes{}, nil
}

func intPtr(v int) *int { return &v }
