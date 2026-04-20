package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"nexio-imdb/apps/api/internal/imdb"
)

const (
	maxConcurrentSocketsPerKey = 5
	socketMessageBurst         = 20
	socketMessageRatePerSecond = 20
	pingInterval               = 25 * time.Second
	pingTimeout                = 10 * time.Second
	writeTimeout               = 10 * time.Second
	wsSearchLimit              = 10
)

// wsInbound is a client-to-server WebSocket frame.
type wsInbound struct {
	Type  string   `json:"type"`
	Seq   uint64   `json:"seq"`
	Q     string   `json:"q,omitempty"`
	Types []string `json:"types,omitempty"`
}

// wsOutbound is a server-to-client WebSocket frame.
type wsOutbound struct {
	Type    string                   `json:"type"`
	Seq     uint64                   `json:"seq"`
	Results []imdb.TitleSearchResult `json:"results,omitempty"`
	Meta    *imdb.TitleSearchMeta    `json:"meta,omitempty"`
	Code    string                   `json:"code,omitempty"`
	Message string                   `json:"message,omitempty"`
}

// handleSearchFrame validates a search frame, cancels any prior in-flight
// search, and spawns a goroutine to execute the query.
func (h Handler) handleSearchFrame(
	connCtx context.Context,
	msg wsInbound,
	bucket *socketBucket,
	cs *cancelStore,
	writeFrame func(wsOutbound),
) {
	if !bucket.allow() {
		writeFrame(wsOutbound{
			Type:    "error",
			Seq:     msg.Seq,
			Code:    "rate_limited",
			Message: "message rate too high",
		})
		return
	}

	q := strings.TrimSpace(msg.Q)
	if len(q) < 2 {
		writeFrame(wsOutbound{
			Type:    "error",
			Seq:     msg.Seq,
			Code:    "invalid_request",
			Message: "q must be at least 2 characters",
		})
		return
	}

	types := []string{"movie", "tvSeries"}
	if len(msg.Types) > 0 {
		filtered := types[:0]
		for _, t := range msg.Types {
			if !allowedTitleTypes[t] {
				writeFrame(wsOutbound{
					Type:    "error",
					Seq:     msg.Seq,
					Code:    "invalid_request",
					Message: "types must be movie and/or tvSeries",
				})
				return
			}
			filtered = append(filtered, t)
		}
		if len(filtered) == 0 {
			writeFrame(wsOutbound{
				Type:    "error",
				Seq:     msg.Seq,
				Code:    "invalid_request",
				Message: "types must be movie and/or tvSeries",
			})
			return
		}
		types = filtered
	}

	// Cancel any prior in-flight search, then start this one.
	searchCtx, searchCancel := context.WithCancel(connCtx)
	cs.replaceAll(msg.Seq, searchCancel)

	capturedSeq := msg.Seq
	capturedTypes := append([]string(nil), types...)
	capturedLimit := wsSearchLimit
	capturedQ := q

	go func() {
		defer cs.remove(capturedSeq)
		resp, err := h.service.SearchTitles(searchCtx, imdb.TitleSearchQuery{
			Q:     capturedQ,
			Types: capturedTypes,
			Limit: capturedLimit,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				writeFrame(wsOutbound{
					Type: "cancelled",
					Seq:  capturedSeq,
				})
				return
			}
			writeFrame(wsOutbound{
				Type:    "error",
				Seq:     capturedSeq,
				Code:    "internal_error",
				Message: "internal server error",
			})
			return
		}
		meta := imdb.TitleSearchMeta{
			SnapshotID: resp.Meta.SnapshotID,
			Count:      resp.Meta.Count,
		}
		writeFrame(wsOutbound{
			Type:    "result",
			Seq:     capturedSeq,
			Results: resp.Results,
			Meta:    &meta,
		})
	}()
}

// socketBucket is a simple per-socket token bucket for message-rate safety.
// It lives here rather than in ratelimit.go because it is a different concern:
// it protects against runaway clients on a single socket, not cross-request quota.
type socketBucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func newSocketBucket() *socketBucket {
	return &socketBucket{
		tokens: socketMessageBurst,
		last:   time.Now(),
	}
}

