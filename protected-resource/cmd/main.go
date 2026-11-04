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
	mux := http.NewServeMux()

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

		if req.ResourceName == "" || req.FencingToken == 0 {
			http.Error(w, `{"error":"resource_name and fencing_token are required"}`, http.StatusBadRequest)
			return
		}

		if err := guard.ValidateAndApply(req.ResourceName, req.FencingToken); err != nil {
			logger.Warn("fencing_token_rejected",
				slog.String("resource", req.ResourceName),
				slog.Uint64("token", req.FencingToken),
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

		logger.Info("fencing_write_accepted",
			slog.String("resource", req.ResourceName),
			slog.Uint64("token", req.FencingToken),
		)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "write committed safely under fencing token verification",
			"applied_token": req.FencingToken,
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
