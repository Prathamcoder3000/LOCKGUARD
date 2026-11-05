package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lockguard/backend/internal/config"
	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/coordinator"
	"lockguard/backend/internal/lockstate"
	"lockguard/backend/internal/middleware"
)

const (
	ServiceName = "lockguard-coordinator"
	Version     = "0.2.0"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		slog.Error("coordinator_config_error", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Initialize structured logging
	var handler slog.Handler
	logLevel := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: logLevel}
	if cfg.LogFormat == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	logger := slog.New(handler)

	logger.Info("coordinator_starting",
		slog.String("service", ServiceName),
		slog.String("version", Version),
		slog.String("node_id", cfg.Raft.NodeID),
		slog.String("raft_bind", cfg.Raft.BindAddr),
		slog.String("raft_advertise", cfg.Raft.AdvertiseAddr),
		slog.Int("api_port", cfg.Raft.CoordinatorAPIPort),
	)

	// Ensure data directory exists
	if err := os.MkdirAll(cfg.Storage.DataDir, 0750); err != nil {
		logger.Error("failed_to_create_data_dir", slog.String("dir", cfg.Storage.DataDir), slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Initialize deterministic in-memory lock state machine
	sm := lockstate.New()

	// Determine if this node should execute bootstrap when storage is empty
	bootstrapLeader := os.Getenv("BOOTSTRAP_LEADER")
	if bootstrapLeader == "" {
		bootstrapLeader = "coordinator-a"
	}
	shouldBootstrap := (cfg.Raft.NodeID == bootstrapLeader || os.Getenv("BOOTSTRAP") == "true")

	// Initialize HashiCorp Raft consensus node
	raftCfg := consensus.NodeConfig{
		NodeID:            cfg.Raft.NodeID,
		BindAddr:          cfg.Raft.BindAddr,
		AdvertiseAddr:     cfg.Raft.AdvertiseAddr,
		DataDir:           cfg.Storage.DataDir,
		ClusterNodes:      cfg.Raft.ClusterNodes,
		HeartbeatTimeout:  cfg.Raft.HeartbeatTimeout,
		ElectionTimeout:   cfg.Raft.ElectionTimeout,
		CommitTimeout:     cfg.Raft.CommitTimeout,
		SnapshotInterval:  cfg.Raft.SnapshotIntervalSec,
		SnapshotThreshold: cfg.Raft.SnapshotThreshold,
		RetainSnapshots:   2,
		Inmem:             false,
		LogLevel:          cfg.LogLevel,
		Logger:            logger,
		BootstrapCluster:  shouldBootstrap,
	}

	raftNode, err := consensus.NewRaftNode(raftCfg, sm)
	if err != nil {
		logger.Error("failed_to_initialize_raft_node", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Setup coordinator HTTP handlers
	coordHandler := coordinator.NewHandler(raftNode, cfg.Raft.NodeID)
	mux := http.NewServeMux()
	coordHandler.RegisterRoutes(mux)

	var httpHandler http.Handler = mux
	httpHandler = middleware.RequestLogger(logger)(httpHandler)
	httpHandler = middleware.CORS(httpHandler)
	httpHandler = middleware.Recovery(logger)(httpHandler)

	addr := fmt.Sprintf("%s:%d", cfg.Raft.CoordinatorAPIHost, cfg.Raft.CoordinatorAPIPort)
	srv := &http.Server{
		Addr:         addr,
		Handler:      httpHandler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Info("coordinator_listening", slog.String("address", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("coordinator_listen_failure", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	sig := <-stopChan
	logger.Info("coordinator_stopping", slog.String("signal", sig.String()))

	ctx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("coordinator_shutdown_error", slog.String("error", err.Error()))
	}

	if err := raftNode.Close(); err != nil {
		logger.Error("raft_node_close_error", slog.String("error", err.Error()))
	}

	logger.Info("coordinator_stopped", slog.String("node_id", cfg.Raft.NodeID))
}
