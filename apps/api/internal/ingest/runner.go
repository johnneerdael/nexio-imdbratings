package ingest

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Runner struct {
	pool               *pgxpool.Pool
	client             *http.Client
	baseURL            string
	datasets           []datasetSpec
	logger             *log.Logger
	forceFullRefresh   bool
	deltaBatchSize     int
	maintenanceWorkMem string
}

type Result struct {
	Imported       bool
	SnapshotID     int64
	DatasetVersion string
}

type datasetSpec struct {
	Name        string
	Filename    string
	BaseTable   string
	Columns     int
	ColumnDefs  string
	CopyColumns string
}

type remoteDataset struct {
	spec            datasetSpec
	url             string
	etag            string
	lastModified    string
	sourceUpdatedAt *time.Time
}

type tableSet struct {
	TitleRatings  string
	TitleEpisodes string
}

type normalizeCounts struct {
	Ratings  int64
	Episodes int64
}

type snapshotCounts struct {
	Ratings  int64
	Episodes int64
}

type syncMode string

const (
	syncModeFullRefresh syncMode = "full_refresh"
)

type indexStatement struct {
	name      string
	statement string
}

type ActiveSnapshotState struct {
	ID     int64
	Exists bool
	Counts snapshotCounts
}

func liveTables() tableSet {
	return tableSet{
		TitleRatings:  "title_ratings",
		TitleEpisodes: "title_episodes",
	}
}

func shadowTables(snapshotID int64) tableSet {
	suffix := fmt.Sprintf("_shadow_%d", snapshotID)
	return tableSet{
		TitleRatings:  "title_ratings" + suffix,
		TitleEpisodes: "title_episodes" + suffix,
	}
}

func previousTables() tableSet {
	return tableSet{
		TitleRatings:  "title_ratings_previous",
		TitleEpisodes: "title_episodes_previous",
	}
}

func (t tableSet) all() []string {
	return []string{t.TitleRatings, t.TitleEpisodes}
}

func NewRunner(pool *pgxpool.Pool, client *http.Client, baseURL string, logger *log.Logger, forceFullRefresh bool, deltaBatchSize int, maintenanceWorkMem string) *Runner {
	baseURL = strings.TrimRight(baseURL, "/")
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	if deltaBatchSize <= 0 {
		deltaBatchSize = 50000
	}
	if strings.TrimSpace(maintenanceWorkMem) == "" {
		maintenanceWorkMem = "1GB"
	}
	return &Runner{
		pool:               pool,
		client:             client,
		baseURL:            baseURL,
		logger:             logger,
		forceFullRefresh:   forceFullRefresh,
		deltaBatchSize:     deltaBatchSize,
		maintenanceWorkMem: maintenanceWorkMem,
		datasets: []datasetSpec{
			{
				Name:        "title.ratings.tsv.gz",
				Filename:    "title.ratings.tsv.gz",
				BaseTable:   "staging_title_ratings_raw",
				Columns:     3,
				ColumnDefs:  "(tconst TEXT, average_rating TEXT, num_votes TEXT)",
				CopyColumns: "(tconst, average_rating, num_votes)",
			},
			{
				Name:        "title.episode.tsv.gz",
				Filename:    "title.episode.tsv.gz",
				BaseTable:   "staging_title_episode_raw",
				Columns:     4,
				ColumnDefs:  "(tconst TEXT, parent_tconst TEXT, season_number TEXT, episode_number TEXT)",
				CopyColumns: "(tconst, parent_tconst, season_number, episode_number)",
			},
		},
	}
}

func createRawTableStatement(tableName, columnDefs string) string {
	return fmt.Sprintf("CREATE UNLOGGED TABLE %s %s", tableName, columnDefs)
}

func createCopyStatement(tableName, copyColumns string) string {
	return fmt.Sprintf(`COPY %s %s FROM STDIN WITH (FORMAT csv, DELIMITER E'\t', NULL '\N')`, tableName, copyColumns)
}

func rawTableName(snapshotID int64, spec datasetSpec) string {
	return fmt.Sprintf("%s_%d", spec.BaseTable, snapshotID)
}

func selectSyncMode(hasActiveSnapshot, forceFullRefresh bool) syncMode {
	return syncModeFullRefresh
}

func (r *Runner) logf(format string, args ...any) {
	if r.logger == nil {
		return
	}
	r.logger.Printf(format, args...)
}

func (r *Runner) datasetByName(name string) datasetSpec {
	for _, item := range r.datasets {
		if item.Name == name {
			return item
		}
	}
	return datasetSpec{}
}

