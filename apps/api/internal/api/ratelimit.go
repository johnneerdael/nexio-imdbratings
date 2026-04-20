package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"nexio-imdb/apps/api/internal/auth"
)

type RateLimiter interface {
	Allow(principal *auth.Principal, r *http.Request) (RateLimitDecision, error)
	AllowConnect(principal *auth.Principal) (RateLimitDecision, error)
}

type RateLimitDecision struct {
	Allowed    bool
	RetryAfter time.Duration
}

type RateLimitConfig struct {
	Enabled         bool
	TokensPerSecond int
	Burst           int
	EpisodesCost    int
	BulkDivisor     int
}

type RequestRateLimiter struct {
	cfg     RateLimitConfig
	now     func() time.Time
	mu      sync.Mutex
	buckets map[string]*tokenBucket
}

type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

func NewRequestRateLimiter(cfg RateLimitConfig) *RequestRateLimiter {
	return &RequestRateLimiter{
		cfg:     cfg,
		now:     time.Now,
		buckets: make(map[string]*tokenBucket),
	}
}

func (l *RequestRateLimiter) Allow(principal *auth.Principal, r *http.Request) (RateLimitDecision, error) {
	if l == nil || !l.cfg.Enabled || principal == nil {
		return RateLimitDecision{Allowed: true}, nil
	}

	cost, err := l.requestCost(r)
	if err != nil {
		return RateLimitDecision{}, err
	}

	now := l.now()
	key := principalBucketKey(principal)
	refillRate := float64(l.cfg.TokensPerSecond)
	burst := float64(l.cfg.Burst)

	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, ok := l.buckets[key]
	if !ok {
		bucket = &tokenBucket{
			tokens:     burst,
			lastRefill: now,
		}
		l.buckets[key] = bucket
	}

	elapsed := now.Sub(bucket.lastRefill).Seconds()
	if elapsed > 0 {
		bucket.tokens = math.Min(burst, bucket.tokens+(elapsed*refillRate))
		bucket.lastRefill = now
	}

	if bucket.tokens >= cost {
		bucket.tokens -= cost
		return RateLimitDecision{Allowed: true}, nil
	}

	deficit := cost - bucket.tokens
	retryAfterSeconds := int(math.Ceil(deficit / refillRate))
	if retryAfterSeconds < 1 {
		retryAfterSeconds = 1
	}

	return RateLimitDecision{
		Allowed:    false,
		RetryAfter: time.Duration(retryAfterSeconds) * time.Second,
	}, nil
}

// AllowConnect charges cost=1 against the same token bucket used for REST
// requests. This enforces the handshake rate limit before the WebSocket upgrade.
func (l *RequestRateLimiter) AllowConnect(principal *auth.Principal) (RateLimitDecision, error) {
	if l == nil || !l.cfg.Enabled || principal == nil {
		return RateLimitDecision{Allowed: true}, nil
	}

	now := l.now()
	key := principalBucketKey(principal)
	refillRate := float64(l.cfg.TokensPerSecond)
	burst := float64(l.cfg.Burst)

	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, ok := l.buckets[key]
	if !ok {
		bucket = &tokenBucket{
			tokens:     burst,
			lastRefill: now,
		}
		l.buckets[key] = bucket
	}

	elapsed := now.Sub(bucket.lastRefill).Seconds()
	if elapsed > 0 {
		bucket.tokens = math.Min(burst, bucket.tokens+(elapsed*refillRate))
		bucket.lastRefill = now
	}

	const cost = 1.0
	if bucket.tokens >= cost {
		bucket.tokens -= cost
		return RateLimitDecision{Allowed: true}, nil
	}

	deficit := cost - bucket.tokens
	retryAfterSeconds := int(math.Ceil(deficit / refillRate))
	if retryAfterSeconds < 1 {
		retryAfterSeconds = 1
	}

	return RateLimitDecision{
		Allowed:    false,
		RetryAfter: time.Duration(retryAfterSeconds) * time.Second,
	}, nil
}

// ConnectionCounter tracks the number of open WebSocket connections per API key
// and enforces a per-key concurrency cap.
type ConnectionCounter struct {
	mu      sync.Mutex
	counts  map[int64]int
	maxPerKey int
}

// NewConnectionCounter returns a ConnectionCounter capped at max concurrent
// sockets per API key.
func NewConnectionCounter(max int) *ConnectionCounter {
	return &ConnectionCounter{
		counts:    make(map[int64]int),
		maxPerKey: max,
	}
}

// Acquire increments the connection count for keyID and returns true if the
// connection is allowed. Returns false if the cap would be exceeded.
func (c *ConnectionCounter) Acquire(keyID int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts[keyID] >= c.maxPerKey {
		return false
	}
	c.counts[keyID]++
	return true
}

// Release decrements the connection count for keyID.
func (c *ConnectionCounter) Release(keyID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts[keyID] > 0 {
		c.counts[keyID]--
	}
}

func principalBucketKey(principal *auth.Principal) string {
	if principal.KeyID > 0 {
		return fmt.Sprintf("key:%d", principal.KeyID)
	}
	return "prefix:" + principal.Prefix
}

func (l *RequestRateLimiter) requestCost(r *http.Request) (float64, error) {
	if wantsEpisodes(r) {
		return float64(l.cfg.EpisodesCost), nil
	}

	if r.Method == http.MethodPost && r.URL.Path == "/v1/ratings/bulk" {
		count, err := readBulkIdentifierCount(r)
		if err != nil {
			return 0, err
		}
		if count <= 0 {
			return 1, nil
		}

		cost := int(math.Ceil(float64(count) / float64(l.cfg.BulkDivisor)))
		if cost < 1 {
			cost = 1
		}
		return float64(cost), nil
	}

	return 1, nil
}

func readBulkIdentifierCount(r *http.Request) (int, error) {
	if r.Body == nil {
		return 0, nil
	}

	payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	r.Body = io.NopCloser(bytes.NewReader(payload))

	var body struct {
		Identifiers []string `json:"identifiers"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return 0, nil
	}

	return len(body.Identifiers), nil
}

func writeRateLimited(w http.ResponseWriter, decision RateLimitDecision) {
	if decision.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(decision.RetryAfter/time.Second)))
	}
	writeError(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
}
