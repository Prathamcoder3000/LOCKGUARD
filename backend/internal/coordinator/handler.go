package coordinator

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lockguard/backend/internal/api"
	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/lockstate"
	"lockguard/backend/internal/metrics"

	"github.com/hashicorp/raft"
)

// Handler provides HTTP endpoints for coordinator lock operations and cluster inspection.
type Handler struct {
	node       *consensus.RaftNode
	nodeID     string
	metricsReg *metrics.Registry
}

// NewHandler constructs a new coordinator HTTP Handler.
func NewHandler(node *consensus.RaftNode, nodeID string) *Handler {
	return &Handler{
		node:   node,
		nodeID: nodeID,
	}
}

// NewHandlerWithMetrics constructs a coordinator HTTP Handler wired to a Prometheus metrics registry.
func NewHandlerWithMetrics(node *consensus.RaftNode, nodeID string, metricsReg *metrics.Registry) *Handler {
	return &Handler{
		node:       node,
		nodeID:     nodeID,
		metricsReg: metricsReg,
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
	mux.HandleFunc("/v1/cluster/stepdown", h.handleStepDown)
	mux.HandleFunc("/v1/cluster/snapshots", h.handleListSnapshots)
	mux.HandleFunc("/v1/cluster/snapshots/create", h.handleCreateSnapshot)
	mux.HandleFunc("/v1/cluster/snapshots/raw", h.handleGetSnapshotRaw)
	mux.HandleFunc("/v1/cluster/snapshots/restore", h.handleRestoreSnapshot)
	mux.HandleFunc("/v1/cluster/partition/inject", h.handlePartitionInject)
	mux.HandleFunc("/v1/cluster/partition/heal", h.handlePartitionHeal)
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

	reqID := resolveRequestID(req.RequestID, r, "req-acq")
	cmd := lockstate.AcquireLockCommand{
		RequestID:         reqID,
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

	reqID := resolveRequestID(req.RequestID, r, "req-rnw")
	cmd := lockstate.RenewLockCommand{
		RequestID:         reqID,
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

	reqID := resolveRequestID(req.RequestID, r, "req-rel")
	cmd := lockstate.ReleaseLockCommand{
		RequestID:         reqID,
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

	reqID := resolveRequestID(req.RequestID, r, "req-exp")
	cmd := lockstate.ExpireLockCommand{
		RequestID:                 reqID,
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

	incExpParam := r.URL.Query().Get("include_expired")
	includeExpired := (incExpParam == "true" || incExpParam == "1")

	locks, err := h.node.ListLocksAt(linearizable, time.Now().UnixNano(), includeExpired)
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

	rec, found, err := h.node.GetLockAt(resName, linearizable, time.Now().UnixNano())
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
	nodeCfg := h.node.Config()

	servers := make([]map[string]interface{}, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		servers = append(servers, map[string]interface{}{
			"id":       string(s.ID),
			"address":  string(s.Address),
			"suffrage": s.Suffrage.String(),
		})
	}

	api.RespondSuccess(w, http.StatusOK, map[string]interface{}{
		"node_id":             h.nodeID,
		"state":               h.node.State().String(),
		"is_leader":           h.node.IsLeader(),
		"leader_id":           string(leaderID),
		"leader_address":      string(leaderAddr),
		"applied_index":       h.node.AppliedIndex(),
		"commit_index":        h.node.CommitIndex(),
		"last_index":          h.node.LastIndex(),
		"current_term":        h.node.CurrentTerm(),
		"partitioned":         h.node.IsPartitioned(),
		"raft_bind_addr":      nodeCfg.BindAddr,
		"raft_advertise_addr": nodeCfg.AdvertiseAddr,
		"data_dir":            nodeCfg.DataDir,
		"snapshot_threshold":  nodeCfg.SnapshotThreshold,
		"snapshot_interval":   nodeCfg.SnapshotInterval.String(),
		"servers":             servers,
		"stats":               h.node.Stats(),
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
		"partitioned":    h.node.IsPartitioned(),
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

func (h *Handler) handleStepDown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, api.ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}
	if !h.node.IsLeader() {
		api.RespondError(w, http.StatusConflict, api.ErrCodeNotLeader, "node is not the active Raft leader", nil)
		return
	}
	err := h.node.LeadershipTransfer()
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, api.ErrCodeInternal, err.Error(), nil)
		return
	}
	api.RespondSuccess(w, http.StatusOK, map[string]interface{}{
		"message": "leadership transfer initiated successfully",
		"node_id": h.nodeID,
	})
}

func (h *Handler) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, api.ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}
	snaps, err := h.node.ListSnapshots()
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, api.ErrCodeInternal, err.Error(), nil)
		return
	}
	api.RespondSuccess(w, http.StatusOK, map[string]interface{}{
		"node_id":   h.nodeID,
		"is_leader": h.node.IsLeader(),
		"snapshots": snaps,
	})
}

func (h *Handler) handleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, api.ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}

	alreadyUpToDate := false
	if err := h.node.Snapshot(); err != nil {
		if errors.Is(err, raft.ErrNothingNewToSnapshot) {
			alreadyUpToDate = true
		} else {
			api.RespondError(w, http.StatusInternalServerError, api.ErrCodeInternal, err.Error(), nil)
			return
		}
	}

	snaps, err := h.node.ListSnapshots()
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, api.ErrCodeInternal, err.Error(), nil)
		return
	}

	var latest interface{}
	if len(snaps) > 0 {
		latest = snaps[0]
	}

	api.RespondSuccess(w, http.StatusOK, map[string]interface{}{
		"node_id":            h.nodeID,
		"already_up_to_date": alreadyUpToDate,
		"latest":             latest,
		"snapshots":          snaps,
	})
}