func (r *Runner) SyncOnce(ctx context.Context) (Result, error) {
	r.logf("imdb sync checking upstream metadata for %d datasets", len(r.datasets))
	remote, err := r.fetchRemoteMetadata(ctx)
	if err != nil {
		return Result{}, err
	}

	changed, err := r.changedDatasets(ctx, remote)
	if err != nil {
		return Result{}, err
	}
	if len(changed) == 0 {
		if err := r.upsertSyncState(ctx, remote, nil); err != nil {
			return Result{}, err
		}
		r.logf("imdb sync metadata unchanged for all datasets")
		return Result{Imported: false}, nil
	}

	active, err := r.activeSnapshotState(ctx)
	if err != nil {
		return Result{}, err
	}
	mode := selectSyncMode(active.Exists, r.forceFullRefresh)

	sourceUpdatedAt := latestSourceUpdatedAt(remote)
	datasetVersion := datasetVersion(sourceUpdatedAt)
	r.logf("imdb sync changes detected, preparing snapshot for dataset version %s using %s", datasetVersion, mode)
	snapshotID, err := r.createSnapshot(ctx, mode, active.Counts, sourceUpdatedAt, remote, datasetVersion)
	if err != nil {
		return Result{}, err
	}

	if err := r.importSnapshot(ctx, snapshotID, mode, changed, remote, active.Counts, sourceUpdatedAt, datasetVersion); err != nil {
		_ = r.markSnapshotFailed(ctx, snapshotID, err)
		return Result{}, err
	}

	return Result{
		Imported:       true,
		SnapshotID:     snapshotID,
		DatasetVersion: datasetVersion,
	}, nil
}

func (r *Runner) fetchRemoteMetadata(ctx context.Context) ([]remoteDataset, error) {
	items := make([]remoteDataset, 0, len(r.datasets))
	for _, spec := range r.datasets {
		url := r.baseURL + "/" + spec.Filename
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
		if err != nil {
			return nil, fmt.Errorf("build HEAD request for %s: %w", spec.Name, err)
		}
		resp, err := r.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch metadata for %s: %w", spec.Name, err)
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("metadata request for %s returned %s", spec.Name, resp.Status)
		}

		var parsed *time.Time
		if raw := strings.TrimSpace(resp.Header.Get("Last-Modified")); raw != "" {
			if ts, err := http.ParseTime(raw); err == nil {
				value := ts.UTC()
				parsed = &value
			}
		}

		items = append(items, remoteDataset{
			spec:            spec,
			url:             url,
			etag:            strings.TrimSpace(resp.Header.Get("ETag")),
			lastModified:    strings.TrimSpace(resp.Header.Get("Last-Modified")),
			sourceUpdatedAt: parsed,
		})
	}
	return items, nil
}

func (r *Runner) changedDatasets(ctx context.Context, remote []remoteDataset) ([]remoteDataset, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT dataset_name, COALESCE(etag, ''), COALESCE(last_modified, '')
		FROM dataset_sync_state
	`)
	if err != nil {
		return nil, fmt.Errorf("query dataset sync state: %w", err)
	}
	defer rows.Close()

	existing := map[string][2]string{}
	for rows.Next() {
		var name, etag, lastModified string
		if err := rows.Scan(&name, &etag, &lastModified); err != nil {
			return nil, fmt.Errorf("scan dataset sync state: %w", err)
		}
		existing[name] = [2]string{etag, lastModified}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dataset sync state: %w", err)
	}

	changed := make([]remoteDataset, 0, len(remote))
	for _, item := range remote {
		previous, ok := existing[item.spec.Name]
		if !ok {
			changed = append(changed, item)
			continue
		}
		if datasetRequiresRefresh(previous[0], previous[1], item.etag, item.lastModified) {
			changed = append(changed, item)
		}
	}
	return changed, nil
}

func datasetRequiresRefresh(previousETag, previousLastModified, currentETag, currentLastModified string) bool {
	previousHasValidator := strings.TrimSpace(previousETag) != "" || strings.TrimSpace(previousLastModified) != ""
	currentHasValidator := strings.TrimSpace(currentETag) != "" || strings.TrimSpace(currentLastModified) != ""

	if !previousHasValidator || !currentHasValidator {
		return true
	}
	return previousETag != currentETag || previousLastModified != currentLastModified
}

func (r *Runner) activeSnapshotState(ctx context.Context) (ActiveSnapshotState, error) {
	var state ActiveSnapshotState
	err := r.pool.QueryRow(ctx, `
		SELECT
			id,
			rating_count,
			episode_count
		FROM imdb_snapshots
		WHERE is_active = TRUE
		ORDER BY imported_at DESC, id DESC
		LIMIT 1
	`).Scan(
		&state.ID,
		&state.Counts.Ratings,
		&state.Counts.Episodes,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ActiveSnapshotState{}, nil
		}
		return ActiveSnapshotState{}, fmt.Errorf("load active snapshot state: %w", err)
	}
	state.Exists = true
	return state, nil
}

func (r *Runner) createSnapshot(ctx context.Context, mode syncMode, baseline snapshotCounts, sourceUpdatedAt *time.Time, remote []remoteDataset, datasetVersion string) (int64, error) {
	sourceURL := r.baseURL + "/"
	sourceETag := joinRemoteValues(remote, func(item remoteDataset) string { return item.etag })

	var snapshotID int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO imdb_snapshots (
			dataset_name,
			status,
			dataset_version,
			source_url,
			source_updated_at,
			source_etag,
			sync_mode,
			notes,
			imported_at,
			is_active,
			rating_count,
			episode_count
		)
		VALUES ('imdbws', 'importing', $1, $2, $3, $4, $5, '', NOW(), FALSE, $6, $7)
		RETURNING id
	`, datasetVersion, sourceURL, sourceUpdatedAt, sourceETag, mode, baseline.Ratings, baseline.Episodes).Scan(&snapshotID)
	if err != nil {
		return 0, fmt.Errorf("create snapshot row: %w", err)
	}

	r.logf("imdb sync created snapshot %d for version %s", snapshotID, datasetVersion)
	return snapshotID, nil
}

