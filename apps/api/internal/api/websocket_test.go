package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nexio-imdb/apps/api/internal/auth"
	"nexio-imdb/apps/api/internal/imdb"
)

// dialWS connects to the given httptest.Server's WebSocket endpoint with an
// optional API key header.
func dialWS(t *testing.T, srv *httptest.Server, apiKey string) (*websocket.Conn, *http.Response) {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/ws"
	opts := &websocket.DialOptions{}
	if apiKey != "" {
		opts.HTTPHeader = http.Header{"X-Api-Key": {apiKey}}
	}
	conn, resp, err := websocket.Dial(context.Background(), url, opts)
	if err != nil {
		// Dial may fail legitimately for rejected handshakes (401/429);
		// the caller checks resp.StatusCode.
		return nil, resp
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn, resp
}

// sendJSON sends a JSON-encoded message over the WebSocket connection.
func sendJSON(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// recvJSON reads one frame and unmarshals it into v.
func recvJSON(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

// newWSRouter builds a router wired for WebSocket tests with default stubs.
func newWSRouter(svc imdb.QueryService, limiter RateLimiter) http.Handler {
	if limiter == nil {
		limiter = stubRateLimiter{}
	}
	return NewRouter(svc, stubAuthenticator{
		authenticate: func(context.Context, string) (*auth.Principal, error) {
			return &auth.Principal{KeyID: 1, Prefix: "test"}, nil
		},
	}, limiter)
}

// --- Tests ---

func TestWS_AuthRequired(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(NewRouter(stubService{}, stubAuthenticator{}, nil))
	defer srv.Close()

	// No API key — should get 401 before upgrade.
	_, resp := dialWS(t, srv, "")
	if resp == nil {
		t.Fatal("expected HTTP response on failed handshake")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestWS_BasicSearch(t *testing.T) {
	t.Parallel()

	year := 1999
	svc := stubService{
		searchTitles: func(_ context.Context, q imdb.TitleSearchQuery) (imdb.TitleSearchResponse, error) {
			return imdb.TitleSearchResponse{
				Results: []imdb.TitleSearchResult{
					{Tconst: "tt0133093", TitleType: "movie", PrimaryTitle: "The Matrix", StartYear: &year},
				},
				Meta: imdb.TitleSearchMeta{SnapshotID: 42, Count: 1},
			}, nil
		},
	}

	srv := httptest.NewServer(newWSRouter(svc, nil))
	defer srv.Close()

	conn, _ := dialWS(t, srv, "valid-key")
	if conn == nil {
		t.Fatal("expected successful WebSocket connection")
	}

	sendJSON(t, conn, wsInbound{Type: "search", Seq: 1, Q: "matrix"})

	var out wsOutbound
	recvJSON(t, conn, &out)

	if out.Type != "result" {
		t.Fatalf("expected type=result, got %q", out.Type)
	}
	if out.Seq != 1 {
		t.Fatalf("expected seq=1, got %d", out.Seq)
	}
	if len(out.Results) == 0 {
		t.Fatal("expected non-empty results")
	}
	if out.Results[0].Tconst != "tt0133093" {
		t.Fatalf("unexpected tconst %q", out.Results[0].Tconst)
	}
	if out.Meta == nil || out.Meta.SnapshotID != 42 {
		t.Fatalf("unexpected meta %+v", out.Meta)
	}
}

func TestWS_SeqEchoed(t *testing.T) {
	t.Parallel()

	svc := stubService{
		searchTitles: func(context.Context, imdb.TitleSearchQuery) (imdb.TitleSearchResponse, error) {
			return imdb.TitleSearchResponse{Results: []imdb.TitleSearchResult{}}, nil
		},
	}

	srv := httptest.NewServer(newWSRouter(svc, nil))
	defer srv.Close()

	conn, _ := dialWS(t, srv, "valid-key")
	if conn == nil {
		t.Fatal("expected successful connection")
	}

	sendJSON(t, conn, wsInbound{Type: "search", Seq: 99, Q: "matrix"})

	var out wsOutbound
	recvJSON(t, conn, &out)

	if out.Seq != 99 {
		t.Fatalf("expected seq=99, got %d", out.Seq)
	}
}

// blockingService is a fake imdb.QueryService that blocks SearchTitles on a
// channel, allowing tests to control concurrency.
type blockingService struct {
	stubService
	// unblock receives a struct{} to unblock a pending SearchTitles call.
	unblock chan struct{}
	// started is closed when the goroutine enters SearchTitles.
	started chan struct{}
}

func newBlockingService(svc stubService) *blockingService {
	return &blockingService{
		stubService: svc,
		unblock:     make(chan struct{}, 1),
		started:     make(chan struct{}),
	}
}

func (b *blockingService) SearchTitles(ctx context.Context, q imdb.TitleSearchQuery) (imdb.TitleSearchResponse, error) {
	// Signal that we entered.
	select {
	case <-b.started:
	default:
		close(b.started)
	}
	// Block until unblocked or context cancelled.
	select {
	case <-b.unblock:
		return b.stubService.SearchTitles(ctx, q)
	case <-ctx.Done():
		return imdb.TitleSearchResponse{}, ctx.Err()
	}
}

func TestWS_CancellationOnNewSearch(t *testing.T) {
	t.Parallel()

	year := 1999
	bs := newBlockingService(stubService{
		searchTitles: func(_ context.Context, q imdb.TitleSearchQuery) (imdb.TitleSearchResponse, error) {
			return imdb.TitleSearchResponse{
				Results: []imdb.TitleSearchResult{
					{Tconst: "tt0133093", TitleType: "movie", PrimaryTitle: "The Matrix", StartYear: &year},
				},
				Meta: imdb.TitleSearchMeta{SnapshotID: 1, Count: 1},
			}, nil
		},
	})

	srv := httptest.NewServer(newWSRouter(bs, nil))
	defer srv.Close()

	conn, _ := dialWS(t, srv, "valid-key")
	if conn == nil {
		t.Fatal("expected successful connection")
	}

	// Send seq=1; it will block.
	sendJSON(t, conn, wsInbound{Type: "search", Seq: 1, Q: "matrix"})

	// Wait until seq=1 goroutine is inside SearchTitles.
	select {
	case <-bs.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for first search to start")
	}

	// Send seq=2; this cancels seq=1 and immediately unblocks by receiving
	// the result after we unblock.
	// First: create a new started channel for seq=2.
	bs.started = make(chan struct{})
	sendJSON(t, conn, wsInbound{Type: "search", Seq: 2, Q: "matrix2"})

	// seq=1 should get a "cancelled" frame.
	// seq=2 is blocked — unblock it.
	select {
	case <-bs.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for second search to start")
	}
	bs.unblock <- struct{}{}

	// Collect frames; we expect "cancelled" for seq=1 and "result" for seq=2.
	frames := make(map[string]wsOutbound)
	for i := 0; i < 2; i++ {
		var out wsOutbound
		recvJSON(t, conn, &out)
		frames[out.Type+":"+string(rune('0'+out.Seq))] = out
	}

	cancelled, ok := frames["cancelled:"+string(rune('0'+1))]
	if !ok {
		// Try finding any cancelled frame.
		for k, v := range frames {
			if v.Type == "cancelled" {
				cancelled = v
				ok = true
				_ = k
				break
			}
		}
	}
	if !ok || cancelled.Type != "cancelled" || cancelled.Seq != 1 {
		t.Fatalf("expected cancelled frame for seq=1, got frames: %+v", frames)
	}

	resultFound := false
	for _, v := range frames {
		if v.Type == "result" && v.Seq == 2 {
			resultFound = true
		}
	}
	if !resultFound {
		t.Fatalf("expected result frame for seq=2, got frames: %+v", frames)
	}
}

func TestWS_InvalidQ(t *testing.T) {
	t.Parallel()

	svc := stubService{
		searchTitles: func(_ context.Context, q imdb.TitleSearchQuery) (imdb.TitleSearchResponse, error) {
			return imdb.TitleSearchResponse{Results: []imdb.TitleSearchResult{}}, nil
		},
	}

	srv := httptest.NewServer(newWSRouter(svc, nil))
	defer srv.Close()

	conn, _ := dialWS(t, srv, "valid-key")
	if conn == nil {
		t.Fatal("expected successful connection")
	}

	// Single-character q — should get error, connection stays open.
	sendJSON(t, conn, wsInbound{Type: "search", Seq: 3, Q: "a"})

	var errOut wsOutbound
	recvJSON(t, conn, &errOut)
	if errOut.Type != "error" || errOut.Code != "invalid_request" || errOut.Seq != 3 {
		t.Fatalf("expected error frame, got %+v", errOut)
	}

	// Now send a valid search — connection should still work.
	sendJSON(t, conn, wsInbound{Type: "search", Seq: 4, Q: "matrix"})
	var resultOut wsOutbound
	recvJSON(t, conn, &resultOut)
	if resultOut.Type != "result" || resultOut.Seq != 4 {
		t.Fatalf("expected result frame for seq=4, got %+v", resultOut)
	}
}

func TestWS_InvalidType(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(newWSRouter(stubService{}, nil))
	defer srv.Close()

	conn, _ := dialWS(t, srv, "valid-key")
	if conn == nil {
		t.Fatal("expected successful connection")
	}

	// Unknown type "channel" — error frame, socket stays open.
	sendJSON(t, conn, wsInbound{Type: "search", Seq: 5, Q: "matrix", Types: []string{"channel"}})

	var out wsOutbound
	recvJSON(t, conn, &out)
	if out.Type != "error" || out.Code != "invalid_request" || out.Seq != 5 {
		t.Fatalf("expected error frame, got %+v", out)
	}

	// Verify socket is still alive by sending ping.
	sendJSON(t, conn, wsInbound{Type: "ping", Seq: 6})
	var pong wsOutbound
	recvJSON(t, conn, &pong)
	if pong.Type != "pong" || pong.Seq != 6 {
		t.Fatalf("expected pong after invalid-type error, got %+v", pong)
	}
}

func TestWS_PingPong(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(newWSRouter(stubService{}, nil))
	defer srv.Close()

	conn, _ := dialWS(t, srv, "valid-key")
	if conn == nil {
		t.Fatal("expected successful connection")
	}

	sendJSON(t, conn, wsInbound{Type: "ping", Seq: 10})

	var out wsOutbound
	recvJSON(t, conn, &out)
	if out.Type != "pong" || out.Seq != 10 {
		t.Fatalf("expected pong seq=10, got %+v", out)
	}
}

func TestWS_SafetyCeiling(t *testing.T) {
	t.Parallel()

	svc := stubService{
		searchTitles: func(_ context.Context, q imdb.TitleSearchQuery) (imdb.TitleSearchResponse, error) {
			return imdb.TitleSearchResponse{Results: []imdb.TitleSearchResult{}}, nil
		},
	}

	srv := httptest.NewServer(newWSRouter(svc, nil))
	defer srv.Close()

	conn, _ := dialWS(t, srv, "valid-key")
	if conn == nil {
		t.Fatal("expected successful connection")
	}

	// Send 30 searches rapidly; some should get rate_limited errors.
	const total = 30
	for i := uint64(1); i <= total; i++ {
		sendJSON(t, conn, wsInbound{Type: "search", Seq: i, Q: "matrix"})
	}

	// Collect all responses. We allow up to 5s total for all frames to arrive.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rateLimited := 0
	results := 0
	for i := 0; i < total; i++ {
		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		var out wsOutbound
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("unmarshal frame: %v", err)
		}
		switch {
		case out.Type == "error" && out.Code == "rate_limited":
			rateLimited++
		case out.Type == "result":
			results++
		}
	}

	if rateLimited == 0 {
		t.Fatal("expected at least one rate_limited frame from 30 rapid messages")
	}
	// Socket should still be alive — send a ping.
	sendJSON(t, conn, wsInbound{Type: "ping", Seq: 999})
	var pong wsOutbound
	recvJSON(t, conn, &pong)
	if pong.Type != "pong" {
		t.Fatalf("expected socket still alive after rate limiting, got %+v", pong)
	}
}

func TestWS_ConcurrentConnectionCap(t *testing.T) {
	t.Parallel()

	// Use KeyID=99 (unique across test suite) with a dedicated server so the
	// connection counter starts at zero and is not shared with other tests.
	srv := httptest.NewServer(NewRouter(
		stubService{},
		stubAuthenticator{
			authenticate: func(context.Context, string) (*auth.Principal, error) {
				return &auth.Principal{KeyID: 99, Prefix: "test"}, nil
			},
		},
		stubRateLimiter{},
	))
	defer srv.Close()

	conns := make([]*websocket.Conn, 0, maxConcurrentSocketsPerKey)
	for i := 0; i < maxConcurrentSocketsPerKey; i++ {
		conn, resp := dialWS(t, srv, "valid-key")
		if conn == nil {
			t.Fatalf("connection %d failed with status %d", i+1, resp.StatusCode)
		}
		conns = append(conns, conn)
	}

	// (maxConcurrentSocketsPerKey+1)th connection should be rejected with 429.
	_, resp := dialWS(t, srv, "valid-key")
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for connection %d, got resp=%v", maxConcurrentSocketsPerKey+1, resp)
	}

	// Close one connection and verify a new one is accepted.
	conns[0].Close(websocket.StatusNormalClosure, "done")
	// Give the server a moment to process the close and release the slot.
	time.Sleep(50 * time.Millisecond)

	conn, _ := dialWS(t, srv, "valid-key")
	if conn == nil {
		t.Fatal("expected new connection to succeed after releasing one slot")
	}
}

func TestWS_HandshakeRateLimit(t *testing.T) {
	t.Parallel()

	// The /ws route goes through requireAPIKey (cost=1 via Allow) and then
	// AllowConnect (cost=1) — so each connect consumes 2 tokens. With burst=2
	// the first connect succeeds; the bucket is empty and the second is rejected.
	limiter := NewRequestRateLimiter(RateLimitConfig{
		Enabled:         true,
		TokensPerSecond: 1,
		Burst:           2,
		EpisodesCost:    8,
		BulkDivisor:     25,
	})
	// Freeze time so tokens don't refill between calls.
	frozen := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return frozen }

	srv := httptest.NewServer(NewRouter(
		stubService{},
		stubAuthenticator{
			authenticate: func(context.Context, string) (*auth.Principal, error) {
				return &auth.Principal{KeyID: 77, Prefix: "rl"}, nil
			},
		},
		limiter,
	))
	defer srv.Close()

	// First connect should succeed (consumes 2 tokens: Allow + AllowConnect).
	conn1, resp1 := dialWS(t, srv, "valid-key")
	if conn1 == nil {
		t.Fatalf("first connect failed: status %v", resp1)
	}
	conn1.CloseNow()

	// Second connect immediately — bucket empty → 429.
	_, resp2 := dialWS(t, srv, "valid-key")
	if resp2 == nil || resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on second handshake, got resp=%v", resp2)
	}
}

func TestWebSocketAlwaysCapsAtTen(t *testing.T) {
	t.Parallel()

	var capturedLimit int
	svc := stubService{
		searchTitles: func(_ context.Context, q imdb.TitleSearchQuery) (imdb.TitleSearchResponse, error) {
			capturedLimit = q.Limit
			return imdb.TitleSearchResponse{Results: []imdb.TitleSearchResult{}}, nil
		},
	}

	srv := httptest.NewServer(newWSRouter(svc, nil))
	defer srv.Close()

	conn, _ := dialWS(t, srv, "valid-key")
	if conn == nil {
		t.Fatal("expected successful connection")
	}

	// Send a search frame with no limit field.
	sendJSON(t, conn, wsInbound{Type: "search", Seq: 1, Q: "matrix"})

	var out wsOutbound
	recvJSON(t, conn, &out)
	if out.Type != "result" {
		t.Fatalf("expected result frame, got %+v", out)
	}

	if capturedLimit != wsSearchLimit {
		t.Fatalf("expected service called with Limit=%d, got %d", wsSearchLimit, capturedLimit)
	}
}

