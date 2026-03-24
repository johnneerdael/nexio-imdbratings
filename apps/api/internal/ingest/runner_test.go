package ingest

import (
	"bytes"
	"io"
	"log"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTransformTSVToCopyCSVSkipsHeaderAndEscapesQuotes(t *testing.T) {
	t.Parallel()

	input := strings.NewReader(strings.Join([]string{
		"tconst\ttitleType\tprimaryTitle\toriginalTitle\tisAdult\tstartYear\tendYear\truntimeMinutes\tgenres",
		`tt0073045	movie	"Giliap"	"Giliap"	0	1975	\N	137	Crime,Drama`,
		"tt0000001\tshort\tCarmencita\tCarmencita\t0\t1894\t\\N\t1\tDocumentary,Short",
	}, "\n") + "\n")

	var out bytes.Buffer
	if err := transformTSVToCopyCSV(input, &out, 9); err != nil {
		t.Fatalf("transformTSVToCopyCSV returned error: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "tconst\ttitleType") {
		t.Fatalf("expected header row to be removed, got %q", got)
	}
	if !strings.Contains(got, "\"\"\"Giliap\"\"\"") {
		t.Fatalf("expected quoted title to be CSV-escaped, got %q", got)
	}
	if !strings.Contains(got, "tt0000001\tshort\tCarmencita") {
		t.Fatalf("expected second record to be preserved, got %q", got)
	}
}

func TestDownloadProgressReaderLogsProgressAndCompletion(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	reader := &downloadProgressReader{
		reader:       strings.NewReader("abcdef"),
		logger:       log.New(&logs, "", 0),
		datasetName:  "title.ratings.tsv.gz",
		contentBytes: 6,
		logEvery:     3,
		startedAt:    time.Unix(0, 0),
	}

	buf := make([]byte, 2)
	for {
		_, err := reader.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read returned error: %v", err)
		}
	}

	got := logs.String()
	if !strings.Contains(got, "download progress title.ratings.tsv.gz: 4/6 bytes") {
		t.Fatalf("expected progress log, got %q", got)
	}
	if !strings.Contains(got, "download complete title.ratings.tsv.gz: 6/6 bytes") {
		t.Fatalf("expected completion log, got %q", got)
	}
}

func TestNewRunnerRegistersRatingsDatasetsOnly(t *testing.T) {
	t.Parallel()

	runner := NewRunner(nil, nil, "https://example.com/", nil, false, 0, "")

	if got, want := len(runner.datasets), 2; got != want {
		t.Fatalf("expected %d datasets, got %d", want, got)
	}

	names := []string{runner.datasets[0].Name, runner.datasets[1].Name}
	expected := []string{"title.ratings.tsv.gz", "title.episode.tsv.gz"}
	if !slices.Equal(names, expected) {
		t.Fatalf("expected datasets %v, got %v", expected, names)
	}
}

func TestCreateRawTableStatementUsesUnloggedTables(t *testing.T) {
	t.Parallel()

	statement := createRawTableStatement("staging_title_basics_raw_7", "(tconst TEXT)")
	if !strings.Contains(statement, "CREATE UNLOGGED TABLE staging_title_basics_raw_7") {
		t.Fatalf("expected unlogged raw table creation, got %q", statement)
	}
}

func TestSelectSyncMode(t *testing.T) {
	t.Parallel()

	if got := selectSyncMode(false, false); got != syncModeFullRefresh {
		t.Fatalf("expected initial sync to use full refresh, got %q", got)
	}
	if got := selectSyncMode(true, false); got != syncModeDelta {
		t.Fatalf("expected recurring sync to use delta, got %q", got)
	}
	if got := selectSyncMode(true, true); got != syncModeFullRefresh {
		t.Fatalf("expected force flag to select full refresh, got %q", got)
	}
}

func TestIndexBuildPlanIsRatingsOnly(t *testing.T) {
	t.Parallel()

	base, deferred := buildIndexPlans(tableSet{
		TitleRatings:  "title_ratings_shadow_7",
		TitleEpisodes: "title_episodes_shadow_7",
	})

	baseNames := make([]string, 0, len(base))
	for _, item := range base {
		baseNames = append(baseNames, item.name)
	}
	expectedBase := []string{"index ratings votes", "index episodes parent"}
	if !slices.Equal(baseNames, expectedBase) {
		t.Fatalf("expected base indexes %v, got %v", expectedBase, baseNames)
	}

	deferredNames := make([]string, 0, len(deferred))
	for _, item := range deferred {
		deferredNames = append(deferredNames, item.name)
	}
	if len(deferredNames) != 0 {
		t.Fatalf("expected no deferred indexes, got %v", deferredNames)
	}
}

func TestTableSetAllReturnsRatingsTablesOnly(t *testing.T) {
	t.Parallel()

	if got, want := liveTables().all(), []string{"title_ratings", "title_episodes"}; !slices.Equal(got, want) {
		t.Fatalf("expected live tables %v, got %v", want, got)
	}

	tables := shadowTables(7)
	if got, want := tables.all(), []string{"title_ratings_shadow_7", "title_episodes_shadow_7"}; !slices.Equal(got, want) {
		t.Fatalf("expected shadow tables %v, got %v", want, got)
	}

	if got, want := previousTables().all(), []string{"title_ratings_previous", "title_episodes_previous"}; !slices.Equal(got, want) {
		t.Fatalf("expected previous tables %v, got %v", want, got)
	}
}