func (r *Runner) importSnapshot(ctx context.Context, snapshotID int64, mode syncMode, changed []remoteDataset, remote []remoteDataset, baseline snapshotCounts, sourceUpdatedAt *time.Time, datasetVersion string) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin import tx: %w", err)
	}
	defer tx.Rollback(ctx)

	shadow := shadowTables(snapshotID)
	r.logf("imdb sync snapshot %d import started", snapshotID)
	for _, item := range remote {
		stageTable := rawTableName(snapshotID, item.spec)
		r.logf("imdb sync preparing staging table %s for %s", stageTable, item.spec.Name)
		if _, err := tx.Exec(ctx, createRawTableStatement(stageTable, item.spec.ColumnDefs)); err != nil {
			return fmt.Errorf("create raw table for %s: %w", item.spec.Name, err)
		}
		if err := r.copyDataset(ctx, tx, item, stageTable); err != nil {
			return err
		}
	}

	if err := r.setLocalMaintenanceWorkMem(ctx, tx); err != nil {
		return err
	}
	if err := r.createShadowTables(ctx, tx, shadow); err != nil {
		return err
	}

	counts, err := r.normalizeSnapshot(ctx, tx, shadow, snapshotID)
	if err != nil {
		return err
	}

	rawTables := make([]string, 0, len(remote))
	for _, item := range remote {
		rawTables = append(rawTables, rawTableName(snapshotID, item.spec))
	}
	if err := r.dropTables(ctx, tx, rawTables...); err != nil {
		return err
	}

	if err := r.createSecondaryIndexes(ctx, tx, shadow); err != nil {
		return err
	}
	if err := r.promoteShadowTables(ctx, tx, snapshotID, shadow, counts, sourceUpdatedAt, datasetVersion, remote); err != nil {
		return err
	}
	r.logf("imdb sync snapshot %d sync state updated", snapshotID)

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit import tx: %w", err)
	}
	r.logf("imdb sync snapshot %d committed to live tables", snapshotID)
	if err := r.dropPreviousTables(ctx); err != nil {
		r.logf("imdb sync snapshot %d previous table cleanup warning: %v", snapshotID, err)
	}
	if err := r.analyzeTables(ctx, liveTables().all()...); err != nil {
		return err
	}
	return nil
}

func (r *Runner) copyDataset(ctx context.Context, tx pgx.Tx, item remoteDataset, targetTable string) error {
	r.logf("imdb sync download started for %s", item.spec.Name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.url, nil)
	if err != nil {
		return fmt.Errorf("build GET request for %s: %w", item.spec.Name, err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", item.spec.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download %s returned %s", item.spec.Name, resp.Status)
	}

	progressReader := newDownloadProgressReader(resp.Body, r.logger, item.spec.Name, resp.ContentLength)
	reader, err := gzip.NewReader(progressReader)
	if err != nil {
		return fmt.Errorf("open gzip for %s: %w", item.spec.Name, err)
	}
	defer reader.Close()

	copyReader, err := transformTSVForCopy(reader, item.spec.Columns)
	if err != nil {
		return fmt.Errorf("prepare %s for copy: %w", item.spec.Name, err)
	}

	started := time.Now()
	tag, err := tx.Conn().PgConn().CopyFrom(ctx, copyReader, createCopyStatement(targetTable, item.spec.CopyColumns))
	if err != nil {
		return fmt.Errorf("copy %s into %s: %w", item.spec.Name, targetTable, err)
	}
	r.logf("imdb sync copied %s into %s rows=%d duration=%s", item.spec.Name, targetTable, tag.RowsAffected(), time.Since(started).Round(time.Second))
	return nil
}

func transformTSVForCopy(input io.Reader, expectedColumns int) (io.Reader, error) {
	pipeReader, pipeWriter := io.Pipe()
	go func() {
		defer pipeWriter.Close()
		if err := transformTSVToCopyCSV(input, pipeWriter, expectedColumns); err != nil {
			_ = pipeWriter.CloseWithError(err)
		}
	}()
	return pipeReader, nil
}

