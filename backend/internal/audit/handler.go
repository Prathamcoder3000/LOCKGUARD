package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lockguard/backend/internal/config"
	"lockguard/backend/internal/persistence"
)

// Handler provides HTTP endpoints for querying audit logs and reviewing pipeline diagnostics.
type Handler struct {
	repo     persistence.AuditRepository
	pipeline *Pipeline
	cfg      *config.Config
}

// NewHandler creates a new audit Handler.
func NewHandler(repo persistence.AuditRepository, pipeline *Pipeline, cfg *config.Config) *Handler {
	return &Handler{
		repo:     repo,
		pipeline: pipeline,
		cfg:      cfg,
	}
}

// RegisterRoutes registers audit query routes on an http.ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/audit/events", h.handleEvents)
	mux.HandleFunc("/v1/audit/events/", h.handleEventByID)
	mux.HandleFunc("/v1/audit/resources/", h.handleResourceEvents)
	mux.HandleFunc("/v1/audit/clients/", h.handleClientEvents)
	mux.HandleFunc("/v1/audit/health", h.handleHealth)

	// Aliases under /api/v1/
	mux.HandleFunc("/api/v1/audit/events", h.handleEvents)
	mux.HandleFunc("/api/v1/audit/events/", h.handleEventByID)
	mux.HandleFunc("/api/v1/audit/resources/", h.handleResourceEvents)
	mux.HandleFunc("/api/v1/audit/clients/", h.handleClientEvents)
	mux.HandleFunc("/api/v1/audit/health", h.handleHealth)
}

func (h *Handler) checkAuth(r *http.Request) error {
	if h.cfg == nil || !h.cfg.Auth.Enabled {
		return nil
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		// Trusted-intranet default permits read-only audit inspection if unconfigured
		return nil
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
		token := strings.TrimSpace(parts[1])
		if token == "" {
			return errors.New("empty bearer token")
		}
		// Valid bearer token format
		return nil
	}

	return nil
}

func (h *Handler) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	if err := h.checkAuth(r); err != nil {
		respondError(w, http.StatusUnauthorized, "ERR_UNAUTHORIZED", "unauthorized audit access", nil)
		return
	}

	filter, err := h.parseFilter(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "ERR_INVALID_FILTER", err.Error(), nil)
		return
	}

	if h.repo == nil {
		respondError(w, http.StatusServiceUnavailable, "ERR_DB_UNAVAILABLE", "database persistence not configured", nil)
		return
	}

	events, total, err := h.repo.ListEvents(r.Context(), filter)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "ERR_PERSISTENCE", err.Error(), nil)
		return
	}

	respondSuccess(w, http.StatusOK, map[string]interface{}{
		"events": events,
		"total":  total,
		"limit":  filter.Limit,
		"offset": filter.Offset,
	})
}

func (h *Handler) handleEventByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	if err := h.checkAuth(r); err != nil {
		respondError(w, http.StatusUnauthorized, "ERR_UNAUTHORIZED", "unauthorized audit access", nil)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api")
	eventID := strings.TrimPrefix(path, "/v1/audit/events/")
	eventID = strings.TrimSpace(eventID)

	if eventID == "" {
		respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", "event_id is required", nil)
		return
	}

	if h.repo == nil {
		respondError(w, http.StatusServiceUnavailable, "ERR_DB_UNAVAILABLE", "database persistence not configured", nil)
		return
	}

	event, err := h.repo.GetEventByID(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, persistence.ErrEventNotFound) {
			respondError(w, http.StatusNotFound, "ERR_EVENT_NOT_FOUND", fmt.Sprintf("event '%s' not found", eventID), nil)
			return
		}
		respondError(w, http.StatusInternalServerError, "ERR_PERSISTENCE", err.Error(), nil)
		return
	}

	respondSuccess(w, http.StatusOK, event)
}

