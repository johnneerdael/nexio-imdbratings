package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"nexio-imdb/apps/api/internal/auth"
	"nexio-imdb/apps/api/internal/imdb"
)

type Handler struct {
	service imdb.QueryService
	auth    auth.Authenticator
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewRouter(service imdb.QueryService, authenticator auth.Authenticator) http.Handler {
	handler := Handler{
		service: service,
		auth:    authenticator,
	}

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(30 * time.Second))

	router.Get("/healthz", handler.healthz)
	router.Get("/readyz", handler.readyz)

	router.Route("/v1", func(r chi.Router) {
		r.Use(handler.requireAPIKey)
		r.Get("/meta/snapshots", handler.listSnapshots)
		r.Get("/meta/stats", handler.getStats)
		r.Get("/ratings/{tconst}", handler.getRating)
		r.Post("/ratings/bulk", handler.bulkGetRatings)
	})

	return router
}

func (h Handler) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h Handler) readyz(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Ready(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "service is not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h Handler) listSnapshots(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListSnapshots(r.Context())
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h Handler) getStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.service.GetStats(r.Context())
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h Handler) getRating(w http.ResponseWriter, r *http.Request) {
	tconst := chi.URLParam(r, "tconst")
	if wantsEpisodes(r) {
		result, err := h.service.GetRatingWithEpisodes(r.Context(), tconst)
		if err != nil {
			handleServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}

	rating, err := h.service.GetRating(r.Context(), tconst)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rating)
}

func (h Handler) bulkGetRatings(w http.ResponseWriter, r *http.Request) {
	identifiers, err := decodeIdentifierBody(r)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	if wantsEpisodes(r) {
		results := make([]imdb.RatingWithEpisodes, 0, len(identifiers))
		missing := make([]string, 0)
		for _, id := range identifiers {
			item, err := h.service.GetRatingWithEpisodes(r.Context(), id)
			if err != nil {
				if errors.Is(err, imdb.ErrNotFound) {
					missing = append(missing, id)
					continue
				}
				handleServiceError(w, err)
				return
			}
			results = append(results, item)
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results, "missing": missing})
		return
	}

	results := make([]imdb.Rating, 0, len(identifiers))
	missing := make([]string, 0)
	for _, id := range identifiers {
		item, err := h.service.GetRating(r.Context(), id)
		if err != nil {
			if errors.Is(err, imdb.ErrNotFound) {
				missing = append(missing, id)
				continue
			}
			handleServiceError(w, err)
			return
		}
		results = append(results, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "missing": missing})
}

func (h Handler) requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
		if apiKey == "" {
			authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
			if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
				apiKey = strings.TrimSpace(authHeader[7:])
			}
		}
		if apiKey == "" {
			writeError(w, http.StatusUnauthorized, "missing_api_key", "api key required")
			return
		}

		if _, err := h.auth.Authenticate(r.Context(), apiKey); err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_api_key", "api key invalid")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func wantsEpisodes(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("episodes")), "true")
}

func decodeJSONBody(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func decodeIdentifierBody(r *http.Request) ([]string, error) {
	var body struct {
		Identifiers []string `json:"identifiers"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		return nil, imdb.ErrInvalidRequest
	}
	if len(body.Identifiers) == 0 || len(body.Identifiers) > 250 {
		return nil, imdb.ErrInvalidRequest
	}

	identifiers := make([]string, 0, len(body.Identifiers))
	for _, item := range body.Identifiers {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, imdb.ErrInvalidRequest
		}
		identifiers = append(identifiers, item)
	}
	return identifiers, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{
		Error: apiError{
			Code:    code,
			Message: message,
		},
	})
}

func handleServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, imdb.ErrInvalidRequest):
		writeError(w, http.StatusBadRequest, "invalid_request", "request parameters are invalid")
	case errors.Is(err, imdb.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