func transformTSVToCopyCSV(input io.Reader, output io.Writer, expectedColumns int) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)

	writer := csv.NewWriter(output)
	writer.Comma = '\t'

	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		if lineNumber == 1 {
			continue
		}

		record := strings.Split(scanner.Text(), "\t")
		if len(record) != expectedColumns {
			return fmt.Errorf("line %d has %d columns, expected %d", lineNumber, len(record), expectedColumns)
		}
		if err := writer.Write(record); err != nil {
			return fmt.Errorf("write transformed record on line %d: %w", lineNumber, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan tsv input: %w", err)
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush transformed csv: %w", err)
	}
	return nil
}

type downloadProgressReader struct {
	reader       io.Reader
	logger       *log.Logger
	datasetName  string
	contentBytes int64
	downloaded   int64
	logEvery     int64
	nextLog      int64
	startedAt    time.Time
	completed    bool
}

func newDownloadProgressReader(reader io.Reader, logger *log.Logger, datasetName string, contentBytes int64) io.Reader {
	logEvery := int64(64 << 20)
	return &downloadProgressReader{
		reader:       reader,
		logger:       logger,
		datasetName:  datasetName,
		contentBytes: contentBytes,
		logEvery:     logEvery,
		nextLog:      logEvery,
		startedAt:    time.Now(),
	}
}

func (r *downloadProgressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.downloaded += int64(n)
		for r.logEvery > 0 && r.downloaded >= r.nextLog {
			r.logProgress("download progress")
			r.nextLog += r.logEvery
		}
	}
	if err == io.EOF && !r.completed {
		r.completed = true
		r.logProgress("download complete")
	}
	return n, err
}

func (r *downloadProgressReader) logProgress(prefix string) {
	if r.logger == nil {
		return
	}
	duration := time.Since(r.startedAt).Round(time.Second)
	if r.contentBytes > 0 {
		r.logger.Printf("%s %s: %d/%d bytes duration=%s", prefix, r.datasetName, r.downloaded, r.contentBytes, duration)
		return
	}
	r.logger.Printf("%s %s: %d bytes duration=%s", prefix, r.datasetName, r.downloaded, duration)
}

func (r *Runner) createShadowTables(ctx context.Context, tx pgx.Tx, tables tableSet) error {
	statements := []struct {
		name      string
		statement string
	}{
		{
			name: "create shadow ratings",
			statement: fmt.Sprintf(`
				CREATE UNLOGGED TABLE %s (
					tconst TEXT PRIMARY KEY,
					average_rating NUMERIC(3,1) NOT NULL,
					num_votes INTEGER NOT NULL DEFAULT 0,
					row_hash TEXT NOT NULL,
					updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
				)
			`, tables.TitleRatings),
		},
		{
			name: "create shadow episodes",
			statement: fmt.Sprintf(`
				CREATE UNLOGGED TABLE %s (
					tconst TEXT PRIMARY KEY,
					parent_tconst TEXT NOT NULL,
					season_number INTEGER,
					episode_number INTEGER,
					row_hash TEXT NOT NULL,
					created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
				)
			`, tables.TitleEpisodes),
		},
	}

	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement.statement); err != nil {
			return fmt.Errorf("%s: %w", statement.name, err)
		}
		r.logf("imdb sync %s ready", statement.name)
	}
	return nil
}

func (r *Runner) createSecondaryIndexes(ctx context.Context, tx pgx.Tx, tables tableSet) error {
	for _, statement := range buildIndexPlans(tables) {
		started := time.Now()
		if _, err := tx.Exec(ctx, statement.statement); err != nil {
			return fmt.Errorf("%s: %w", statement.name, err)
		}
		r.logf("imdb sync step complete: %s duration=%s", statement.name, time.Since(started).Round(time.Second))
	}
	return nil
}

func buildIndexPlans(tables tableSet) []indexStatement {
	return []indexStatement{
		{name: "index ratings votes", statement: fmt.Sprintf(`CREATE INDEX %s ON %s(num_votes DESC)`, tables.TitleRatings+"_num_votes_idx", tables.TitleRatings)},
		{name: "index episodes parent", statement: fmt.Sprintf(`CREATE INDEX %s ON %s(parent_tconst, season_number, episode_number)`, tables.TitleEpisodes+"_parent_idx", tables.TitleEpisodes)},
	}
}

