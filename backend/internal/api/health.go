package api

import (
	"context"
	"net/http"
	"os"
	"time"

	"lockguard/backend/internal/config"
)

// LeaderChecker defines an interface to query cluster leader accessibility.
type LeaderChecker interface {
	GetLeader(ctx context.Context) (string, error)
}

// DatabaseChecker defines an interface to verify PostgreSQL connectivity.
type DatabaseChecker interface {
	Ping(ctx context.Context) error
}

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

// ReadinessHandler checks subsystem readiness (storage accessibility, configuration validity, coordinator leader reachability).
func ReadinessHandler(cfg *config.Config, serviceName, nodeID string, checkers ...LeaderChecker) http.HandlerFunc {
	return ReadinessHandlerWithDB(cfg, serviceName, nodeID, nil, checkers...)
}

// ReadinessHandlerWithDB checks subsystem readiness including active PostgreSQL connectivity.
func ReadinessHandlerWithDB(cfg *config.Config, serviceName, nodeID string, db DatabaseChecker, checkers ...LeaderChecker) http.HandlerFunc {
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
		if db != nil {
			pingCtx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
			err := db.Ping(pingCtx)
			cancel()
			if err != nil {
				components["database"] = "unavailable: " + err.Error()
				allReady = false
			} else {
				components["database"] = "ready"
			}
		} else {
			components["database"] = "not_configured"
		}

		// Check Coordinator Raft Leader Reachability if checker provided
		if len(checkers) > 0 && checkers[0] != nil {
			leaderURL, err := checkers[0].GetLeader(r.Context())
			if err != nil || leaderURL == "" {
				components["coordinator_cluster"] = "leader_unavailable"
				allReady = false
			} else {
				components["coordinator_cluster"] = "ready"
				components["leader"] = leaderURL
			}
		}

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

