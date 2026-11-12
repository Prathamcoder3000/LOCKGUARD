package benchmark

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

// Handler serves live benchmark execution and history endpoints.
type Handler struct {
	runner *Runner
}

// NewHandler constructs a benchmark HTTP Handler.
func NewHandler(runner *Runner) *Handler {
	return &Handler{runner: runner}
}

// RegisterRoutesWithAuth registers read and admin-gated benchmark execution routes.
func (h *Handler) RegisterRoutesWithAuth(mux *http.ServeMux, readWrap, adminWrap func(http.Handler) http.Handler) {
	wrapRead := func(fn http.HandlerFunc) http.Handler {
		if readWrap != nil {
			return readWrap(fn)
		}
		return fn
	}
	wrapAdmin := func(fn http.HandlerFunc) http.Handler {
		if adminWrap != nil {
			return adminWrap(fn)
		}
		return fn
	}

	mux.Handle("/v1/benchmarks/runs", wrapRead(h.handleListRuns))
	mux.Handle("/api/v1/benchmarks/runs", wrapRead(h.handleListRuns))

	mux.Handle("/v1/benchmarks/run", wrapAdmin(h.handleRun))
	mux.Handle("/api/v1/benchmarks/run", wrapAdmin(h.handleRun))
}

func (h *Handler) handleListRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	runs, source, _ := h.runner.ListRuns(r.Context(), limit)
	respondOK(w, http.StatusOK, map[string]interface{}{
		"running":             h.runner.IsRunning(),
		"storage_source":      source,
		"runs":                runs,
		"reference_baselines": ReferenceBaselines(),
	})
}

func (h *Handler) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, http.StatusMethodNotAllowed, "ERR_METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	var req RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "ERR_BAD_REQUEST", "invalid JSON request body")
		return
	}

	run, err := h.runner.Execute(r.Context(), req)
	if err != nil {
		if errors.Is(err, ErrBenchmarkRunning) {
			respondErr(w, http.StatusConflict, "ERR_BENCHMARK_RUNNING", err.Error())
			return
		}
		if errors.Is(err, ErrNoLeader) {
			respondErr(w, http.StatusServiceUnavailable, "ERR_QUORUM_UNAVAILABLE", err.Error())
			return
		}
		respondErr(w, http.StatusInternalServerError, "ERR_BENCHMARK_FAILED", err.Error())
		return
	}

	respondOK(w, http.StatusOK, run)
}

func respondOK(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"data":      data,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func respondErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error": map[string]interface{}{
			"code":    code,
			"message": msg,
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
}
