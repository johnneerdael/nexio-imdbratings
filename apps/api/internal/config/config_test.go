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
