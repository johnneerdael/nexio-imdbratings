package config

import (
	"testing"
	"time"
)

func TestLoadFallsBackForNonPositiveSyncInterval(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("API_KEY_PEPPER", "pepper")
	t.Setenv("IMDB_SYNC_INTERVAL_HOURS", "0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.IMDbSyncInterval != 12*time.Hour {
		t.Fatalf("expected fallback sync interval, got %v", cfg.IMDbSyncInterval)
	}
}

func TestLoadFallsBackForNonPositiveTimeout(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("API_KEY_PEPPER", "pepper")
	t.Setenv("HTTP_TIMEOUT_MINUTES", "-5")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.HTTPTimeout != 30*time.Minute {
		t.Fatalf("expected fallback HTTP timeout, got %v", cfg.HTTPTimeout)
	}
}

func TestLoadRateLimitDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("API_KEY_PEPPER", "pepper")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if !cfg.RateLimitEnabled {
		t.Fatalf("expected rate limiting to be enabled by default")
	}
	if cfg.RateLimitTokensPerSecond != 10 {
		t.Fatalf("expected default tokens/sec 10, got %d", cfg.RateLimitTokensPerSecond)
	}
	if cfg.RateLimitBurst != 40 {
		t.Fatalf("expected default burst 40, got %d", cfg.RateLimitBurst)
	}
	if cfg.RateLimitEpisodesCost != 8 {
		t.Fatalf("expected default episodes cost 8, got %d", cfg.RateLimitEpisodesCost)
	}
	if cfg.RateLimitBulkDivisor != 25 {
		t.Fatalf("expected default bulk divisor 25, got %d", cfg.RateLimitBulkDivisor)
	}
}

func TestLoadFallsBackForNonPositiveRateLimitValues(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("API_KEY_PEPPER", "pepper")
	t.Setenv("RATE_LIMIT_TOKENS_PER_SECOND", "0")
	t.Setenv("RATE_LIMIT_BURST", "-1")
	t.Setenv("RATE_LIMIT_EPISODES_COST", "0")
	t.Setenv("RATE_LIMIT_BULK_DIVISOR", "-5")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.RateLimitTokensPerSecond != 10 {
		t.Fatalf("expected fallback tokens/sec 10, got %d", cfg.RateLimitTokensPerSecond)
	}
	if cfg.RateLimitBurst != 40 {
		t.Fatalf("expected fallback burst 40, got %d", cfg.RateLimitBurst)
	}
	if cfg.RateLimitEpisodesCost != 8 {
		t.Fatalf("expected fallback episodes cost 8, got %d", cfg.RateLimitEpisodesCost)
	}
	if cfg.RateLimitBulkDivisor != 25 {
		t.Fatalf("expected fallback bulk divisor 25, got %d", cfg.RateLimitBulkDivisor)
	}
}
