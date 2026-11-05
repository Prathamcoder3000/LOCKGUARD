package api

import (
	"net/http"
	"os"
	"time"

	"lockguard/backend/internal/config"
)

// HealthResponse represents liveness status.
type HealthResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Version   string `json:"version"`
	NodeID    string `json:"node_id,omitempty"`
	Timestamp string `json:"timestamp"`
}

// ReadinessResponse represents readiness status across critical subsystems.
type ReadinessResponse struct {
	Status     string            `json:"status"`
	Service    string            `json:"service"`
	NodeID     string            `json:"node_id,omitempty"`
	Components map[string]string `json:"components"`
	Timestamp  string            `json:"timestamp"`
}

// HealthHandler returns basic liveness information.
func HealthHandler(serviceName, version, nodeID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			RespondError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed", nil)
			return
		}

		resp := HealthResponse{
			Status:    "ok",
			Service:   serviceName,
			Version:   version,
			NodeID:    nodeID,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		}
		RespondSuccess(w, http.StatusOK, resp)
	}
}

// ReadinessHandler checks subsystem readiness (storage accessibility, configuration validity).
func ReadinessHandler(cfg *config.Config, serviceName, nodeID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			RespondError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed", nil)
			return
		}

		components := make(map[string]string)
		allReady := true

		// Check Config state
		if cfg != nil {
			components["config"] = "ready"
		} else {
			components["config"] = "uninitialized"
			allReady = false
		}

		// Check Storage Directory accessibility
		if cfg != nil && cfg.Storage.DataDir != "" {
			if _, err := os.Stat(cfg.Storage.DataDir); err == nil {
				components["storage"] = "ready"
			} else if os.IsNotExist(err) {
				// Attempt creation or mark as initialized
				if err := os.MkdirAll(cfg.Storage.DataDir, 0750); err == nil {
					components["storage"] = "ready"
				} else {
					components["storage"] = "error: " + err.Error()
					allReady = false
				}
			} else {
				components["storage"] = "inaccessible"
				allReady = false
			}
		} else {
			components["storage"] = "not_configured"
		}

		// Database readiness state
		components["database"] = "standby_phase0"

		statusStr := "ready"
		httpStatus := http.StatusOK
		if !allReady {
			statusStr = "not_ready"
			httpStatus = http.StatusServiceUnavailable
		}

		resp := ReadinessResponse{
			Status:     statusStr,
			Service:    serviceName,
			NodeID:     nodeID,
			Components: components,
			Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		}

		if allReady {
			RespondSuccess(w, httpStatus, resp)
		} else {
			RespondError(w, httpStatus, ErrCodeNotReady, "service is not ready", resp)
		}
	}
}

