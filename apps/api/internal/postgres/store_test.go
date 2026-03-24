package postgres

import (
	"context"
	"errors"
	"testing"

	"nexio-imdb/apps/api/internal/imdb"
)

func TestResolveRatingWithEpisodesReturnsSeriesRatingAndEpisodes(t *testing.T) {
	t.Parallel()

	result, err := resolveRatingWithEpisodes(context.Background(), "tt-series", ratingEpisodesResolver{
		getRating: func(context.Context, string) (imdb.Rating, error) {
			return imdb.Rating{
				Tconst:        "tt-series",
				AverageRating: 9.3,
				NumVotes:      4000,
			}, nil
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
		listEpisodeRatings: func(_ context.Context, parent string) ([]imdb.EpisodeRating, error) {
			if parent != "tt-series" {
				t.Fatalf("unexpected parent %q", parent)
			}
			return []imdb.EpisodeRating{
				{
					Tconst:        "tt-ep1",
					ParentTconst:  "tt-series",
					SeasonNumber:  intPtr(1),
					EpisodeNumber: intPtr(1),
					AverageRating: 8.5,
					NumVotes:      100,
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("resolveRatingWithEpisodes returned error: %v", err)
	}

	if result.Rating == nil || result.Rating.Tconst != "tt-series" {
		t.Fatalf("unexpected rating %#v", result.Rating)
	}
	if result.EpisodesParentTconst != "tt-series" {
		t.Fatalf("unexpected parent %#v", result)
	}
	if len(result.Episodes) != 1 || result.Episodes[0].Tconst != "tt-ep1" {
		t.Fatalf("unexpected episodes %#v", result.Episodes)
	}
}

func TestResolveRatingWithEpisodesUsesEpisodeParentForSiblingLookup(t *testing.T) {
	t.Parallel()

	result, err := resolveRatingWithEpisodes(context.Background(), "tt-ep1", ratingEpisodesResolver{
		getRating: func(context.Context, string) (imdb.Rating, error) {
			return imdb.Rating{
				Tconst:        "tt-ep1",
				AverageRating: 8.6,
				NumVotes:      150,
			}, nil
		},
		getEpisodeParentTconst: func(_ context.Context, tconst string) (string, bool, error) {
			if tconst != "tt-ep1" {
				t.Fatalf("unexpected tconst %q", tconst)
			}
			return "tt-series", true, nil
		},
		hasEpisodesParent: func(context.Context, string) (bool, error) {
			t.Fatal("hasEpisodesParent should not be called for episode lookups")
			return false, nil
		},
		listEpisodeRatings: func(_ context.Context, parent string) ([]imdb.EpisodeRating, error) {
			if parent != "tt-series" {
				t.Fatalf("unexpected parent %q", parent)
			}
			return []imdb.EpisodeRating{{Tconst: "tt-ep1", ParentTconst: "tt-series"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("resolveRatingWithEpisodes returned error: %v", err)
	}

	if result.EpisodesParentTconst != "tt-series" {
		t.Fatalf("unexpected parent %#v", result)
	}
	if len(result.Episodes) != 1 || result.Episodes[0].ParentTconst != "tt-series" {
		t.Fatalf("unexpected episodes %#v", result.Episodes)
	}
}

func TestResolveRatingWithEpisodesReturnsPlainRatingWhenUnrelatedToEpisodes(t *testing.T) {
	t.Parallel()

	result, err := resolveRatingWithEpisodes(context.Background(), "tt-movie", ratingEpisodesResolver{
		getRating: func(context.Context, string) (imdb.Rating, error) {
			return imdb.Rating{
				Tconst:        "tt-movie",
				AverageRating: 7.2,
				NumVotes:      80,
			}, nil
		},
		getEpisodeParentTconst: func(context.Context, string) (string, bool, error) {
			return "", false, nil
		},
		hasEpisodesParent: func(context.Context, string) (bool, error) {
			return false, nil
		},
	})
	if err != nil {
		t.Fatalf("resolveRatingWithEpisodes returned error: %v", err)
	}

	if result.Rating == nil || result.Rating.Tconst != "tt-movie" {
		t.Fatalf("unexpected rating %#v", result.Rating)
	}
	if result.EpisodesParentTconst != "" || len(result.Episodes) != 0 {
		t.Fatalf("unexpected episodes metadata %#v", result)
	}
}

func TestResolveRatingWithEpisodesReturnsNotFoundWhenNothingMatches(t *testing.T) {
	t.Parallel()

	_, err := resolveRatingWithEpisodes(context.Background(), "tt-missing", ratingEpisodesResolver{
		getRating: func(context.Context, string) (imdb.Rating, error) {
			return imdb.Rating{}, imdb.ErrNotFound
		},
		getEpisodeParentTconst: func(context.Context, string) (string, bool, error) {
			return "", false, nil
		},
		hasEpisodesParent: func(context.Context, string) (bool, error) {
			return false, nil
		},
	})
	if !errors.Is(err, imdb.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func intPtr(v int) *int { return &v }
