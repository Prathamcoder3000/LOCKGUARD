package heartbeat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Handler provides HTTP endpoints for client registration and heartbeat ping operations.
type Handler struct {
	manager *Manager
}

// NewHandler constructs a new Handler.
func NewHandler(m *Manager) *Handler {
	return &Handler{manager: m}
}

// RegisterRoutes registers client endpoints on the provided ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/clients/register", h.handleRegister)
	mux.HandleFunc("/v1/clients/heartbeat", h.handleHeartbeat)
	mux.HandleFunc("/v1/clients", h.handleClients)
	mux.HandleFunc("/v1/clients/", h.handleClientByID)
}

type registerRequest struct {
	ClientID       string `json:"client_id"`
	OwnerID        string `json:"owner_id"`
	TTLSeconds     int64  `json:"ttl_seconds,omitempty"`
	TTLNanoseconds int64  `json:"ttl_nanoseconds,omitempty"`
}

type heartbeatRequest struct {
	ClientID  string `json:"client_id"`
	SessionID string `json:"session_id"`
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", fmt.Sprintf("invalid json: %v", err), nil)
		return
	}

	ttl := time.Duration(req.TTLSeconds) * time.Second
	if req.TTLNanoseconds > 0 {
		ttl = time.Duration(req.TTLNanoseconds)
	}
	sess, err := h.manager.Register(req.ClientID, req.OwnerID, ttl)
	if err != nil {
		if err == ErrOwnerMismatch || err == ErrSessionAlreadyActive {
			respondError(w, http.StatusConflict, "ERR_CONFLICT", err.Error(), nil)
			return
		}
		respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", err.Error(), nil)
		return
	}

	respondSuccess(w, http.StatusOK, sess.Snapshot())
}

func (h *Handler) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	var req heartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", fmt.Sprintf("invalid json: %v", err), nil)
		return
	}

	sess, err := h.manager.Heartbeat(req.ClientID, req.SessionID)
	if err != nil {
		if err == ErrSessionNotFound {
			respondError(w, http.StatusNotFound, "ERR_NOT_FOUND", err.Error(), nil)
			return
		}
		if err == ErrSessionExpired {
			respondError(w, http.StatusPreconditionFailed, "ERR_LEASE_EXPIRED", err.Error(), nil)
			return
		}
		if err == ErrSessionMismatch {
			respondError(w, http.StatusForbidden, "ERR_FORBIDDEN", err.Error(), nil)
			return
		}
		respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", err.Error(), nil)
		return
	}

	respondSuccess(w, http.StatusOK, sess.Snapshot())
}

func (h *Handler) handleClients(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	authSessionID := r.Header.Get("X-Session-ID")
	sessions := h.manager.ListSessions()
	sanitized := make([]SessionSnapshot, len(sessions))
	for i, s := range sessions {
		sanitized[i] = sanitizeSessionSnapshot(s, authSessionID)
	}

	respondSuccess(w, http.StatusOK, map[string]interface{}{
		"sessions": sanitized,
		"count":    len(sanitized),
	})
}

func (h *Handler) handleClientByID(w http.ResponseWriter, r *http.Request) {
	clientID := strings.TrimPrefix(r.URL.Path, "/v1/clients/")
	clientID = strings.TrimSpace(clientID)

	if clientID == "" {
		h.handleClients(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		sess, found := h.manager.GetSession(clientID)
		if !found {
			respondError(w, http.StatusNotFound, "ERR_NOT_FOUND", "client session not found", nil)
			return
		}
		authSessionID := r.Header.Get("X-Session-ID")
		respondSuccess(w, http.StatusOK, sanitizeSessionSnapshot(sess.Snapshot(), authSessionID))

	case http.MethodDelete:
		sessionID := r.Header.Get("X-Session-ID")
		if sessionID == "" {
			sessionID = r.URL.Query().Get("session_id")
		}
		if sessionID == "" {
			var body struct {
				SessionID string `json:"session_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			sessionID = body.SessionID
		}

		err := h.manager.CloseSession(clientID, sessionID)
		if err != nil {
			if err == ErrSessionNotFound {
				respondError(w, http.StatusNotFound, "ERR_NOT_FOUND", err.Error(), nil)
				return
			}
			if err == ErrSessionMismatch {
				respondError(w, http.StatusForbidden, "ERR_FORBIDDEN", err.Error(), nil)
				return
			}
			respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", err.Error(), nil)
			return
		}

		respondSuccess(w, http.StatusOK, map[string]interface{}{
			"client_id": clientID,
			"status":    "closed",
		})

	default:
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed", nil)
	}
}

type successEnvelope struct {
	Success   bool        `json:"success"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp string      `json:"timestamp"`
}

type errorDetail struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

type errorEnvelope struct {
	Success   bool        `json:"success"`
	Error     errorDetail `json:"error"`
	Timestamp string      `json:"timestamp"`
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

func sanitizeSessionSnapshot(s SessionSnapshot, authSessionID string) SessionSnapshot {
	if authSessionID != "" && s.SessionID == authSessionID {
		return s
	}
	s.SessionID = "[REDACTED]"
	return s
}
