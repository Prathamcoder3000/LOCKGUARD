package coordinator

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lockguard/backend/internal/api"
	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/lockstate"

	"github.com/hashicorp/raft"
)

// Handler provides HTTP endpoints for coordinator lock operations and cluster inspection.
type Handler struct {
	node   *consensus.RaftNode
	nodeID string
}

// NewHandler constructs a new coordinator HTTP Handler.
func NewHandler(node *consensus.RaftNode, nodeID string) *Handler {
	return &Handler{
		node:   node,
		nodeID: nodeID,
	}
}

// RegisterRoutes registers all coordinator HTTP endpoints on the provided ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/locks/acquire", h.handleAcquire)
	mux.HandleFunc("/v1/locks/renew", h.handleRenew)
	mux.HandleFunc("/v1/locks/release", h.handleRelease)
	mux.HandleFunc("/v1/locks/expire", h.handleExpire)
	mux.HandleFunc("/v1/locks", h.handleLocks)
	mux.HandleFunc("/v1/locks/", h.handleGetLock)
	mux.HandleFunc("/v1/cluster/status", h.handleClusterStatus)
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/ready", h.handleReady)
}

// AcquireRequest models the incoming JSON payload for lock acquisition.
type AcquireRequest struct {
	RequestID         string `json:"request_id"`
	ResourceName      string `json:"resource_name"`
	OwnerID           string `json:"owner_id"`
	TTLSeconds        int64  `json:"ttl_seconds,omitempty"`
	TTLNanoseconds    int64  `json:"ttl_nanoseconds,omitempty"`
	TimestampUnixNano int64  `json:"timestamp_unix_nano,omitempty"`
}

// RenewRequest models the incoming JSON payload for lease renewal.
type RenewRequest struct {
	RequestID         string `json:"request_id"`
	ResourceName      string `json:"resource_name"`
	OwnerID           string `json:"owner_id"`
	FencingToken      uint64 `json:"fencing_token"`
	TTLSeconds        int64  `json:"ttl_seconds,omitempty"`
	TTLNanoseconds    int64  `json:"ttl_nanoseconds,omitempty"`
	TimestampUnixNano int64  `json:"timestamp_unix_nano,omitempty"`
}

// ReleaseRequest models the incoming JSON payload for explicit lease release.
type ReleaseRequest struct {
	RequestID         string `json:"request_id"`
	ResourceName      string `json:"resource_name"`
	OwnerID           string `json:"owner_id"`
	FencingToken      uint64 `json:"fencing_token"`
	TimestampUnixNano int64  `json:"timestamp_unix_nano,omitempty"`
}

// ExpireRequest models the incoming JSON payload for deterministic lease expiration.
type ExpireRequest struct {
	RequestID                 string `json:"request_id"`
	ResourceName              string `json:"resource_name"`
	ExpectedOwnerID           string `json:"expected_owner_id"`
	ExpectedFencingToken      uint64 `json:"expected_fencing_token"`
	ExpectedLeaseVersion      uint64 `json:"expected_lease_version"`
	ExpectedExpiresAtUnixNano int64  `json:"expected_expires_at_unix_nano"`
	TimestampUnixNano         int64  `json:"timestamp_unix_nano,omitempty"`
}

func (h *Handler) handleAcquire(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, api.ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}

	var req AcquireRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.RespondError(w, http.StatusBadRequest, api.ErrCodeBadRequest, fmt.Sprintf("invalid json payload: %v", err), nil)
		return
	}

	ttlNano := req.TTLNanoseconds
	if ttlNano <= 0 && req.TTLSeconds > 0 {
		ttlNano = req.TTLSeconds * int64(time.Second)
	}

	ts := req.TimestampUnixNano
	if ts <= 0 {
		ts = time.Now().UnixNano()
	}

	cmd := lockstate.AcquireLockCommand{
		RequestID:         strings.TrimSpace(req.RequestID),
		ResourceName:      strings.TrimSpace(req.ResourceName),
		OwnerID:           strings.TrimSpace(req.OwnerID),
		TTLNanoseconds:    ttlNano,
		TimestampUnixNano: ts,
	}

	result, err := h.node.ApplyCommand(r.Context(), cmd)
	if err != nil {
		h.handleApplyError(w, err)
		return
	}

	if !result.Success {
		status := mapErrorCodeToStatus(result.Code)
		api.RespondError(w, status, result.Code, result.Message, result)
		return
	}

	api.RespondSuccess(w, http.StatusOK, result)
}