func (r *Runner) promoteShadowTables(ctx context.Context, tx pgx.Tx, snapshotID int64, shadow tableSet, counts normalizeCounts, sourceUpdatedAt *time.Time, datasetVersion string, remote []remoteDataset) error {
	live := liveTables()
	previous := previousTables()

	promotionSteps := []struct {
		name      string
		statement string
		args      []any
	}{
		{name: "drop previous ratings backup", statement: fmt.Sprintf(`DROP TABLE IF EXISTS %s, %s`, previous.TitleRatings, previous.TitleEpisodes)},
		{name: "rename live ratings to backup", statement: fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, live.TitleRatings, previous.TitleRatings)},
		{name: "rename live episodes to backup", statement: fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, live.TitleEpisodes, previous.TitleEpisodes)},
		{name: "promote ratings", statement: fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, shadow.TitleRatings, live.TitleRatings)},
		{name: "promote episodes", statement: fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, shadow.TitleEpisodes, live.TitleEpisodes)},
		{name: "deactivate previous snapshots", statement: `UPDATE imdb_snapshots SET is_active = FALSE WHERE id <> $1`, args: []any{snapshotID}},
		{name: "finalize snapshot", statement: `
			UPDATE imdb_snapshots
			SET
				status = 'ready',
				dataset_version = $2,
				source_updated_at = $3,
				source_etag = $4,
				completed_at = NOW(),
				duration_seconds = GREATEST(EXTRACT(EPOCH FROM (NOW() - imported_at))::INTEGER, 0),
				is_active = TRUE,
				rating_count = $5,
				episode_count = $6,
				notes = ''
			WHERE id = $1
		`, args: []any{snapshotID, datasetVersion, sourceUpdatedAt, joinRemoteValues(remote, func(item remoteDataset) string { return item.etag }), counts.Ratings, counts.Episodes}},
	}

	for _, step := range promotionSteps {
		started := time.Now()
		if _, err := tx.Exec(ctx, step.statement, step.args...); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
		r.logf("imdb sync snapshot %d promotion step complete: %s duration=%s", snapshotID, step.name, time.Since(started).Round(time.Second))
	}

	if err := upsertSyncStateWithExecutor(ctx, tx, remote, &snapshotID); err != nil {
		return err
	}
	return nil
}

func (r *Runner) dropPreviousTables(ctx context.Context) error {
	previous := previousTables()
	_, err := r.pool.Exec(ctx, fmt.Sprintf(`
		DROP TABLE IF EXISTS %s, %s
	`, previous.TitleRatings, previous.TitleEpisodes))
	if err != nil {
		return fmt.Errorf("drop previous tables: %w", err)
	}
	return nil
}

func (r *Runner) setLocalMaintenanceWorkMem(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('maintenance_work_mem', $1, true)`, r.maintenanceWorkMem); err != nil {
		return fmt.Errorf("set local maintenance_work_mem: %w", err)
	}
	return nil
}

func (r *Runner) analyzeTables(ctx context.Context, tables ...string) error {
	for _, table := range tables {
		started := time.Now()
		if _, err := r.pool.Exec(ctx, fmt.Sprintf("ANALYZE %s", table)); err != nil {
			return fmt.Errorf("analyze %s: %w", table, err)
		}
		r.logf("imdb sync step complete: analyze %s duration=%s", table, time.Since(started).Round(time.Second))
	}
	return nil
}

func (r *Runner) dropTables(ctx context.Context, tx pgx.Tx, tables ...string) error {
	if len(tables) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", strings.Join(tables, ", ")))
	if err != nil {
		return fmt.Errorf("drop tables %s: %w", strings.Join(tables, ", "), err)
	}
	return nil
}

func deltaTableName(snapshotID int64, base string) string {
	return fmt.Sprintf("%s_delta_%d", base, snapshotID)
}

func (r *Runner) importDeltaSnapshot(ctx context.Context, snapshotID int64, changed []remoteDataset, remote []remoteDataset, baseline snapshotCounts, sourceUpdatedAt *time.Time, datasetVersion string) error {
	r.logf("imdb sync snapshot %d delta import started for %d datasets", snapshotID, len(changed))
	counts := baseline

	for _, item := range changed {
		var affectedTables []string
		tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return fmt.Errorf("begin delta tx for %s: %w", item.spec.Name, err)
		}

		stageTable := rawTableName(snapshotID, item.spec)
		r.logf("imdb sync preparing staging table %s for %s", stageTable, item.spec.Name)
		if _, err := tx.Exec(ctx, createRawTableStatement(stageTable, item.spec.ColumnDefs)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("create raw table for %s: %w", item.spec.Name, err)
		}
		if err := r.copyDataset(ctx, tx, item, stageTable); err != nil {
			tx.Rollback(ctx)
			return err
		}

		switch item.spec.Name {
		case "title.ratings.tsv.gz":
			counts.Ratings, err = r.mergeRatingsDelta(ctx, tx, snapshotID, stageTable)
			affectedTables = []string{"title_ratings"}
		case "title.episode.tsv.gz":
			counts.Episodes, err = r.mergeEpisodesDelta(ctx, tx, stageTable)
			affectedTables = []string{"title_episodes"}
		default:
			err = fmt.Errorf("unsupported delta dataset %s", item.spec.Name)
		}
		if err != nil {
			tx.Rollback(ctx)
			return err
		}

		if err := r.dropTables(ctx, tx, stageTable); err != nil {
			tx.Rollback(ctx)
			return err
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit delta tx for %s: %w", item.spec.Name, err)
		}
		if err := r.updateSnapshotProgress(ctx, snapshotID, counts); err != nil {
			return err
		}
		if len(affectedTables) > 0 {
			if err := r.analyzeTables(ctx, affectedTables...); err != nil {
				return err
			}
		}
	}

	if err := r.finalizeDeltaSnapshot(ctx, snapshotID, remote, counts, sourceUpdatedAt, datasetVersion); err != nil {
		return err
	}
	return nil
}

func (r *Runner) updateSnapshotProgress(ctx context.Context, snapshotID int64, counts snapshotCounts) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE imdb_snapshots
		SET
			rating_count = $2,
			episode_count = $3
		WHERE id = $1
	`, snapshotID, counts.Ratings, counts.Episodes)
	if err != nil {
		return fmt.Errorf("update snapshot progress: %w", err)
	}
	return nil
}

