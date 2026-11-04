package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"lockguard/backend/internal/config"
	"lockguard/backend/internal/middleware"
)

// Server encapsulates the HTTP server, configuration, and logging for LockGuard.
type Server struct {
	cfg         *config.Config
	logger      *slog.Logger
	httpServer  *http.Server
	mux         *http.ServeMux
	serviceName string
	version     string
}

// ClusterStatusResponse outlines cluster topology and quorum expectations for Phase 0.
type ClusterStatusResponse struct {
	Service         string            `json:"service"`
	NodeID          string            `json:"node_id"`
	Environment     string            `json:"environment"`
	RaftCluster     []string          `json:"raft_cluster"`
	QuorumRule      string            `json:"quorum_rule"`
	DesignPrinciple string            `json:"design_principle"`
	StoragePath     string            `json:"storage_path"`
	Subsystems      map[string]string `json:"subsystems"`
	Timestamp       string            `json:"timestamp"`
}

// NewServer initializes a new Server instance with configured routes and middleware.
func NewServer(cfg *config.Config, logger *slog.Logger, serviceName, version string) *Server {
	mux := http.NewServeMux()

	s := &Server{
		cfg:         cfg,
		logger:      logger,
		mux:         mux,
		serviceName: serviceName,
		version:     version,
	}

	s.registerRoutes()

	// Apply middleware stack: Recovery -> CORS -> Logging
	var handler http.Handler = s.mux
	handler = middleware.RequestLogger(logger)(handler)
	handler = middleware.CORS(handler)
	handler = middleware.Recovery(logger)(handler)

	addr := fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port)
	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
		IdleTimeout:  cfg.HTTP.IdleTimeout,
	}

	return s
}

func (s *Server) registerRoutes() {
	// Top-level Health & Readiness probes
	s.mux.HandleFunc("/health", HealthHandler(s.serviceName, s.version, s.cfg.Raft.NodeID))
	s.mux.HandleFunc("/ready", ReadinessHandler(s.cfg, s.serviceName, s.cfg.Raft.NodeID))

	// Versioned API routes under /api/v1/
	s.mux.HandleFunc("/api/v1/health", HealthHandler(s.serviceName, s.version, s.cfg.Raft.NodeID))
	s.mux.HandleFunc("/api/v1/ready", ReadinessHandler(s.cfg, s.serviceName, s.cfg.Raft.NodeID))

	// Cluster topology and invariant status
	s.mux.HandleFunc("/api/v1/cluster/status", s.handleClusterStatus)
}

func (s *Server) handleClusterStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		RespondError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}

	subsystems := map[string]string{
		"raft_consensus":     "initialized_phase0",
		"state_machine":      "pending_phase1",
		"lease_manager":      "pending_phase2",
		"fencing_manager":    "pending_phase2",
		"snapshot_manager":   "pending_phase3",
		"protected_resource": "pending_phase4",
		"benchmark_module":   "pending_phase5",
	}

	status := ClusterStatusResponse{
		Service:         s.serviceName,
		NodeID:          s.cfg.Raft.NodeID,
		Environment:     s.cfg.Environment,
		RaftCluster:     s.cfg.Raft.ClusterNodes,
		QuorumRule:      "2 of 3 nodes constitute quorum (Fail Closed)",
		DesignPrinciple: "CONSISTENCY FIRST — FAIL CLOSED",
		StoragePath:     s.cfg.Storage.DataDir,
		Subsystems:      subsystems,
		Timestamp:       time.Now().UTC().Format(time.RFC3339Nano),
	}

	RespondSuccess(w, http.StatusOK, status)
}

// Start begins listening on the configured address.
func (s *Server) Start() error {
	s.logger.Info("server_starting",
		slog.String("service", s.serviceName),
		slog.String("address", s.httpServer.Addr),
		slog.String("node_id", s.cfg.Raft.NodeID),
		slog.String("environment", s.cfg.Environment),
	)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server failure: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the HTTP server within the provided context deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("server_shutting_down", slog.String("service", s.serviceName))
	return s.httpServer.Shutdown(ctx)
}

// Handler returns the underlying http.Handler (used for testing).
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}
