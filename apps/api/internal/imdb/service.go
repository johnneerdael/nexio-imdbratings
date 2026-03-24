package imdb

import (
	"context"
	"errors"
)

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
	result := RatingWithEpisodes{
		RequestTconst: tconst,
		Episodes:      []EpisodeRating{},
	}

	rating, err := s.repo.GetRating(ctx, tconst)
	switch {
	case err == nil:
		result.Rating = &rating
	case !errors.Is(err, ErrNotFound):
		return RatingWithEpisodes{}, err
	}

	parentTconst, isEpisode, err := s.repo.GetEpisodeParentTconst(ctx, tconst)
	if err != nil {
		return RatingWithEpisodes{}, err
	}

	if isEpisode {
		result.EpisodesParentTconst = parentTconst
		result.Episodes, err = s.listEpisodeRatings(ctx, parentTconst)
		if err != nil {
			return RatingWithEpisodes{}, err
		}
		return result, nil
	}

	hasEpisodesParent, err := s.repo.HasEpisodesParent(ctx, tconst)
	if err != nil {
		return RatingWithEpisodes{}, err
	}
	if hasEpisodesParent {
		result.EpisodesParentTconst = tconst
		result.Episodes, err = s.listEpisodeRatings(ctx, tconst)
		if err != nil {
			return RatingWithEpisodes{}, err
		}
		return result, nil
	}

	if result.Rating != nil {
		return result, nil
	}

	return RatingWithEpisodes{}, ErrNotFound
}

func (s *Service) listEpisodeRatings(ctx context.Context, parentTconst string) ([]EpisodeRating, error) {
	items, err := s.repo.ListEpisodeRatings(ctx, parentTconst)
	if err != nil {
		return nil, err
	}
	if items == nil {
		return []EpisodeRating{}, nil
	}
	return items, nil
}
