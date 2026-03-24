package imdb

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("resource not found")
	ErrInvalidRequest = errors.New("invalid request")
)

type QueryService interface {
	Ready(ctx context.Context) error
	ListSnapshots(ctx context.Context) ([]Snapshot, error)
	GetStats(ctx context.Context) (Stats, error)
	GetRating(ctx context.Context, tconst string) (Rating, error)
	GetRatingWithEpisodes(ctx context.Context, tconst string) (RatingWithEpisodes, error)
}

type Repository interface {
	Ping(ctx context.Context) error
	ListSnapshots(ctx context.Context) ([]Snapshot, error)
	GetStats(ctx context.Context) (Stats, error)
	GetRating(ctx context.Context, tconst string) (Rating, error)
	GetEpisodeParentTconst(ctx context.Context, tconst string) (string, bool, error)
	HasEpisodesParent(ctx context.Context, tconst string) (bool, error)
	ListEpisodeRatings(ctx context.Context, parentTconst string) ([]EpisodeRating, error)
}

type Snapshot struct {
	ID              int64      `json:"id"`
	Dataset         string     `json:"dataset"`
	Status          string     `json:"status"`
	SyncMode        string     `json:"syncMode,omitempty"`
	DatasetVersion  string     `json:"datasetVersion,omitempty"`
	ImportedAt      time.Time  `json:"importedAt"`
	SourceUpdatedAt *time.Time `json:"sourceUpdatedAt,omitempty"`
	SourceETag      string     `json:"sourceETag,omitempty"`
	IsActive        bool       `json:"isActive"`
	RatingCount     int64      `json:"ratingCount"`
	EpisodeCount    int64      `json:"episodeCount"`
	Notes           string     `json:"notes,omitempty"`
	SourceURL       string     `json:"sourceUrl,omitempty"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
}

type Stats struct {
	Ratings   int64 `json:"ratings"`
	Episodes  int64 `json:"episodes"`
	Snapshots int64 `json:"snapshots"`
}

type Rating struct {
	Tconst        string  `json:"tconst"`
	AverageRating float64 `json:"averageRating"`
	NumVotes      int     `json:"numVotes"`
}

type EpisodeRating struct {
	Tconst        string  `json:"tconst"`
	ParentTconst  string  `json:"parentTconst"`
	SeasonNumber  *int    `json:"seasonNumber,omitempty"`
	EpisodeNumber *int    `json:"episodeNumber,omitempty"`
	AverageRating float64 `json:"averageRating"`
	NumVotes      int     `json:"numVotes"`
}

type RatingWithEpisodes struct {
	RequestTconst        string          `json:"requestTconst"`
	Rating               *Rating         `json:"rating,omitempty"`
	EpisodesParentTconst string          `json:"episodesParentTconst,omitempty"`
	Episodes             []EpisodeRating `json:"episodes"`
}
