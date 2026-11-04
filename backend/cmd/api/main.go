package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"lockguard/backend/internal/api"
	"lockguard/backend/internal/config"
)

const (
	ServiceName = "lockguard-api"
	Version     = "0.1.0"
)

func main() {
	// 1. Load and validate configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		slog.Error("failed_to_load_config", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// 2. Initialize structured logging
	var handler slog.Handler
	logLevel := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: logLevel}
	if cfg.LogFormat == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	logger := slog.New(handler)

	logger.Info("starting_service",
		slog.String("service", ServiceName),
		slog.String("version", Version),
		slog.String("env", cfg.Environment),
		slog.Int("port", cfg.HTTP.Port),
	)

	// 3. Create server
	srv := api.NewServer(cfg, logger, ServiceName, Version)

	// 4. Setup graceful shutdown channel
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := srv.Start(); err != nil {
			logger.Error("server_fatal_error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	// 5. Block until shutdown signal received
	sig := <-stopChan
	logger.Info("shutdown_signal_received", slog.String("signal", sig.String()))

	ctx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("server_shutdown_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("service_stopped_cleanly", slog.String("service", ServiceName))
}
