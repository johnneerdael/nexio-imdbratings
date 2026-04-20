package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nexio-imdb/apps/api/internal/auth"
)

func TestRequestRateLimiterCountsMetaRequests(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)
	limiter := NewRequestRateLimiter(RateLimitConfig{
		Enabled:         true,
		TokensPerSecond: 1,
		Burst:           2,
		EpisodesCost:    8,
		BulkDivisor:     25,
	})
	limiter.now = func() time.Time { return now }

	principal := &auth.Principal{KeyID: 42, Prefix: "meta"}
	req := httptest.NewRequest(http.MethodGet, "/v1/meta/stats", nil)

	for i := 0; i < 2; i++ {
		decision, err := limiter.Allow(principal, req)
		if err != nil {
			t.Fatalf("Allow returned error: %v", err)
		}
		if !decision.Allowed {
			t.Fatalf("expected request %d to be allowed", i+1)
		}
	}

	decision, err := limiter.Allow(principal, req)
	if err != nil {
		t.Fatalf("Allow returned error: %v", err)
	}
	if decision.Allowed {
		t.Fatalf("expected third request to be rate limited")
	}
	if decision.RetryAfter != time.Second {
		t.Fatalf("expected retry-after 1s, got %v", decision.RetryAfter)
	}
}

func TestRequestRateLimiterChargesEpisodesRequestsHeavier(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)
	limiter := NewRequestRateLimiter(RateLimitConfig{
		Enabled:         true,
		TokensPerSecond: 1,
		Burst:           10,
		EpisodesCost:    8,
		BulkDivisor:     25,
	})
	limiter.now = func() time.Time { return now }

	principal := &auth.Principal{KeyID: 7, Prefix: "episodes"}
	req := httptest.NewRequest(http.MethodGet, "/v1/ratings/tt0239195?episodes=true", nil)

	first, err := limiter.Allow(principal, req)
	if err != nil {
		t.Fatalf("Allow returned error: %v", err)
	}
	if !first.Allowed {
		t.Fatalf("expected first episodes request to be allowed")
	}

	second, err := limiter.Allow(principal, req)
	if err != nil {
		t.Fatalf("Allow returned error: %v", err)
	}
	if second.Allowed {
		t.Fatalf("expected second episodes request to be rate limited")
	}
	if second.RetryAfter != 6*time.Second {
		t.Fatalf("expected retry-after 6s, got %v", second.RetryAfter)
	}
}

func TestRequestRateLimiterWeightsBulkByIdentifierCountAndPreservesBody(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)
	limiter := NewRequestRateLimiter(RateLimitConfig{
		Enabled:         true,
		TokensPerSecond: 1,
		Burst:           4,
		EpisodesCost:    8,
		BulkDivisor:     25,
	})
	limiter.now = func() time.Time { return now }

	principal := &auth.Principal{KeyID: 88, Prefix: "bulk"}
	payload := []byte(`{"identifiers":["tt-001","tt-002","tt-003","tt-004","tt-005","tt-006","tt-007","tt-008","tt-009","tt-010","tt-011","tt-012","tt-013","tt-014","tt-015","tt-016","tt-017","tt-018","tt-019","tt-020","tt-021","tt-022","tt-023","tt-024","tt-025","tt-026","tt-027","tt-028","tt-029","tt-030","tt-031","tt-032","tt-033","tt-034","tt-035","tt-036","tt-037","tt-038","tt-039","tt-040","tt-041","tt-042","tt-043","tt-044","tt-045","tt-046","tt-047","tt-048","tt-049","tt-050","tt-051"]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/ratings/bulk", bytes.NewReader(payload))

	first, err := limiter.Allow(principal, req)
	if err != nil {
		t.Fatalf("Allow returned error: %v", err)
	}
	if !first.Allowed {
		t.Fatalf("expected first bulk request to be allowed")
	}

	restored, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if !bytes.Equal(restored, payload) {
		t.Fatalf("expected body to be restored after rate-limit inspection")
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/ratings/bulk", bytes.NewReader(payload))
	second, err := limiter.Allow(principal, req)
	if err != nil {
		t.Fatalf("Allow returned error: %v", err)
	}
	if second.Allowed {
		t.Fatalf("expected second weighted bulk request to be rate limited")
	}
	if second.RetryAfter != 2*time.Second {
		t.Fatalf("expected retry-after 2s, got %v", second.RetryAfter)
	}
}

func TestRouterReturns429WhenRateLimitExceeded(t *testing.T) {
	t.Parallel()

	router := NewRouter(stubService{}, stubAuthenticator{
		authenticate: func(context.Context, string) (*auth.Principal, error) {
			return &auth.Principal{KeyID: 1, Prefix: "limited"}, nil
		},
	}, stubRateLimiter{
		allow: func(*auth.Principal, *http.Request) (RateLimitDecision, error) {
			return RateLimitDecision{
				Allowed:    false,
				RetryAfter: 3 * time.Second,
			}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/meta/stats", nil)
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "3" {
		t.Fatalf("expected Retry-After header 3, got %q", got)
	}
	if body := rec.Body.String(); body != "{\"error\":{\"code\":\"rate_limited\",\"message\":\"rate limit exceeded\"}}\n" {
		t.Fatalf("unexpected response body %s", body)
	}
}

type stubRateLimiter struct {
	allow        func(*auth.Principal, *http.Request) (RateLimitDecision, error)
	allowConnect func(*auth.Principal) (RateLimitDecision, error)
}

func (s stubRateLimiter) Allow(principal *auth.Principal, r *http.Request) (RateLimitDecision, error) {
	if s.allow != nil {
		return s.allow(principal, r)
	}
	return RateLimitDecision{Allowed: true}, nil
}

func (s stubRateLimiter) AllowConnect(principal *auth.Principal) (RateLimitDecision, error) {
	if s.allowConnect != nil {
		return s.allowConnect(principal)
	}
	return RateLimitDecision{Allowed: true}, nil
}