func (h *Handler) handleRenew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, api.ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}

	var req RenewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.RespondError(w, http.StatusBadRequest, api.ErrCodeBadRequest, fmt.Sprintf("invalid json payload: %v", err), nil)
		return
	}

	ttlNano := req.TTLNanoseconds
	if ttlNano <= 0 && req.TTLSeconds > 0 {
		ttlNano = req.TTLSeconds * int64(time.Second)
	}

	ts := req.TimestampUnixNano
	if ts <= 0 {
		ts = time.Now().UnixNano()
	}

	cmd := lockstate.RenewLockCommand{
		RequestID:         strings.TrimSpace(req.RequestID),
		ResourceName:      strings.TrimSpace(req.ResourceName),
		OwnerID:           strings.TrimSpace(req.OwnerID),
		FencingToken:      req.FencingToken,
		TTLNanoseconds:    ttlNano,
		TimestampUnixNano: ts,
	}

	result, err := h.node.ApplyCommand(r.Context(), cmd)
	if err != nil {
		h.handleApplyError(w, err)
		return
	}

	if !result.Success {
		status := mapErrorCodeToStatus(result.Code)
		api.RespondError(w, status, result.Code, result.Message, result)
		return
	}

	api.RespondSuccess(w, http.StatusOK, result)
}

func (h *Handler) handleRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, api.ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}

	var req ReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.RespondError(w, http.StatusBadRequest, api.ErrCodeBadRequest, fmt.Sprintf("invalid json payload: %v", err), nil)
		return
	}

	ts := req.TimestampUnixNano
	if ts <= 0 {
		ts = time.Now().UnixNano()
	}

	cmd := lockstate.ReleaseLockCommand{
		RequestID:         strings.TrimSpace(req.RequestID),
		ResourceName:      strings.TrimSpace(req.ResourceName),
		OwnerID:           strings.TrimSpace(req.OwnerID),
		FencingToken:      req.FencingToken,
		TimestampUnixNano: ts,
	}

	result, err := h.node.ApplyCommand(r.Context(), cmd)
	if err != nil {
		h.handleApplyError(w, err)
		return
	}

	if !result.Success {
		status := mapErrorCodeToStatus(result.Code)
		api.RespondError(w, status, result.Code, result.Message, result)
		return
	}

	api.RespondSuccess(w, http.StatusOK, result)
}

func (h *Handler) handleExpire(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, api.ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}

	var req ExpireRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.RespondError(w, http.StatusBadRequest, api.ErrCodeBadRequest, fmt.Sprintf("invalid json payload: %v", err), nil)
		return
	}

	ts := req.TimestampUnixNano
	if ts <= 0 {
		ts = time.Now().UnixNano()
	}

	cmd := lockstate.ExpireLockCommand{
		RequestID:                 strings.TrimSpace(req.RequestID),
		ResourceName:              strings.TrimSpace(req.ResourceName),
		ExpectedOwnerID:           strings.TrimSpace(req.ExpectedOwnerID),
		ExpectedFencingToken:      req.ExpectedFencingToken,
		ExpectedLeaseVersion:      req.ExpectedLeaseVersion,
		ExpectedExpiresAtUnixNano: req.ExpectedExpiresAtUnixNano,
		TimestampUnixNano:         ts,
	}

	result, err := h.node.ApplyCommand(r.Context(), cmd)
	if err != nil {
		h.handleApplyError(w, err)
		return
	}

	if !result.Success {
		status := mapErrorCodeToStatus(result.Code)
		api.RespondError(w, status, result.Code, result.Message, result)
		return
	}

	api.RespondSuccess(w, http.StatusOK, result)
}

func (h *Handler) handleLocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, api.ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}

	staleParam := r.URL.Query().Get("stale")
	linearizable := (staleParam != "true" && staleParam != "1")
	if linearizable {
		w.Header().Set("X-LockGuard-Read-Consistency", "authoritative-linearizable")
	} else {
		w.Header().Set("X-LockGuard-Read-Consistency", "potentially-stale-diagnostic")
	}

	locks, err := h.node.ListLocks(linearizable)
	if err != nil {
		h.handleApplyError(w, err)
		return
	}

	api.RespondSuccess(w, http.StatusOK, map[string]interface{}{
		"locks": locks,
		"count": len(locks),
	})
}

func (h *Handler) handleGetLock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, api.ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}

	resName := strings.TrimPrefix(r.URL.Path, "/v1/locks/")
	if strings.TrimSpace(resName) == "" {
		h.handleLocks(w, r)
		return
	}

	staleParam := r.URL.Query().Get("stale")
	linearizable := (staleParam != "true" && staleParam != "1")
	if linearizable {
		w.Header().Set("X-LockGuard-Read-Consistency", "authoritative-linearizable")
	} else {
		w.Header().Set("X-LockGuard-Read-Consistency", "potentially-stale-diagnostic")
	}

	rec, found, err := h.node.GetLock(resName, linearizable)
	if err != nil {
		h.handleApplyError(w, err)
		return
	}

	if !found {
		api.RespondError(w, http.StatusNotFound, api.ErrCodeNotFound, fmt.Sprintf("no active lock found for resource %q", resName), nil)
		return
	}

	api.RespondSuccess(w, http.StatusOK, rec)
}