func (r *Runner) finalizeDeltaSnapshot(ctx context.Context, snapshotID int64, remote []remoteDataset, counts snapshotCounts, sourceUpdatedAt *time.Time, datasetVersion string) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin delta finalize tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `UPDATE imdb_snapshots SET is_active = FALSE WHERE id <> $1`, snapshotID); err != nil {
		return fmt.Errorf("deactivate previous snapshots: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE imdb_snapshots
		SET
			status = 'ready',
			dataset_version = $2,
			source_updated_at = $3,
			source_etag = $4,
			completed_at = NOW(),
			duration_seconds = GREATEST(EXTRACT(EPOCH FROM (NOW() - imported_at))::INTEGER, 0),
			is_active = TRUE,
			rating_count = $5,
			episode_count = $6,
			notes = ''
		WHERE id = $1
	`, snapshotID, datasetVersion, sourceUpdatedAt, joinRemoteValues(remote, func(item remoteDataset) string { return item.etag }), counts.Ratings, counts.Episodes); err != nil {
		return fmt.Errorf("finalize delta snapshot: %w", err)
	}
	if err := upsertSyncStateWithExecutor(ctx, tx, remote, &snapshotID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delta finalize tx: %w", err)
	}
	r.logf("imdb sync snapshot %d committed to live tables", snapshotID)
	return nil
}

func (r *Runner) execBatchedStatement(ctx context.Context, tx pgx.Tx, label, statement string, args ...any) (int64, error) {
	var total int64
	for {
		tag, err := tx.Exec(ctx, statement, args...)
		if err != nil {
			return total, fmt.Errorf("%s: %w", label, err)
		}
		if tag.RowsAffected() == 0 {
			return total, nil
		}
		total += tag.RowsAffected()
	}
}

func (r *Runner) mergeRatingsDelta(ctx context.Context, tx pgx.Tx, snapshotID int64, stageTable string) (int64, error) {
	deltaTable := deltaTableName(snapshotID, "title_ratings")
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		CREATE UNLOGGED TABLE %s (
			tconst TEXT PRIMARY KEY,
			average_rating NUMERIC(3,1) NOT NULL,
			num_votes INTEGER NOT NULL DEFAULT 0,
			row_hash TEXT NOT NULL
		)
	`, deltaTable)); err != nil {
		return 0, fmt.Errorf("create ratings delta table: %w", err)
	}
	tag, err := tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s (tconst, average_rating, num_votes, row_hash)
		SELECT
			r.tconst,
			r.average_rating::NUMERIC(3,1),
			COALESCE(NULLIF(r.num_votes, ''), '0')::INTEGER,
			md5(concat_ws('|', r.tconst, COALESCE(r.average_rating, ''), COALESCE(r.num_votes, '0')))
		FROM %s r
		WHERE r.tconst IS NOT NULL
		  AND r.average_rating IS NOT NULL
		  AND r.average_rating <> ''
	`, deltaTable, stageTable))
	if err != nil {
		return 0, fmt.Errorf("normalize ratings delta: %w", err)
	}
	count := tag.RowsAffected()
	if _, err := r.execBatchedStatement(ctx, tx, "insert ratings delta batch", fmt.Sprintf(`
		WITH batch AS (
			SELECT d.*
			FROM %s d
			LEFT JOIN title_ratings t ON t.tconst = d.tconst
			WHERE t.tconst IS NULL
			ORDER BY d.tconst
			LIMIT %d
		)
		INSERT INTO title_ratings (tconst, average_rating, num_votes, row_hash, updated_at)
		SELECT tconst, average_rating, num_votes, row_hash, NOW()
		FROM batch
	`, deltaTable, r.deltaBatchSize)); err != nil {
		return 0, err
	}
	if _, err := r.execBatchedStatement(ctx, tx, "update ratings delta batch", fmt.Sprintf(`
		WITH batch AS (
			SELECT d.*
			FROM %s d
			JOIN title_ratings t ON t.tconst = d.tconst
			WHERE t.row_hash IS DISTINCT FROM d.row_hash
			ORDER BY d.tconst
			LIMIT %d
		)
		UPDATE title_ratings t
		SET average_rating = b.average_rating, num_votes = b.num_votes, row_hash = b.row_hash, updated_at = NOW()
		FROM batch b
		WHERE t.tconst = b.tconst
	`, deltaTable, r.deltaBatchSize)); err != nil {
		return 0, err
	}
	if _, err := r.execBatchedStatement(ctx, tx, "delete ratings delta batch", fmt.Sprintf(`
		WITH batch AS (
			SELECT t.tconst
			FROM title_ratings t
			LEFT JOIN %s d ON d.tconst = t.tconst
			WHERE d.tconst IS NULL
			ORDER BY t.tconst
			LIMIT %d
		)
		DELETE FROM title_ratings t
		USING batch b
		WHERE t.tconst = b.tconst
	`, deltaTable, r.deltaBatchSize)); err != nil {
		return 0, err
	}
	if err := r.dropTables(ctx, tx, deltaTable); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *Runner) mergeEpisodesDelta(ctx context.Context, tx pgx.Tx, stageTable string) (int64, error) {
	deltaTable := deltaTableName(time.Now().UnixNano(), "title_episodes")
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		CREATE UNLOGGED TABLE %s (
			tconst TEXT PRIMARY KEY,
			parent_tconst TEXT NOT NULL,
			season_number INTEGER,
			episode_number INTEGER,
			row_hash TEXT NOT NULL
		)
	`, deltaTable)); err != nil {
		return 0, fmt.Errorf("create episodes delta table: %w", err)
	}
	tag, err := tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s (tconst, parent_tconst, season_number, episode_number, row_hash)
		SELECT
			e.tconst,
			e.parent_tconst,
			NULLIF(e.season_number, '')::INTEGER,
			NULLIF(e.episode_number, '')::INTEGER,
			md5(concat_ws('|', e.tconst, e.parent_tconst, COALESCE(e.season_number, ''), COALESCE(e.episode_number, '')))
		FROM %s e
		WHERE e.tconst IS NOT NULL
		  AND e.parent_tconst IS NOT NULL
		  AND e.parent_tconst <> ''
	`, deltaTable, stageTable))
	if err != nil {
		return 0, fmt.Errorf("normalize episodes delta: %w", err)
	}
	count := tag.RowsAffected()
	if _, err := r.execBatchedStatement(ctx, tx, "insert episodes delta batch", fmt.Sprintf(`
		WITH batch AS (
			SELECT d.*
			FROM %s d
			LEFT JOIN title_episodes t ON t.tconst = d.tconst
			WHERE t.tconst IS NULL
			ORDER BY d.tconst
			LIMIT %d
		)
		INSERT INTO title_episodes (tconst, parent_tconst, season_number, episode_number, row_hash, created_at)
		SELECT tconst, parent_tconst, season_number, episode_number, row_hash, NOW()
		FROM batch
	`, deltaTable, r.deltaBatchSize)); err != nil {
		return 0, err
	}
	if _, err := r.execBatchedStatement(ctx, tx, "update episodes delta batch", fmt.Sprintf(`
		WITH batch AS (
			SELECT d.*
			FROM %s d
			JOIN title_episodes t ON t.tconst = d.tconst
			WHERE t.row_hash IS DISTINCT FROM d.row_hash
			ORDER BY d.tconst
			LIMIT %d
		)
		UPDATE title_episodes t
		SET parent_tconst = b.parent_tconst, season_number = b.season_number, episode_number = b.episode_number, row_hash = b.row_hash
		FROM batch b
		WHERE t.tconst = b.tconst
	`, deltaTable, r.deltaBatchSize)); err != nil {
		return 0, err
	}
	if _, err := r.execBatchedStatement(ctx, tx, "delete episodes delta batch", fmt.Sprintf(`
		WITH batch AS (
			SELECT t.tconst
			FROM title_episodes t
			LEFT JOIN %s d ON d.tconst = t.tconst
			WHERE d.tconst IS NULL
			ORDER BY t.tconst
			LIMIT %d
		)
		DELETE FROM title_episodes t
		USING batch b
		WHERE t.tconst = b.tconst
	`, deltaTable, r.deltaBatchSize)); err != nil {
		return 0, err
	}
	if err := r.dropTables(ctx, tx, deltaTable); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *Runner) normalizeSnapshot(ctx context.Context, tx pgx.Tx, tables tableSet, snapshotID int64) (normalizeCounts, error) {
	rawRatings := rawTableName(snapshotID, r.datasetByName("title.ratings.tsv.gz"))
	rawEpisodes := rawTableName(snapshotID, r.datasetByName("title.episode.tsv.gz"))

	type normalizeStep struct {
		name      string
		statement string
	}

	var counts normalizeCounts
	steps := []normalizeStep{
		{name: "load ratings", statement: fmt.Sprintf(`
			INSERT INTO %s (tconst, average_rating, num_votes, row_hash, updated_at)
			SELECT
				tconst,
				average_rating::NUMERIC(3,1),
				COALESCE(NULLIF(num_votes, ''), '0')::INTEGER,
				md5(concat_ws('|', tconst, COALESCE(average_rating, ''), COALESCE(num_votes, '0'))),
				NOW()
			FROM %s
			WHERE tconst IS NOT NULL
			  AND average_rating IS NOT NULL
			  AND average_rating <> ''
		`, tables.TitleRatings, rawRatings)},
		{name: "load episodes", statement: fmt.Sprintf(`
			INSERT INTO %s (tconst, parent_tconst, season_number, episode_number, row_hash, created_at)
			SELECT
				e.tconst,
				e.parent_tconst,
				NULLIF(e.season_number, '')::INTEGER,
				NULLIF(e.episode_number, '')::INTEGER,
				md5(concat_ws('|', e.tconst, e.parent_tconst, COALESCE(e.season_number, ''), COALESCE(e.episode_number, ''))),
				NOW()
			FROM %s e
			WHERE e.tconst IS NOT NULL
			  AND e.parent_tconst IS NOT NULL
			  AND e.parent_tconst <> ''
		`, tables.TitleEpisodes, rawEpisodes)},
	}

	r.logf("imdb sync snapshot %d normalization started", snapshotID)
	for _, step := range steps {
		stepStarted := time.Now()
		tag, err := tx.Exec(ctx, step.statement)
		if err != nil {
			return counts, fmt.Errorf("%s: %w", step.name, err)
		}
		r.logf("imdb sync snapshot %d step complete: %s rows=%d duration=%s", snapshotID, step.name, tag.RowsAffected(), time.Since(stepStarted).Round(time.Second))
		switch step.name {
		case "load ratings":
			counts.Ratings = tag.RowsAffected()
		case "load episodes":
			counts.Episodes = tag.RowsAffected()
		}
	}

	return counts, nil
}

func (r *Runner) markSnapshotFailed(ctx context.Context, snapshotID int64, importErr error) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE imdb_snapshots
		SET status = 'failed', notes = $2, completed_at = NOW(), is_active = FALSE
		WHERE id = $1
	`, snapshotID, truncateText(importErr.Error(), 2000))
	if err != nil {
		return fmt.Errorf("mark snapshot failed: %w", err)
	}
	return nil
}

