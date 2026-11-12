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
	"lockguard/backend/internal/metrics"
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

	// Setup coordinator HTTP handlers and metrics
	coordMetrics := metrics.NewRegistry()
	coordHandler := coordinator.NewHandlerWithMetrics(raftNode, cfg.Raft.NodeID, coordMetrics)
	mux := http.NewServeMux()
	coordHandler.RegisterRoutes(mux)
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_, leaderID := raftNode.Leader()
		coordMetrics.SetQuorumAvailable(leaderID != "" && !raftNode.IsPartitioned())
		if rCfg, err := raftNode.GetConfiguration(); err == nil {
			coordMetrics.SetClusterNodesAvailable(len(rCfg.Servers))
		}
		coordMetrics.IncHTTPRequest(r.Method, "2xx")
		coordMetrics.Handler()(w, r)
	})

	monitorCtx, monitorCancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()

		var lastLeader string
		var quorumLostAt time.Time
		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-ticker.C:
				_, leaderID := raftNode.Leader()
				curLeader := string(leaderID)
				partitioned := raftNode.IsPartitioned()

				if partitioned || curLeader == "" {
					coordMetrics.SetQuorumAvailable(false)
					if lastLeader != "" && quorumLostAt.IsZero() {
						quorumLostAt = time.Now()
					}
					continue
				}

				coordMetrics.SetQuorumAvailable(true)
				if lastLeader == "" {
					lastLeader = curLeader
					if !quorumLostAt.IsZero() {
						coordMetrics.SetRecoveryDuration("leader_election", time.Since(quorumLostAt).Seconds())
						quorumLostAt = time.Time{}
					}
					continue
				}
				if curLeader != lastLeader {
					coordMetrics.IncLeaderChanges()
					if !quorumLostAt.IsZero() {
						coordMetrics.SetRecoveryDuration("leader_election", time.Since(quorumLostAt).Seconds())
						quorumLostAt = time.Time{}
					}
					lastLeader = curLeader
				}
			}
		}
	}()

	// Leader-side fallback lease reaper ensures Raft FSM converges even if the API gateway is offline.
	// Uses a short grace period past ExpiresAtUnixNano so the API gateway reaper runs first and emits LOCK_EXPIRE audit rows.
	go func() {
		reapTicker := time.NewTicker(1 * time.Second)
		defer reapTicker.Stop()

		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-reapTicker.C:
				if !raftNode.IsLeader() || raftNode.IsPartitioned() {
					continue
				}
				nowNs := time.Now().UnixNano()
				cutoffNs := nowNs - int64(2*time.Second)
				expired := raftNode.ListExpiredLocksAt(cutoffNs)
				for _, rec := range expired {
					if monitorCtx.Err() != nil {
						return
					}
					cmd := lockstate.ExpireLockCommand{
						RequestID:                 fmt.Sprintf("coord-reap-%s-%d-%d", rec.ResourceName, rec.FencingToken, rec.LeaseVersion),
						ResourceName:              rec.ResourceName,
						ExpectedOwnerID:           rec.OwnerID,
						ExpectedFencingToken:      rec.FencingToken,
						ExpectedLeaseVersion:      rec.LeaseVersion,
						ExpectedExpiresAtUnixNano: rec.ExpiresAtUnixNano,
						TimestampUnixNano:         nowNs,
					}
					reapCtx, cancel := context.WithTimeout(monitorCtx, 2*time.Second)
					_, _ = raftNode.ApplyCommand(reapCtx, cmd)
					cancel()
				}
			}
		}
	}()

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
	monitorCancel()
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
