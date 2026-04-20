package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nexio-imdb/apps/api/internal/auth"
	"nexio-imdb/apps/api/internal/imdb"
)

func TestHealthzDoesNotRequireAuth(t *testing.T) {
	t.Parallel()

	router := NewRouter(stubService{}, stubAuthenticator{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %#v", body)
	}
}

func TestReadyzDoesNotRequireAuth(t *testing.T) {
	t.Parallel()

	router := NewRouter(stubService{
		ready: func(context.Context) error { return nil },
	}, stubAuthenticator{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestV1RoutesRequireAPIKey(t *testing.T) {
	t.Parallel()

	router := NewRouter(stubService{}, stubAuthenticator{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/meta/stats", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestGetStatsReturnsRatingsOnlyPayload(t *testing.T) {
	t.Parallel()

	router := NewRouter(stubService{
		getStats: func(context.Context) (imdb.Stats, error) {
			return imdb.Stats{
				Ratings:   101,
				Episodes:  202,
				Snapshots: 3,
			}, nil
		},
	}, stubAuthenticator{
		authenticate: func(context.Context, string) (*auth.Principal, error) {
			return &auth.Principal{KeyID: 1, Prefix: "test"}, nil
		},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/meta/stats", nil)
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body imdb.Stats
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Ratings != 101 || body.Episodes != 202 || body.Snapshots != 3 {
		t.Fatalf("unexpected stats %#v", body)
	}
}

func TestGetRatingSupportsXAPIKey(t *testing.T) {
	t.Parallel()

	router := NewRouter(stubService{
		getRating: func(_ context.Context, tconst string) (imdb.Rating, error) {
			return imdb.Rating{
				Tconst:        tconst,
				AverageRating: 8.8,
				NumVotes:      42,
			}, nil
		},
	}, stubAuthenticator{
		authenticate: func(_ context.Context, key string) (*auth.Principal, error) {
			if key != "valid-key" {
				t.Fatalf("unexpected key %q", key)
			}
			return &auth.Principal{KeyID: 1, Prefix: "test"}, nil
		},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/ratings/tt1375666", nil)
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body imdb.Rating
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Tconst != "tt1375666" || body.AverageRating != 8.8 || body.NumVotes != 42 {
		t.Fatalf("unexpected rating %#v", body)
	}
}

func TestGetRatingWithEpisodesReturnsWrapper(t *testing.T) {
	t.Parallel()

	router := NewRouter(stubService{
		getRatingWithEpisodes: func(_ context.Context, tconst string) (imdb.RatingWithEpisodes, error) {
			if tconst != "tt0944947" {
				t.Fatalf("unexpected tconst %q", tconst)
			}
			return imdb.RatingWithEpisodes{
				RequestTconst: "tt0944947",
				Rating: &imdb.Rating{
					Tconst:        "tt0944947",
					AverageRating: 9.2,
					NumVotes:      5000,
				},
				EpisodesParentTconst: "tt0944947",
				Episodes: []imdb.EpisodeRating{
					{
						Tconst:        "tt1480055",
						ParentTconst:  "tt0944947",
						SeasonNumber:  intPtr(1),
						EpisodeNumber: intPtr(1),
						AverageRating: 8.9,
						NumVotes:      1200,
					},
				},
			}, nil
		},
	}, stubAuthenticator{
		authenticate: func(context.Context, string) (*auth.Principal, error) {
			return &auth.Principal{KeyID: 1, Prefix: "test"}, nil
		},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/ratings/tt0944947?episodes=true", nil)
	req.Header.Set("Authorization", "Bearer valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body imdb.RatingWithEpisodes
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.RequestTconst != "tt0944947" {
		t.Fatalf("unexpected request tconst %#v", body)
	}
	if body.Rating == nil || body.Rating.Tconst != "tt0944947" {
		t.Fatalf("unexpected rating %#v", body.Rating)
	}
	if body.EpisodesParentTconst != "tt0944947" {
		t.Fatalf("unexpected parent %#v", body)
	}
	if len(body.Episodes) != 1 || body.Episodes[0].Tconst != "tt1480055" {
		t.Fatalf("unexpected episodes %#v", body.Episodes)
	}
}

func TestBulkGetRatingsRejectsTrailingJSONGarbage(t *testing.T) {
	t.Parallel()

	router := NewRouter(stubService{}, stubAuthenticator{
		authenticate: func(context.Context, string) (*auth.Principal, error) {
			return &auth.Principal{KeyID: 1, Prefix: "test"}, nil
		},
	}, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/ratings/bulk", bytes.NewBufferString(`{"identifiers":["tt-a"]}{"extra":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBulkGetRatingsReturnsResultsAndMissing(t *testing.T) {
	t.Parallel()

	router := NewRouter(stubService{
		getRating: func(_ context.Context, tconst string) (imdb.Rating, error) {
			if tconst == "tt-missing" {
				return imdb.Rating{}, imdb.ErrNotFound
			}
			return imdb.Rating{
				Tconst:        tconst,
				AverageRating: 7.5,
				NumVotes:      10,
			}, nil
		},
	}, stubAuthenticator{
		authenticate: func(context.Context, string) (*auth.Principal, error) {
			return &auth.Principal{KeyID: 1, Prefix: "test"}, nil
		},
	}, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/ratings/bulk", bytes.NewBufferString(`{"identifiers":["tt-a","tt-missing","tt-b"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Results []imdb.Rating `json:"results"`
		Missing []string      `json:"missing"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Results) != 2 || body.Results[0].Tconst != "tt-a" || body.Results[1].Tconst != "tt-b" {
		t.Fatalf("unexpected results %#v", body.Results)
	}
	if len(body.Missing) != 1 || body.Missing[0] != "tt-missing" {
		t.Fatalf("unexpected missing %#v", body.Missing)
	}
}

func TestBulkGetRatingsWithEpisodesReturnsWrappersAndMissing(t *testing.T) {
	t.Parallel()

	router := NewRouter(stubService{
		getRatingWithEpisodes: func(_ context.Context, tconst string) (imdb.RatingWithEpisodes, error) {
			if tconst == "tt-missing" {
				return imdb.RatingWithEpisodes{}, imdb.ErrNotFound
			}
			return imdb.RatingWithEpisodes{
				RequestTconst: tconst,
				Rating: &imdb.Rating{
					Tconst:        tconst,
					AverageRating: 9.1,
					NumVotes:      100,
				},
				Episodes: []imdb.EpisodeRating{},
			}, nil
		},
	}, stubAuthenticator{
		authenticate: func(context.Context, string) (*auth.Principal, error) {
			return &auth.Principal{KeyID: 1, Prefix: "test"}, nil
		},
	}, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/ratings/bulk?episodes=true", bytes.NewBufferString(`{"identifiers":["tt-a","tt-missing"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Results []imdb.RatingWithEpisodes `json:"results"`
		Missing []string                  `json:"missing"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Results) != 1 || body.Results[0].RequestTconst != "tt-a" {
		t.Fatalf("unexpected results %#v", body.Results)
	}
	if len(body.Missing) != 1 || body.Missing[0] != "tt-missing" {
		t.Fatalf("unexpected missing %#v", body.Missing)
	}
}

func TestBulkGetRatingsRejectsMoreThan250Identifiers(t *testing.T) {
	t.Parallel()

	identifiers := make([]string, 251)
	for i := range identifiers {
		identifiers[i] = "tt-over-limit"
	}
	payload, err := json.Marshal(map[string]any{"identifiers": identifiers})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	router := NewRouter(stubService{}, stubAuthenticator{
		authenticate: func(context.Context, string) (*auth.Principal, error) {
			return &auth.Principal{KeyID: 1, Prefix: "test"}, nil
		},
	}, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/ratings/bulk", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func newAuthRouter(svc imdb.QueryService) http.Handler {
	return NewRouter(svc, stubAuthenticator{
		authenticate: func(context.Context, string) (*auth.Principal, error) {
			return &auth.Principal{KeyID: 1, Prefix: "test"}, nil
		},
	}, nil)
}

func TestSearchTitlesRejects_q_ShorterThan2(t *testing.T) {
	t.Parallel()

	router := newAuthRouter(stubService{})
	req := httptest.NewRequest(http.MethodGet, "/v1/titles/search?q=a", nil)
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSearchTitlesRejects_q_Empty(t *testing.T) {
	t.Parallel()

	router := newAuthRouter(stubService{})
	req := httptest.NewRequest(http.MethodGet, "/v1/titles/search", nil)
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSearchTitlesRejectsUnknownType(t *testing.T) {
	t.Parallel()

	router := newAuthRouter(stubService{})
	req := httptest.NewRequest(http.MethodGet, "/v1/titles/search?q=matrix&types=short", nil)
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSearchTitlesRejectsInvalidLimit(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"0", "51", "abc", "-1"} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			router := newAuthRouter(stubService{})
			req := httptest.NewRequest(http.MethodGet, "/v1/titles/search?q=matrix&limit="+raw, nil)
			req.Header.Set("X-API-Key", "valid-key")
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("limit=%s: expected 400, got %d body=%s", raw, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestSearchTitlesDefaultsTypesToBoth(t *testing.T) {
	t.Parallel()

	var gotTypes []string
	router := newAuthRouter(stubService{
		searchTitles: func(_ context.Context, q imdb.TitleSearchQuery) (imdb.TitleSearchResponse, error) {
			gotTypes = q.Types
			return imdb.TitleSearchResponse{Results: []imdb.TitleSearchResult{}}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/titles/search?q=matrix", nil)
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(gotTypes) != 2 {
		t.Fatalf("expected 2 types, got %v", gotTypes)
	}
}

func TestSearchTitlesDefaultsLimitTo10(t *testing.T) {
	t.Parallel()

	var gotLimit int
	router := newAuthRouter(stubService{
		searchTitles: func(_ context.Context, q imdb.TitleSearchQuery) (imdb.TitleSearchResponse, error) {
			gotLimit = q.Limit
			return imdb.TitleSearchResponse{Results: []imdb.TitleSearchResult{}}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/titles/search?q=matrix", nil)
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if gotLimit != 10 {
		t.Fatalf("expected limit 10, got %d", gotLimit)
	}
}

func TestSearchTitlesSetsCacheControlHeader(t *testing.T) {
	t.Parallel()

	router := newAuthRouter(stubService{})
	req := httptest.NewRequest(http.MethodGet, "/v1/titles/search?q=matrix", nil)
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	cc := rec.Header().Get("Cache-Control")
	if cc != "public, max-age=60, stale-while-revalidate=300" {
		t.Fatalf("unexpected Cache-Control %q", cc)
	}
}

func TestSearchTitlesReturnsExpectedJSONShape(t *testing.T) {
	t.Parallel()

	year := 1999
	router := newAuthRouter(stubService{
		searchTitles: func(context.Context, imdb.TitleSearchQuery) (imdb.TitleSearchResponse, error) {
			return imdb.TitleSearchResponse{
				Results: []imdb.TitleSearchResult{
					{Tconst: "tt0133093", TitleType: "movie", PrimaryTitle: "The Matrix", StartYear: &year},
				},
				Meta: imdb.TitleSearchMeta{SnapshotID: 42, Count: 1},
			}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/titles/search?q=matrix", nil)
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body imdb.TitleSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(body.Results))
	}
	r := body.Results[0]
	if r.Tconst != "tt0133093" || r.TitleType != "movie" || r.PrimaryTitle != "The Matrix" {
		t.Fatalf("unexpected result %#v", r)
	}
	if r.StartYear == nil || *r.StartYear != 1999 {
		t.Fatalf("unexpected startYear %v", r.StartYear)
	}
	if body.Meta.SnapshotID != 42 || body.Meta.Count != 1 {
		t.Fatalf("unexpected meta %#v", body.Meta)
	}
}

func TestSearchTitlesRequiresAPIKey(t *testing.T) {
	t.Parallel()

	router := NewRouter(stubService{}, stubAuthenticator{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/titles/search?q=matrix", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestRemovedIMDbEndpointsReturn404(t *testing.T) {
	t.Parallel()

	router := NewRouter(stubService{}, stubAuthenticator{
		authenticate: func(context.Context, string) (*auth.Principal, error) {
			return &auth.Principal{KeyID: 1, Prefix: "test"}, nil
		},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/titles/tt1375666", nil)
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

type stubService struct {
	ready                 func(context.Context) error
	listSnapshots         func(context.Context) ([]imdb.Snapshot, error)
	getStats              func(context.Context) (imdb.Stats, error)
	getRating             func(context.Context, string) (imdb.Rating, error)
	getRatingWithEpisodes func(context.Context, string) (imdb.RatingWithEpisodes, error)
	searchTitles          func(context.Context, imdb.TitleSearchQuery) (imdb.TitleSearchResponse, error)
}

func (s stubService) Ready(ctx context.Context) error {
	if s.ready != nil {
		return s.ready(ctx)
	}
	return nil
}

func (s stubService) ListSnapshots(ctx context.Context) ([]imdb.Snapshot, error) {
	if s.listSnapshots != nil {
		return s.listSnapshots(ctx)
	}
	return nil, nil
}

func (s stubService) GetStats(ctx context.Context) (imdb.Stats, error) {
	if s.getStats != nil {
		return s.getStats(ctx)
	}
	return imdb.Stats{}, nil
}

func (s stubService) GetRating(ctx context.Context, tconst string) (imdb.Rating, error) {
	if s.getRating != nil {
		return s.getRating(ctx, tconst)
	}
	return imdb.Rating{}, nil
}

func (s stubService) GetRatingWithEpisodes(ctx context.Context, tconst string) (imdb.RatingWithEpisodes, error) {
	if s.getRatingWithEpisodes != nil {
		return s.getRatingWithEpisodes(ctx, tconst)
	}
	return imdb.RatingWithEpisodes{}, nil
}

func (s stubService) SearchTitles(ctx context.Context, query imdb.TitleSearchQuery) (imdb.TitleSearchResponse, error) {
	if s.searchTitles != nil {
		return s.searchTitles(ctx, query)
	}
	return imdb.TitleSearchResponse{Results: []imdb.TitleSearchResult{}}, nil
}

type stubAuthenticator struct {
	authenticate func(context.Context, string) (*auth.Principal, error)
}

func (s stubAuthenticator) Authenticate(ctx context.Context, key string) (*auth.Principal, error) {
	if s.authenticate != nil {
		return s.authenticate(ctx, key)
	}
	return nil, auth.ErrInvalidAPIKey
}

func intPtr(v int) *int { return &v }
