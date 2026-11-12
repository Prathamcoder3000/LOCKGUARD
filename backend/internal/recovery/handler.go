package recovery

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Handler serves HTTP endpoints for system diagnostics, cluster recovery reports,
// controlled chaos drills, network partition injection, and Raft snapshot recovery.
type Handler struct {
	diagnostics *DiagnosticsService
}

// NewHandler constructs a new recovery HTTP Handler.
func NewHandler(diagnostics *DiagnosticsService) *Handler {
	return &Handler{
		diagnostics: diagnostics,
	}
}

// RegisterRoutes registers all recovery endpoints without auth middleware (unit/test convenience).
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	h.RegisterRoutesWithAuth(mux, nil)
}

// RegisterRoutesWithAuth registers recovery read and admin-gated mutation endpoints on an http.ServeMux.
func (h *Handler) RegisterRoutesWithAuth(mux *http.ServeMux, adminWrap func(http.Handler) http.Handler) {
	wrapAdmin := func(fn http.HandlerFunc) http.Handler {
		if adminWrap != nil {
			return adminWrap(fn)
		}
		return fn
	}

	mux.HandleFunc("/v1/recovery/diagnostics", h.handleDiagnostics)
	mux.HandleFunc("/v1/recovery/cluster", h.handleCluster)
	mux.HandleFunc("/v1/recovery/snapshots", h.handleSnapshots)
	mux.HandleFunc("/v1/recovery/health", h.handleHealth)
	mux.HandleFunc("/v1/recovery/chaos", h.handleChaosStatus)

	mux.Handle("/v1/recovery/chaos/run", wrapAdmin(h.handleChaosRun))
	mux.Handle("/v1/recovery/partition/inject", wrapAdmin(h.handlePartitionInject))
	mux.Handle("/v1/recovery/partition/heal", wrapAdmin(h.handlePartitionHeal))
	mux.Handle("/v1/recovery/snapshots/create", wrapAdmin(h.handleSnapshotCreate))
	mux.Handle("/v1/recovery/snapshots/restore", wrapAdmin(h.handleSnapshotRestore))

	// Aliases under /api/v1/
	mux.HandleFunc("/api/v1/recovery/diagnostics", h.handleDiagnostics)
	mux.HandleFunc("/api/v1/recovery/cluster", h.handleCluster)
	mux.HandleFunc("/api/v1/recovery/snapshots", h.handleSnapshots)
	mux.HandleFunc("/api/v1/recovery/health", h.handleHealth)
	mux.HandleFunc("/api/v1/recovery/chaos", h.handleChaosStatus)

	mux.Handle("/api/v1/recovery/chaos/run", wrapAdmin(h.handleChaosRun))
	mux.Handle("/api/v1/recovery/partition/inject", wrapAdmin(h.handlePartitionInject))
	mux.Handle("/api/v1/recovery/partition/heal", wrapAdmin(h.handlePartitionHeal))
	mux.Handle("/api/v1/recovery/snapshots/create", wrapAdmin(h.handleSnapshotCreate))
	mux.Handle("/api/v1/recovery/snapshots/restore", wrapAdmin(h.handleSnapshotRestore))
}

func (h *Handler) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	report := h.diagnostics.CollectReport(r.Context())
	status := http.StatusOK
	if report.OverallStatus == StatusUnhealthy {
		status = http.StatusServiceUnavailable
	}

	respondSuccess(w, status, report)
}

func (h *Handler) handleCluster(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	report := h.diagnostics.CollectClusterReport(r.Context())
	respondSuccess(w, http.StatusOK, report)
}

func (h *Handler) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	report := h.diagnostics.CollectSnapshotReport(r.Context())
	respondSuccess(w, http.StatusOK, report)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	report := h.diagnostics.CollectReport(r.Context())
	statusCode := http.StatusOK
	if report.OverallStatus == StatusUnhealthy {
		// Quorum loss or critical failure: fail closed with 503
		statusCode = http.StatusServiceUnavailable
	}

	respondSuccess(w, statusCode, map[string]interface{}{
		"status":                report.OverallStatus,
		"raft_quorum_available": report.RaftQuorumAvailable,
		"leader_available":      report.LeaderAvailable,
		"database_available":    report.DatabaseAvailable,
		"node_id":               report.NodeID,
		"timestamp":             report.Timestamp,
	})
}

