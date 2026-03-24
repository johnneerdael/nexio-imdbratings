package worker

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"nexio-imdb/apps/api/internal/config"
	"nexio-imdb/apps/api/internal/ingest"
)

type Service struct {
	ingester *ingest.Runner
	cfg      config.Config
	logger   *log.Logger
}

func New(pool *pgxpool.Pool, cfg config.Config, logger *log.Logger) *Service {
	if logger == nil {
		logger = log.Default()
	}
	return &Service{
		ingester: ingest.NewRunner(pool, &http.Client{
			Timeout: cfg.HTTPTimeout,
		}, cfg.IMDbDatasetBaseURL, logger, cfg.IMDbForceFullRefresh, cfg.IMDbDeltaBatchSize, cfg.IMDbMaintenanceWorkMem),
		cfg:    cfg,
		logger: logger,
	}
}

func (s *Service) Run(ctx context.Context) error {
	s.runSyncLoop(ctx)
	return nil
}

func (s *Service) runSyncLoop(ctx context.Context) {
	if s.cfg.IMDbRunOnStartup {
		s.executeSync(ctx, "initial")
	}

	ticker := time.NewTicker(s.cfg.IMDbSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.executeSync(ctx, "scheduled")
		}
	}
}

func (s *Service) executeSync(ctx context.Context, label string) {
	result, err := s.ingester.SyncOnce(ctx)
	if err != nil {
		s.logger.Printf("%s imdb sync failed: %v", label, err)
		return
	}
	if result.Imported {
		s.logger.Printf("%s imdb sync imported snapshot %d (%s)", label, result.SnapshotID, result.DatasetVersion)
		return
	}
	s.logger.Printf("%s imdb sync skipped: upstream unchanged", label)
}
