package renewal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Handler provides HTTP endpoints to inspect and manage automatic lease renewal workers.
type Handler struct {
	manager *Manager
}

// NewHandler constructs a new renewal Handler.
func NewHandler(m *Manager) *Handler {
	return &Handler{manager: m}
}

// RegisterRoutes registers renewal endpoints on the provided ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/renewal/start", h.handleStart)
	mux.HandleFunc("/v1/renewal/stop", h.handleStop)
	mux.HandleFunc("/v1/renewal/status", h.handleStatus)
}

type startRenewalRequest struct {
	ResourceName      string `json:"resource_name"`
	OwnerID           string `json:"owner_id"`
	ClientID          string `json:"client_id"`
	SessionID         string `json:"session_id"`
	FencingToken      uint64 `json:"fencing_token"`
	LeaseVersion      uint64 `json:"lease_version"`
	ExpiresAtUnixNano int64  `json:"expires_at_unix_nano"`
	TTLSeconds        int64  `json:"ttl_seconds,omitempty"`
	TTLNanoseconds    int64  `json:"ttl_nanoseconds,omitempty"`
}

type stopRenewalRequest struct {
	ResourceName string `json:"resource_name"`
	SessionID    string `json:"session_id,omitempty"`
}

func (h *Handler) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	var req startRenewalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", fmt.Sprintf("invalid json: %v", err), nil)
		return
	}

	ttl := time.Duration(req.TTLSeconds) * time.Second
	if req.TTLNanoseconds > 0 {
		ttl = time.Duration(req.TTLNanoseconds)
	}

	err := h.manager.StartRenewal(
		req.ResourceName, req.OwnerID, req.ClientID, req.SessionID,
		req.FencingToken, req.LeaseVersion, req.ExpiresAtUnixNano, ttl,
	)
	if err != nil {
		if err == ErrUnauthorizedSession || err == ErrNotLockOwner {
			respondError(w, http.StatusForbidden, "ERR_FORBIDDEN", err.Error(), nil)
			return
		}
		if err == ErrStaleWorkerGeneration || err == ErrFencingTokenMismatch {
			respondError(w, http.StatusConflict, "ERR_CONFLICT", err.Error(), nil)
			return
		}
		if err == ErrLockNotFound {
			respondError(w, http.StatusNotFound, "ERR_NOT_FOUND", err.Error(), nil)
			return
		}
		if err == ErrLeaseExpired {
			respondError(w, http.StatusGone, "ERR_LEASE_EXPIRED", err.Error(), nil)
			return
		}
		respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", err.Error(), nil)
		return
	}

	st, _ := h.manager.GetStatus(req.ResourceName)
	respondSuccess(w, http.StatusOK, st)
}

func (h *Handler) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	var req stopRenewalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", fmt.Sprintf("invalid json: %v", err), nil)
		return
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = r.Header.Get("X-Session-ID")
	}
	if sessionID == "" {
		sessionID = r.URL.Query().Get("session_id")
	}

	err := h.manager.StopRenewal(req.ResourceName, sessionID)
	if err != nil {
		if err == ErrSessionMismatch {
			respondError(w, http.StatusForbidden, "ERR_FORBIDDEN", err.Error(), nil)
			return
		}
		if err == ErrWorkerNotFound {
			respondError(w, http.StatusNotFound, "ERR_NOT_FOUND", err.Error(), nil)
			return
		}
		respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", err.Error(), nil)
		return
	}

	respondSuccess(w, http.StatusOK, map[string]interface{}{
		"resource_name": req.ResourceName,
		"status":        "stopped",
	})
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	res := strings.TrimSpace(r.URL.Query().Get("resource"))
	if res != "" {
		st, found := h.manager.GetStatus(res)
		if !found {
			respondError(w, http.StatusNotFound, "ERR_NOT_FOUND", "renewal worker not found", nil)
			return
		}
		respondSuccess(w, http.StatusOK, st)
		return
	}

	statuses := h.manager.ListStatus()
	respondSuccess(w, http.StatusOK, map[string]interface{}{
		"renewals": statuses,
		"count":    len(statuses),
	})
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
