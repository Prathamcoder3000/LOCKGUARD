package recovery

import (
	"encoding/json"
	"net/http"
	"time"
)

// Handler serves HTTP endpoints for system diagnostics, cluster recovery reports, and snapshot status.
type Handler struct {
	diagnostics *DiagnosticsService
}

// NewHandler constructs a new recovery HTTP Handler.
func NewHandler(diagnostics *DiagnosticsService) *Handler {
	return &Handler{
		diagnostics: diagnostics,
	}
}

// RegisterRoutes registers the recovery endpoints on an http.ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/recovery/diagnostics", h.handleDiagnostics)
	mux.HandleFunc("/v1/recovery/cluster", h.handleCluster)
	mux.HandleFunc("/v1/recovery/snapshots", h.handleSnapshots)
	mux.HandleFunc("/v1/recovery/health", h.handleHealth)

	// Aliases under /api/v1/
	mux.HandleFunc("/api/v1/recovery/diagnostics", h.handleDiagnostics)
	mux.HandleFunc("/api/v1/recovery/cluster", h.handleCluster)
	mux.HandleFunc("/api/v1/recovery/snapshots", h.handleSnapshots)
	mux.HandleFunc("/api/v1/recovery/health", h.handleHealth)
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