func (r *Runner) upsertSyncState(ctx context.Context, remote []remoteDataset, snapshotID *int64) error {
	return upsertSyncStateWithExecutor(ctx, r.pool, remote, snapshotID)
}

func upsertSyncStateWithExecutor(ctx context.Context, execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, remote []remoteDataset, snapshotID *int64) error {
	var snapshotValue any
	if snapshotID != nil {
		snapshotValue = *snapshotID
	}

	for _, item := range remote {
		_, err := execer.Exec(ctx, `
			INSERT INTO dataset_sync_state (dataset_name, source_url, etag, last_modified, checked_at, imported_at, snapshot_id)
			VALUES ($1, $2, $3, $4, NOW(), CASE WHEN $5::bigint IS NULL THEN NULL ELSE NOW() END, $5)
			ON CONFLICT (dataset_name)
			DO UPDATE SET
				source_url = EXCLUDED.source_url,
				etag = EXCLUDED.etag,
				last_modified = EXCLUDED.last_modified,
				checked_at = NOW(),
				imported_at = CASE WHEN EXCLUDED.snapshot_id IS NULL THEN dataset_sync_state.imported_at ELSE NOW() END,
				snapshot_id = EXCLUDED.snapshot_id
		`, item.spec.Name, item.url, item.etag, item.lastModified, snapshotValue)
		if err != nil {
			return fmt.Errorf("upsert dataset sync state for %s: %w", item.spec.Name, err)
		}
	}
	return nil
}

func latestSourceUpdatedAt(items []remoteDataset) *time.Time {
	var latest *time.Time
	for _, item := range items {
		if item.sourceUpdatedAt == nil {
			continue
		}
		if latest == nil || item.sourceUpdatedAt.After(*latest) {
			value := item.sourceUpdatedAt.UTC()
			latest = &value
		}
	}
	return latest
}

func datasetVersion(sourceUpdatedAt *time.Time) string {
	if sourceUpdatedAt != nil {
		return sourceUpdatedAt.UTC().Format(time.RFC3339)
	}
	return time.Now().UTC().Format(time.RFC3339)
}

func joinRemoteValues(items []remoteDataset, selector func(remoteDataset) string) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		value := strings.TrimSpace(selector(item))
		if value == "" {
			continue
		}
		parts = append(parts, item.spec.Name+"="+value)
	}
	return strings.Join(parts, "; ")
}

func truncateText(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