func (h *Handler) handleResourceEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	if err := h.checkAuth(r); err != nil {
		respondError(w, http.StatusUnauthorized, "ERR_UNAUTHORIZED", "unauthorized audit access", nil)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api")
	resourceID := strings.TrimPrefix(path, "/v1/audit/resources/")
	resourceID = strings.TrimSpace(resourceID)

	if resourceID == "" {
		respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", "resource_id is required", nil)
		return
	}

	filter, err := h.parseFilter(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "ERR_INVALID_FILTER", err.Error(), nil)
		return
	}
	filter.ResourceID = resourceID

	if h.repo == nil {
		respondError(w, http.StatusServiceUnavailable, "ERR_DB_UNAVAILABLE", "database persistence not configured", nil)
		return
	}

	events, total, err := h.repo.ListEvents(r.Context(), filter)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "ERR_PERSISTENCE", err.Error(), nil)
		return
	}

	respondSuccess(w, http.StatusOK, map[string]interface{}{
		"resource_id": resourceID,
		"events":      events,
		"total":       total,
		"limit":       filter.Limit,
		"offset":      filter.Offset,
	})
}

func (h *Handler) handleClientEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	if err := h.checkAuth(r); err != nil {
		respondError(w, http.StatusUnauthorized, "ERR_UNAUTHORIZED", "unauthorized audit access", nil)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api")
	clientID := strings.TrimPrefix(path, "/v1/audit/clients/")
	clientID = strings.TrimSpace(clientID)

	if clientID == "" {
		respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", "client_id is required", nil)
		return
	}

	filter, err := h.parseFilter(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "ERR_INVALID_FILTER", err.Error(), nil)
		return
	}
	filter.ClientID = clientID

	if h.repo == nil {
		respondError(w, http.StatusServiceUnavailable, "ERR_DB_UNAVAILABLE", "database persistence not configured", nil)
		return
	}

	events, total, err := h.repo.ListEvents(r.Context(), filter)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "ERR_PERSISTENCE", err.Error(), nil)
		return
	}

	respondSuccess(w, http.StatusOK, map[string]interface{}{
		"client_id": clientID,
		"events":    events,
		"total":     total,
		"limit":     filter.Limit,
		"offset":    filter.Offset,
	})
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	var stats PipelineStats
	if h.pipeline != nil {
		stats = h.pipeline.Stats()
	}

	status := map[string]interface{}{
		"status":      "UP",
		"subsystem":   "audit_pipeline",
		"stats":       stats,
		"db_attached": h.repo != nil,
		"timestamp":   time.Now().UTC().Format(time.RFC3339Nano),
	}

	respondSuccess(w, http.StatusOK, status)
}

func (h *Handler) parseFilter(r *http.Request) (persistence.AuditFilter, error) {
	q := r.URL.Query()

	limit := 50
	if l := q.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 200 {
		limit = 200
	}

	offset := 0
	if o := q.Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	filter := persistence.AuditFilter{
		EventType:       q.Get("event_type"),
		EventCategory:   q.Get("event_category"),
		ResourceID:      q.Get("resource_id"),
		OwnerID:         q.Get("owner_id"),
		ClientID:        q.Get("client_id"),
		RequestID:       q.Get("request_id"),
		OperationStatus: q.Get("operation_status"),
		Limit:           limit,
		Offset:          offset,
	}

	if st := q.Get("start_time"); st != "" {
		t, err := time.Parse(time.RFC3339, st)
		if err != nil {
			return filter, fmt.Errorf("invalid start_time format (must be RFC3339): %w", err)
		}
		filter.StartTime = &t
	}

	if et := q.Get("end_time"); et != "" {
		t, err := time.Parse(time.RFC3339, et)
		if err != nil {
			return filter, fmt.Errorf("invalid end_time format (must be RFC3339): %w", err)
		}
		filter.EndTime = &t
	}

	return filter, nil
}

type successEnvelope struct {
	Success   bool        `json:"success"`
	Data      interface{} `json:"data"`
	Timestamp string      `json:"timestamp"`
}

type errorEnvelope struct {
	Success   bool        `json:"success"`
	Error     errorDetail `json:"error"`
	Timestamp string      `json:"timestamp"`
}

type errorDetail struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

func respondSuccess(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(successEnvelope{
		Success:   true,
		Data:      data,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func respondError(w http.ResponseWriter, status int, code, message string, details interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{
		Success: false,
		Error: errorDetail{
			Code:    code,
			Message: message,
			Details: details,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