func (h *Handler) handleClusterStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, api.ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}

	leaderAddr, leaderID := h.node.Leader()
	cfg, _ := h.node.GetConfiguration()

	servers := make([]map[string]interface{}, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		servers = append(servers, map[string]interface{}{
			"id":       string(s.ID),
			"address":  string(s.Address),
			"suffrage": s.Suffrage.String(),
		})
	}

	api.RespondSuccess(w, http.StatusOK, map[string]interface{}{
		"node_id":        h.nodeID,
		"state":          h.node.State().String(),
		"is_leader":      h.node.IsLeader(),
		"leader_id":      string(leaderID),
		"leader_address": string(leaderAddr),
		"applied_index":  h.node.AppliedIndex(),
		"last_index":     h.node.LastIndex(),
		"servers":        servers,
		"stats":          h.node.Stats(),
	})
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	leaderAddr, leaderID := h.node.Leader()
	api.RespondSuccess(w, http.StatusOK, map[string]interface{}{
		"status":         "ok",
		"service":        "lockguard-coordinator",
		"node_id":        h.nodeID,
		"raft_state":     h.node.State().String(),
		"is_leader":      h.node.IsLeader(),
		"leader_id":      string(leaderID),
		"leader_address": string(leaderAddr),
		"timestamp":      time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (h *Handler) handleReady(w http.ResponseWriter, r *http.Request) {
	state := h.node.State()
	if state == raft.Shutdown {
		api.RespondError(w, http.StatusServiceUnavailable, api.ErrCodeNotReady, "raft node is shutdown", nil)
		return
	}

	leaderAddr, leaderID := h.node.Leader()
	api.RespondSuccess(w, http.StatusOK, map[string]interface{}{
		"status":         "ready",
		"service":        "lockguard-coordinator",
		"node_id":        h.nodeID,
		"raft_state":     state.String(),
		"is_leader":      h.node.IsLeader(),
		"leader_id":      string(leaderID),
		"leader_address": string(leaderAddr),
		"timestamp":      time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (h *Handler) handleApplyError(w http.ResponseWriter, err error) {
	var notLeaderErr *consensus.NotLeaderError
	if errors.As(err, &notLeaderErr) {
		if notLeaderErr.LeaderAddress != "" {
			w.Header().Set("X-Raft-Leader-Address", notLeaderErr.LeaderAddress)
		}
		if notLeaderErr.LeaderID != "" {
			w.Header().Set("X-Raft-Leader-ID", notLeaderErr.LeaderID)
		}
		api.RespondError(w, http.StatusMisdirectedRequest, api.ErrCodeNotLeader, notLeaderErr.Error(), map[string]interface{}{
			"leader_id":      notLeaderErr.LeaderID,
			"leader_address": notLeaderErr.LeaderAddress,
		})
		return
	}

	if errors.Is(err, consensus.ErrQuorumLoss) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		api.RespondError(w, http.StatusServiceUnavailable, api.ErrCodeQuorumUnavailable, "cluster quorum unavailable: commit timed out", nil)
		return
	}

	api.RespondError(w, http.StatusInternalServerError, api.ErrCodeInternal, err.Error(), nil)
}

func mapErrorCodeToStatus(code string) int {
	switch code {
	case lockstate.CodeLockAlreadyHeld, lockstate.CodeIdempotencyConflict:
		return http.StatusConflict // 409
	case lockstate.CodeLockNotFound:
		return http.StatusNotFound // 404
	case lockstate.CodeNotOwner:
		return http.StatusForbidden // 403
	case lockstate.CodeFencingTokenMismatch, lockstate.CodeLeaseExpired, lockstate.CodeInvalidExpiration, lockstate.CodeLeaseVersionMismatch:
		return http.StatusPreconditionFailed // 412
	case lockstate.CodeTokenExhausted, lockstate.CodeVersionExhausted:
		return http.StatusServiceUnavailable // 503
	case lockstate.CodeInvalidCommand, lockstate.CodeInvalidResource, lockstate.CodeInvalidOwner, lockstate.CodeInvalidTTL, lockstate.CodeInvalidTimestamp, lockstate.CodeArithmeticOverflow:
		return http.StatusBadRequest // 400
	default:
		return http.StatusInternalServerError // 500
	}
}