func (h *Handler) handleGetSnapshotRaw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, api.ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}
	snapID := strings.TrimSpace(r.URL.Query().Get("id"))
	meta, raw, info, err := h.node.OpenSnapshotRaw(snapID)
	if err != nil {
		api.RespondError(w, http.StatusNotFound, api.ErrCodeNotFound, err.Error(), nil)
		return
	}
	api.RespondSuccess(w, http.StatusOK, map[string]interface{}{
		"node_id":     h.nodeID,
		"info":        info,
		"meta":        meta,
		"payload_b64": base64.StdEncoding.EncodeToString(raw),
	})
}

type restoreSnapshotReq struct {
	SnapshotID string             `json:"snapshot_id"`
	Confirm    bool               `json:"confirm"`
	Meta       *raft.SnapshotMeta `json:"meta,omitempty"`
	PayloadB64 string             `json:"payload_b64,omitempty"`
}

func (h *Handler) handleRestoreSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, api.ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}

	var req restoreSnapshotReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.RespondError(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid JSON payload", nil)
		return
	}
	if !req.Confirm {
		api.RespondError(w, http.StatusBadRequest, api.ErrCodeBadRequest, "explicit confirmation (confirm=true) is required before restoring a Raft snapshot", nil)
		return
	}
	if !h.node.IsLeader() {
		addr, id := h.node.Leader()
		h.handleApplyError(w, &consensus.NotLeaderError{LeaderID: string(id), LeaderAddress: string(addr)})
		return
	}

	var restoredInfo *consensus.SnapshotInfo
	if req.PayloadB64 != "" && req.Meta != nil {
		raw, err := base64.StdEncoding.DecodeString(req.PayloadB64)
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, api.ErrCodeBadRequest, fmt.Sprintf("invalid snapshot base64 payload: %v", err), nil)
			return
		}
		if err := h.node.RestoreSnapshotBytes(req.Meta, raw, 15*time.Second); err != nil {
			h.handleRestoreError(w, err)
			return
		}
		restoredInfo = &consensus.SnapshotInfo{
			ID:    req.Meta.ID,
			Index: req.Meta.Index,
			Term:  req.Meta.Term,
			Size:  int64(len(raw)),
			Valid: true,
		}
	} else {
		info, err := h.node.RestoreSnapshotByID(req.SnapshotID, 15*time.Second)
		if err != nil {
			h.handleRestoreError(w, err)
			return
		}
		restoredInfo = info
	}

	if h.metricsReg != nil {
		h.metricsReg.IncSnapshotRestore("success")
	}

	api.RespondSuccess(w, http.StatusOK, map[string]interface{}{
		"node_id":  h.nodeID,
		"restored": true,
		"snapshot": restoredInfo,
	})
}

func (h *Handler) handleRestoreError(w http.ResponseWriter, err error) {
	var notLeaderErr *consensus.NotLeaderError
	if errors.As(err, &notLeaderErr) {
		h.handleApplyError(w, err)
		return
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "not found") || strings.Contains(lower, "invalid snapshot") || strings.Contains(lower, "corrupt snapshot") || strings.Contains(lower, "no snapshots available") {
		api.RespondError(w, http.StatusBadRequest, api.ErrCodeBadRequest, err.Error(), nil)
		return
	}
	api.RespondError(w, http.StatusInternalServerError, api.ErrCodeInternal, err.Error(), nil)
}

type partitionInjectReq struct {
	Confirm bool `json:"confirm"`
}

func (h *Handler) handlePartitionInject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, api.ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}
	var req partitionInjectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Confirm {
		api.RespondError(w, http.StatusBadRequest, api.ErrCodeBadRequest, "explicit confirmation (confirm=true) is required to isolate Raft transport", nil)
		return
	}
	if h.node.IsPartitioned() {
		api.RespondError(w, http.StatusConflict, api.ErrCodeConflict, fmt.Sprintf("node %s is already partitioned", h.nodeID), nil)
		return
	}
	if err := h.node.SetPartitioned(true); err != nil {
		api.RespondError(w, http.StatusInternalServerError, api.ErrCodeInternal, err.Error(), nil)
		return
	}
	if h.metricsReg != nil {
		h.metricsReg.IncNetworkPartitions("simulated_partition")
	}
	api.RespondSuccess(w, http.StatusOK, map[string]interface{}{
		"node_id":     h.nodeID,
		"partitioned": true,
	})
}

func (h *Handler) handlePartitionHeal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, api.ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}
	if err := h.node.SetPartitioned(false); err != nil {
		api.RespondError(w, http.StatusInternalServerError, api.ErrCodeInternal, err.Error(), nil)
		return
	}
	api.RespondSuccess(w, http.StatusOK, map[string]interface{}{
		"node_id":     h.nodeID,
		"partitioned": false,
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

func resolveRequestID(raw string, r *http.Request, prefix string) string {
	id := strings.TrimSpace(raw)
	if id != "" {
		return id
	}
	id = strings.TrimSpace(r.Header.Get("X-Request-ID"))
	if id != "" {
		return id
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}