// allow returns true and deducts one token if the message is within rate.
func (b *socketBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		refilled := b.tokens + elapsed*socketMessageRatePerSecond
		if refilled > socketMessageBurst {
			refilled = socketMessageBurst
		}
		b.tokens = refilled
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// cancelStore holds the cancel function for the one in-flight search per socket.
// keyed by the seq of that search.
type cancelStore struct {
	mu      sync.Mutex
	cancels map[uint64]context.CancelFunc
}

func newCancelStore() *cancelStore {
	return &cancelStore{cancels: make(map[uint64]context.CancelFunc)}
}

// replaceAll cancels every in-flight search (there is at most one) and stores
// the new cancel under seq. Returns any previously-held seq values whose
// goroutines must emit a "cancelled" frame.
func (cs *cancelStore) replaceAll(seq uint64, cancel context.CancelFunc) []uint64 {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	var cancelled []uint64
	for s, c := range cs.cancels {
		c()
		cancelled = append(cancelled, s)
		delete(cs.cancels, s)
	}
	cs.cancels[seq] = cancel
	return cancelled
}

// remove deletes the entry for seq (called by the goroutine when it finishes).
func (cs *cancelStore) remove(seq uint64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.cancels, seq)
}

// websocketSearch upgrades the connection and runs the WebSocket message loop.
// Auth is enforced by the requireAPIKey middleware before this handler is called.
// TODO: OriginPatterns: []string{"*"} is intentionally permissive; auth is
// already enforced via API key on the HTTP handshake, so browser-origin
// checking adds no security here. Tighten to specific origins if needed.
func (h Handler) websocketSearch(w http.ResponseWriter, r *http.Request) {
	principal := principalFromContext(r.Context())
	if principal == nil {
		writeError(w, http.StatusUnauthorized, "missing_api_key", "api key required")
		return
	}

	// Handshake rate limit: charge one token against the shared REST bucket.
	if h.limiter != nil {
		decision, err := h.limiter.AllowConnect(principal)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		if !decision.Allowed {
			writeRateLimited(w, decision)
			return
		}
	}

	// Concurrency cap: at most maxConcurrentSocketsPerKey open sockets per key.
	if !h.connections.Acquire(principal.KeyID) {
		writeRateLimited(w, RateLimitDecision{Allowed: false})
		return
	}
	defer h.connections.Release(principal.KeyID)

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		// Accept already wrote an error response.
		return
	}
	defer conn.CloseNow()

	connCtx, connCancel := context.WithCancel(r.Context())
	defer connCancel()

	bucket := newSocketBucket()
	cs := newCancelStore()

	var writeMu sync.Mutex
	writeFrame := func(payload wsOutbound) {
		data, err := json.Marshal(payload)
		if err != nil {
			return
		}
		writeCtx, writeCancel := context.WithTimeout(connCtx, writeTimeout)
		defer writeCancel()
		writeMu.Lock()
		defer writeMu.Unlock()
		if err := conn.Write(writeCtx, websocket.MessageText, data); err != nil {
			// Slow or dead client — close and cancel the connection context.
			connCancel()
		}
	}

	// Ping goroutine: keep the connection alive and detect dead peers.
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-connCtx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(connCtx, pingTimeout)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					conn.Close(websocket.StatusGoingAway, "ping timeout")
					connCancel()
					return
				}
			}
		}
	}()

	// Message read loop.
	for {
		_, data, err := conn.Read(connCtx)
		if err != nil {
			conn.Close(websocket.StatusNormalClosure, "")
			return
		}

		var msg wsInbound
		if err := json.Unmarshal(data, &msg); err != nil {
			writeFrame(wsOutbound{
				Type:    "error",
				Seq:     0,
				Code:    "invalid_request",
				Message: "invalid JSON",
			})
			continue
		}

		switch msg.Type {
		case "ping":
			writeFrame(wsOutbound{Type: "pong", Seq: msg.Seq})

		case "search":
			h.handleSearchFrame(connCtx, msg, bucket, cs, writeFrame)

		default:
			writeFrame(wsOutbound{
				Type:    "error",
				Seq:     msg.Seq,
				Code:    "invalid_request",
				Message: "unknown message type",
			})
		}
	}
}
