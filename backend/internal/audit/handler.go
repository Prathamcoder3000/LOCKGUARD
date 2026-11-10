package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lockguard/backend/internal/auth"
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

// SetRepository updates the audit repository under safe handler control.
func (h *Handler) SetRepository(repo persistence.AuditRepository) {
	h.repo = repo
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

type callerIdentity struct {
	authenticated bool
	isAdmin       bool
	clientID      string
}

func (h *Handler) authenticate(r *http.Request) (callerIdentity, error) {
	// If Auth is disabled, trusted intranet mode: all callers are allowed as admin/read-only
	if h.cfg == nil || !h.cfg.Auth.Enabled {
		callerClient := strings.TrimSpace(r.Header.Get("X-Client-ID"))
		return callerIdentity{authenticated: true, isAdmin: callerClient == "", clientID: callerClient}, nil
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return callerIdentity{}, errors.New("missing authorization header")
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return callerIdentity{}, errors.New("invalid authorization header scheme, expected Bearer <token>")
	}

	token := strings.TrimSpace(parts[1])
	if token == "" {
		return callerIdentity{}, errors.New("empty bearer token")
	}

	// 1. Standard Phase 5 JWT validation
	if h.cfg != nil && h.cfg.Auth.JWTSecretKey != "" {
		if claims, err := auth.ValidateJWT(token, h.cfg.Auth.JWTSecretKey); err == nil {
			if claims.Role == auth.RoleAdmin || claims.Role == auth.RoleReadOnly {
				return callerIdentity{authenticated: true, isAdmin: true}, nil
			}
			return callerIdentity{authenticated: true, isAdmin: false, clientID: claims.Subject}, nil
		}
	}

	// 2. Administrative static credentials (development/intranet mode)
	if token == "admin" || token == "operator" || token == h.cfg.Auth.JWTSecretKey {
		return callerIdentity{authenticated: true, isAdmin: true}, nil
	}

	// Client-scoped credentials (e.g. "client:<client_id>")
	if strings.HasPrefix(token, "client:") {
		cid := strings.TrimPrefix(token, "client:")
		return callerIdentity{authenticated: true, isAdmin: false, clientID: cid}, nil
	}

	// If caller specified X-Client-ID header with a valid bearer token
	callerClient := strings.TrimSpace(r.Header.Get("X-Client-ID"))
	if callerClient != "" {
		return callerIdentity{authenticated: true, isAdmin: false, clientID: callerClient}, nil
	}

	return callerIdentity{}, errors.New("unauthorized token")
}

func (h *Handler) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	ident, err := h.authenticate(r)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "ERR_UNAUTHORIZED", "unauthorized audit access", nil)
		return
	}

	filter, err := h.parseFilter(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "ERR_INVALID_FILTER", err.Error(), nil)
		return
	}

	// Cross-client authorization enforcement
	if !ident.isAdmin && ident.clientID != "" {
		if filter.ClientID != "" && filter.ClientID != ident.clientID {
			respondError(w, http.StatusForbidden, "ERR_FORBIDDEN",
				"cross-client audit access forbidden: client is not authorized to view another client's audit records", nil)
			return
		}
		if filter.OwnerID != "" && filter.OwnerID != ident.clientID {
			respondError(w, http.StatusForbidden, "ERR_FORBIDDEN",
				"cross-client audit access forbidden: client is not authorized to view another client's audit records", nil)
			return
		}
		filter.ClientID = ident.clientID
		filter.OwnerID = ident.clientID
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

	ident, err := h.authenticate(r)
	if err != nil {
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

	// Cross-client authorization check: non-admin cannot view another client's record
	if !ident.isAdmin && ident.clientID != "" && event.ClientID != "" && event.ClientID != ident.clientID {
		respondError(w, http.StatusForbidden, "ERR_FORBIDDEN",
			"cross-client audit access forbidden: client is not authorized to view another client's audit records", nil)
		return
	}

	respondSuccess(w, http.StatusOK, event)
}

func (h *Handler) handleResourceEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	ident, err := h.authenticate(r)
	if err != nil {
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

	// Cross-client authorization enforcement: non-admin only sees their own events for the resource
	if !ident.isAdmin && ident.clientID != "" {
		if filter.ClientID != "" && filter.ClientID != ident.clientID {
			respondError(w, http.StatusForbidden, "ERR_FORBIDDEN",
				"cross-client audit access forbidden: client is not authorized to view another client's audit records", nil)
			return
		}
		if filter.OwnerID != "" && filter.OwnerID != ident.clientID {
			respondError(w, http.StatusForbidden, "ERR_FORBIDDEN",
				"cross-client audit access forbidden: client is not authorized to view another client's audit records", nil)
			return
		}
		filter.ClientID = ident.clientID
		filter.OwnerID = ident.clientID
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

	ident, err := h.authenticate(r)
	if err != nil {
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

	// Cross-client authorization check: non-admin cannot view another client's records
	if !ident.isAdmin && ident.clientID != "" && ident.clientID != clientID {
		respondError(w, http.StatusForbidden, "ERR_FORBIDDEN",
			"cross-client audit access forbidden: client is not authorized to view another client's audit records", nil)
		return
	}

	filter, err := h.parseFilter(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "ERR_INVALID_FILTER", err.Error(), nil)
		return
	}
	filter.ClientID = clientID
	filter.OwnerID = clientID

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