func (h *Handler) handleChaosStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	report := h.diagnostics.GetChaosStatus(r.Context())
	respondSuccess(w, http.StatusOK, report)
}

func (h *Handler) handleChaosRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	var req struct {
		Scenario string `json:"scenario"`
		Confirm  bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", "invalid JSON request body")
		return
	}
	res, err := h.diagnostics.RunChaosTest(r.Context(), req.Scenario, req.Confirm)
	if err != nil {
		h.respondRecoveryError(w, err, res)
		return
	}
	respondSuccess(w, http.StatusOK, res)
}

func (h *Handler) handlePartitionInject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	var req struct {
		TargetNodeID    string `json:"target_node_id"`
		Confirm         bool   `json:"confirm"`
		DurationSeconds int    `json:"duration_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", "invalid JSON request body")
		return
	}
	res, err := h.diagnostics.InjectPartition(r.Context(), req.TargetNodeID, req.Confirm, req.DurationSeconds)
	if err != nil {
		h.respondRecoveryError(w, err, nil)
		return
	}
	respondSuccess(w, http.StatusOK, res)
}

func (h *Handler) handlePartitionHeal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	var req struct {
		TargetNodeID string `json:"target_node_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	res, err := h.diagnostics.HealPartition(r.Context(), req.TargetNodeID)
	if err != nil {
		h.respondRecoveryError(w, err, nil)
		return
	}
	respondSuccess(w, http.StatusOK, res)
}

func (h *Handler) handleSnapshotCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	res, err := h.diagnostics.CreateClusterSnapshot(r.Context())
	if err != nil {
		h.respondRecoveryError(w, err, nil)
		return
	}
	respondSuccess(w, http.StatusOK, res)
}

func (h *Handler) handleSnapshotRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	var req struct {
		SnapshotID string `json:"snapshot_id"`
		Confirm    bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", "invalid JSON request body")
		return
	}
	res, err := h.diagnostics.RestoreClusterSnapshot(r.Context(), req.SnapshotID, req.Confirm)
	if err != nil {
		h.respondRecoveryError(w, err, nil)
		return
	}
	respondSuccess(w, http.StatusOK, res)
}

func (h *Handler) respondRecoveryError(w http.ResponseWriter, err error, details interface{}) {
	status := http.StatusInternalServerError
	code := "ERR_RECOVERY_FAILED"
	switch {
	case errors.Is(err, ErrConfirmationRequired):
		status = http.StatusBadRequest
		code = "ERR_CONFIRMATION_REQUIRED"
	case errors.Is(err, ErrInvalidRequest):
		status = http.StatusBadRequest
		code = "ERR_BAD_REQUEST"
	case errors.Is(err, ErrForbiddenInProd):
		status = http.StatusForbidden
		code = "ERR_FORBIDDEN"
	case errors.Is(err, ErrTargetNotFound):
		status = http.StatusNotFound
		code = "ERR_NOT_FOUND"
	case errors.Is(err, ErrQuorumSafetyViolation):
		status = http.StatusConflict
		code = "ERR_QUORUM_SAFETY_VIOLATION"
	case errors.Is(err, ErrConflictState):
		status = http.StatusConflict
		code = "ERR_CONFLICT"
	case errors.Is(err, ErrNoQuorumAvailable):
		status = http.StatusServiceUnavailable
		code = "ERR_QUORUM_UNAVAILABLE"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	payload := map[string]interface{}{
		"success": false,
		"error": map[string]interface{}{
			"code":    code,
			"message": err.Error(),
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if details != nil {
		payload["data"] = details
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func respondSuccess(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   statusCode < 400,
		"data":      data,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func respondError(w http.ResponseWriter, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

