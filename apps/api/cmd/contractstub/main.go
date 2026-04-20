package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nexio-imdb/apps/api/internal/api"
	"nexio-imdb/apps/api/internal/auth"
	"nexio-imdb/apps/api/internal/imdb"
)

const (
	defaultAddress = "127.0.0.1:4010"
	contractAPIKey = "contract-test-key"
)

func main() {
	addr := os.Getenv("CONTRACT_STUB_ADDRESS")
	if addr == "" {
		addr = defaultAddress
	}

	server := &http.Server{
		Addr:    addr,
		Handler: api.NewRouter(contractService{}, contractAuthenticator{}, nil),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("contract stub listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

type contractAuthenticator struct{}

func (contractAuthenticator) Authenticate(context.Context, string) (*auth.Principal, error) {
	return &auth.Principal{KeyID: 1, Prefix: "contract"}, nil
}

type contractService struct{}

func (contractService) Ready(context.Context) error {
	return nil
}

func (contractService) ListSnapshots(context.Context) ([]imdb.Snapshot, error) {
	importedAt := time.Date(2026, 3, 22, 18, 2, 11, 0, time.UTC)
	completedAt := time.Date(2026, 3, 22, 18, 5, 0, 0, time.UTC)
	sourceUpdatedAt := time.Date(2026, 3, 22, 12, 34, 20, 0, time.UTC)

	return []imdb.Snapshot{
		{
			ID:              1,
			Dataset:         "title.ratings.tsv.gz",
			Status:          "active",
			SyncMode:        "full",
			DatasetVersion:  "2026-03-22",
			ImportedAt:      importedAt,
			SourceUpdatedAt: &sourceUpdatedAt,
			SourceETag:      `W/"etag-value"`,
			IsActive:        true,
			RatingCount:     1600000,
			EpisodeCount:    6200000,
			Notes:           "Imported successfully.",
			SourceURL:       "https://datasets.imdbws.com/title.ratings.tsv.gz",
			CompletedAt:     &completedAt,
		},
	}, nil
}

func (contractService) GetStats(context.Context) (imdb.Stats, error) {
	return imdb.Stats{
		Ratings:   1600000,
		Episodes:  6200000,
		Snapshots: 12,
	}, nil
}

func (contractService) GetRating(_ context.Context, tconst string) (imdb.Rating, error) {
	rating, ok := ratingFixtures[tconst]
	if !ok {
		return imdb.Rating{}, imdb.ErrNotFound
	}
	return rating, nil
}

func (contractService) SearchTitles(_ context.Context, query imdb.TitleSearchQuery) (imdb.TitleSearchResponse, error) {
	year := 1999
	results := []imdb.TitleSearchResult{
		{
			Tconst:       "tt0133093",
			TitleType:    "movie",
			PrimaryTitle: "The Matrix",
			StartYear:    &year,
		},
	}
	filtered := results[:0]
	typeSet := make(map[string]bool, len(query.Types))
	for _, t := range query.Types {
		typeSet[t] = true
	}
	for _, r := range results {
		if typeSet[r.TitleType] {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) > query.Limit {
		filtered = filtered[:query.Limit]
	}
	return imdb.TitleSearchResponse{
		Results: filtered,
		Meta:    imdb.TitleSearchMeta{SnapshotID: 1, Count: len(filtered)},
	}, nil
}

func (contractService) GetRatingWithEpisodes(_ context.Context, tconst string) (imdb.RatingWithEpisodes, error) {
	rating, hasRating := ratingFixtures[tconst]
	parentTconst, isEpisode := episodeParentByTconst[tconst]
	switch {
	case episodeFixtures[tconst] != nil:
		return imdb.RatingWithEpisodes{
			RequestTconst:        tconst,
			Rating:               ratingPtr(rating, hasRating),
			EpisodesParentTconst: tconst,
			Episodes:             append([]imdb.EpisodeRating(nil), episodeFixtures[tconst]...),
		}, nil
	case isEpisode:
		episodes := append([]imdb.EpisodeRating(nil), episodeFixtures[parentTconst]...)
		return imdb.RatingWithEpisodes{
			RequestTconst:        tconst,
			Rating:               ratingPtr(rating, hasRating),
			EpisodesParentTconst: parentTconst,
			Episodes:             episodes,
		}, nil
	case hasRating:
		return imdb.RatingWithEpisodes{
			RequestTconst: tconst,
			Rating:        &rating,
			Episodes:      []imdb.EpisodeRating{},
		}, nil
	default:
		return imdb.RatingWithEpisodes{}, imdb.ErrNotFound
	}
}

var ratingFixtures = map[string]imdb.Rating{
	"tt32459853": {
		Tconst:        "tt32459853",
		AverageRating: 7.8,
		NumVotes:      1500,
	},
	"tt0944947": {
		Tconst:        "tt0944947",
		AverageRating: 9.2,
		NumVotes:      2400000,
	},
	"tt1480055": {
		Tconst:        "tt1480055",
		AverageRating: 8.9,
		NumVotes:      1200,
	},
}

var episodeFixtures = map[string][]imdb.EpisodeRating{
	"tt0944947": {
		{
			Tconst:        "tt1480055",
			ParentTconst:  "tt0944947",
			SeasonNumber:  intPtr(1),
			EpisodeNumber: intPtr(1),
			AverageRating: 8.9,
			NumVotes:      1200,
		},
	},
}

var episodeParentByTconst = map[string]string{
	"tt1480055": "tt0944947",
}

func ratingPtr(rating imdb.Rating, ok bool) *imdb.Rating {
	if !ok {
		return nil
	}
	copy := rating
	return &copy
}

func intPtr(value int) *int {
	return &value
}
