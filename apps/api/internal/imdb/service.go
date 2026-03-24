package imdb

import "context"

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Ready(ctx context.Context) error {
	return s.repo.Ping(ctx)
}

func (s *Service) ListSnapshots(ctx context.Context) ([]Snapshot, error) {
	return s.repo.ListSnapshots(ctx)
}

func (s *Service) GetStats(ctx context.Context) (Stats, error) {
	return s.repo.GetStats(ctx)
}

func (s *Service) GetRating(ctx context.Context, tconst string) (Rating, error) {
	return s.repo.GetRating(ctx, tconst)
}

func (s *Service) GetRatingWithEpisodes(ctx context.Context, tconst string) (RatingWithEpisodes, error) {
	return s.repo.GetRatingWithEpisodes(ctx, tconst)
}
