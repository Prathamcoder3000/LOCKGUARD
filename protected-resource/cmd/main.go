package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"lockguard/protected-resource/internal"
)

const (
	ServiceName = "lockguard-protected-resource"
	Version     = "0.1.0"
)

type WriteRequest struct {
	ResourceName string `json:"resource_name"`
	FencingToken uint64 `json:"fencing_token"`
	OwnerID      string `json:"owner_id"`
	Payload      string `json:"payload"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	port := 9000
	if pStr := os.Getenv("PROTECTED_RESOURCE_PORT"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil {
			port = p
		}
	}

	guard := internal.NewFencingGuard()
	var writesAccepted atomic.Uint64
	var writesRejected atomic.Uint64
	mux := http.NewServeMux()

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprintf(w, "# HELP lockguard_protected_resource_up Protected resource service liveness status.\n")
		fmt.Fprintf(w, "# TYPE lockguard_protected_resource_up gauge\n")
		fmt.Fprintf(w, "lockguard_protected_resource_up 1\n")
		fmt.Fprintf(w, "# HELP lockguard_protected_resource_writes_accepted_total Total writes accepted with valid fencing tokens.\n")
		fmt.Fprintf(w, "# TYPE lockguard_protected_resource_writes_accepted_total counter\n")
		fmt.Fprintf(w, "lockguard_protected_resource_writes_accepted_total %d\n", writesAccepted.Load())
		fmt.Fprintf(w, "# HELP lockguard_protected_resource_writes_rejected_total Total writes rejected due to stale or conflicting fencing tokens.\n")
		fmt.Fprintf(w, "# TYPE lockguard_protected_resource_writes_rejected_total counter\n")
		fmt.Fprintf(w, "lockguard_protected_resource_writes_rejected_total %d\n", writesRejected.Load())
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "ok",
			"service":   ServiceName,
			"version":   Version,
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		})
	})

	mux.HandleFunc("/api/v1/write", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var req WriteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid json request"}`, http.StatusBadRequest)
			return
		}

		if req.ResourceName == "" || req.FencingToken == 0 || req.OwnerID == "" {
			http.Error(w, `{"error":"resource_name, fencing_token, and owner_id are required"}`, http.StatusBadRequest)
			return
		}

		if err := guard.ValidateAndApply(req.ResourceName, req.FencingToken, req.OwnerID); err != nil {
			writesRejected.Add(1)
			logger.Warn("fencing_token_rejected",
				slog.String("resource", req.ResourceName),
				slog.Uint64("token", req.FencingToken),
				slog.String("owner_id", req.OwnerID),
				slog.String("error", err.Error()),
			)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error": map[string]interface{}{
					"code":    "ERR_FENCING_TOKEN_STALE",
					"message": err.Error(),
				},
			})
			return
		}

		writesAccepted.Add(1)
		logger.Info("fencing_write_accepted",
			slog.String("resource", req.ResourceName),
			slog.Uint64("token", req.FencingToken),
			slog.String("owner_id", req.OwnerID),
		)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":       true,
			"message":       "write committed safely under fencing token verification",
			"applied_token": req.FencingToken,
			"owner_id":      req.OwnerID,
		})
	})

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Info("protected_resource_started", slog.String("addr", addr))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server_failed", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	<-stop
	logger.Info("shutting_down_protected_resource")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown_error", slog.String("error", err.Error()))
	}
}
