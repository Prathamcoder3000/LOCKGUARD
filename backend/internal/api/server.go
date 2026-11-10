package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"lockguard/backend/internal/audit"
	"lockguard/backend/internal/auth"
	"lockguard/backend/internal/config"
	"lockguard/backend/internal/gateway"
	"lockguard/backend/internal/heartbeat"
	"lockguard/backend/internal/metrics"
	"lockguard/backend/internal/middleware"
	"lockguard/backend/internal/persistence"
	"lockguard/backend/internal/recovery"
	"lockguard/backend/internal/renewal"
	"sync"
)

// Server encapsulates the API Gateway, client session manager, lease renewal, persistence, and audit subsystems.
type Server struct {
	cfg         *config.Config
	logger      *slog.Logger
	httpServer  *http.Server
	mux         *http.ServeMux
	serviceName string
	version     string

	discovery     *gateway.LeaderDiscovery
	router        *gateway.Router
	heartbeatMgr  *heartbeat.Manager
	renewalMgr    *renewal.Manager
	metricsReg    *metrics.Registry
	db            *persistence.Database
	migrator      *persistence.Migrator
	auditRepo     persistence.AuditRepository
	auditPipeline *audit.Pipeline
	auditHandler  *audit.Handler
	diagnostics   *recovery.DiagnosticsService

	authService    *auth.Service
	authMiddleware *auth.Middleware
	rateLimiter    *middleware.RateLimiter
	loginLimiter   *middleware.RateLimiter
}

// ClusterStatusResponse outlines cluster topology and quorum expectations.
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
	metricsReg := metrics.DefaultRegistry

	discovery := gateway.NewLeaderDiscovery(
		cfg.Gateway.CoordinatorEndpoints,
		cfg.Gateway.LeaderCacheTTL,
		nil,
		metricsReg,
	)

	router := gateway.NewRouter(cfg, discovery, metricsReg)
	hbMgr := heartbeat.NewManager(cfg, metricsReg)

	// Renewal worker client routes through discovered leader with 421 retry
	renewClient := &dynamicGatewayRenewalClient{
		discovery:  discovery,
		httpClient: &http.Client{Timeout: cfg.Gateway.RequestTimeout},
	}
	renewalMgr := renewal.NewManager(cfg, renewClient, hbMgr, metricsReg)

	// Initialize Phase 3 PostgreSQL persistence if configuration permits
	var db *persistence.Database
	var migrator *persistence.Migrator
	var auditRepo persistence.AuditRepository
	pCfg, err := persistence.LoadConfigFromEnv()
	if err == nil {
		if openedDB, err := persistence.Open(pCfg); err == nil {
			db = openedDB
			migrator = persistence.NewMigrator(db)
			auditRepo = persistence.NewPostgresAuditRepo(db)

			// Execute non-blocking migration check in background
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := migrator.RunMigrations(ctx); err != nil {
					logger.Warn("initial_migrations_deferred", slog.String("error", err.Error()))
					metricsReg.SetGauge("lockguard_database_migrations_status", nil, 0)
				} else {
					logger.Info("schema_migrations_up_to_date")
					metricsReg.SetGauge("lockguard_database_migrations_status", nil, 1)
				}
			}()
		} else {
			logger.Warn("persistence_db_open_failed", slog.String("error", err.Error()))
		}
	} else {
		logger.Warn("persistence_config_unavailable", slog.String("error", err.Error()))
	}

	auditPipe := audit.NewPipeline(auditRepo, metricsReg, audit.DefaultPipelineConfig())
	auditH := audit.NewHandler(auditRepo, auditPipe, cfg)
	diagService := recovery.NewDiagnosticsService(cfg.Raft.NodeID, serviceName, discovery, db, migrator, auditPipe)

	adminToken := "admin"
	if envTok := cfg.Auth.APITokenSalt; envTok != "" {
		adminToken = envTok
	}
	authService := auth.NewService(cfg.Auth.JWTSecretKey, time.Duration(cfg.Auth.JWTExpirationH)*time.Hour, adminToken)
	authMiddleware := auth.NewMiddleware(authService, metricsReg)
	rateLimiter := middleware.NewRateLimiter(200.0, 400.0, 10000, metricsReg)
	loginLimiter := middleware.NewRateLimiter(10.0, 20.0, 1000, metricsReg)

	s := &Server{
		cfg:            cfg,
		logger:         logger,
		mux:            mux,
		serviceName:    serviceName,
		version:        version,
		discovery:      discovery,
		router:         router,
		heartbeatMgr:   hbMgr,
		renewalMgr:     renewalMgr,
		metricsReg:     metricsReg,
		db:             db,
		migrator:       migrator,
		auditRepo:      auditRepo,
		auditPipeline:  auditPipe,
		auditHandler:   auditH,
		diagnostics:    diagService,
		authService:    authService,
		authMiddleware: authMiddleware,
		rateLimiter:    rateLimiter,
		loginLimiter:   loginLimiter,
	}

	s.registerRoutes()

	// Apply production middleware stack: SecurityHeaders -> MaxBodySize (1MB) -> ValidateContentType -> RateLimiting -> RequestLogger -> CORS -> Recovery
	var handler http.Handler = s.mux
	handler = middleware.RateLimitMiddleware(rateLimiter, "global")(handler)
	handler = middleware.ValidateContentType(handler)
	handler = middleware.MaxBodySize(1 << 20)(handler)
	handler = middleware.SecurityHeaders(handler)
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

// AuthService returns the centralized authentication service.
func (s *Server) AuthService() *auth.Service {
	return s.authService
}

// RateLimiter returns the API Gateway rate limiter.
func (s *Server) RateLimiter() *middleware.RateLimiter {
	return s.rateLimiter
}

func (s *Server) registerRoutes() {
	// Centralized Authentication endpoints
	s.mux.HandleFunc("/v1/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/v1/auth/login", s.handleLogin)

	// Top-level Health & Readiness probes
	s.mux.HandleFunc("/health", HealthHandler(s.serviceName, s.version, s.cfg.Raft.NodeID))
	s.mux.HandleFunc("/ready", ReadinessHandler(s.cfg, s.serviceName, s.cfg.Raft.NodeID, s.discovery))
	s.mux.HandleFunc("/metrics", s.metricsReg.Handler())

	// Versioned Health & Readiness routes under /v1/ and /api/v1/
	s.mux.HandleFunc("/v1/health", HealthHandler(s.serviceName, s.version, s.cfg.Raft.NodeID))
	s.mux.HandleFunc("/v1/ready", ReadinessHandler(s.cfg, s.serviceName, s.cfg.Raft.NodeID, s.discovery))
	s.mux.HandleFunc("/v1/metrics", s.metricsReg.Handler())
	s.mux.HandleFunc("/api/v1/health", HealthHandler(s.serviceName, s.version, s.cfg.Raft.NodeID))
	s.mux.HandleFunc("/api/v1/ready", ReadinessHandler(s.cfg, s.serviceName, s.cfg.Raft.NodeID, s.discovery))
	s.mux.HandleFunc("/api/v1/metrics", s.metricsReg.Handler())

	// Cluster topology and invariant status
	s.mux.HandleFunc("/v1/cluster/status", s.handleClusterStatus)
	s.mux.HandleFunc("/api/v1/cluster/status", s.handleClusterStatus)

	// Lock operations proxied to current Raft leader with audit logging
	s.mux.HandleFunc("/v1/locks", s.handleLockOperation)
	s.mux.HandleFunc("/v1/locks/", s.handleLockOperation)
	s.mux.HandleFunc("/api/v1/locks", s.handleLockOperation)
	s.mux.HandleFunc("/api/v1/locks/", s.handleLockOperation)

	// Client session and heartbeat management
	hbHandler := heartbeat.NewHandler(s.heartbeatMgr)
	hbHandler.RegisterRoutes(s.mux)
	s.mux.HandleFunc("/api/v1/clients/register", s.stripAPIPrefix(hbHandler))
	s.mux.HandleFunc("/api/v1/clients/heartbeat", s.stripAPIPrefix(hbHandler))
	s.mux.HandleFunc("/api/v1/clients", s.stripAPIPrefix(hbHandler))
	s.mux.HandleFunc("/api/v1/clients/", s.stripAPIPrefix(hbHandler))

	// Automated lease renewal management
	renewalHandler := renewal.NewHandler(s.renewalMgr)
	renewalHandler.RegisterRoutes(s.mux)
	s.mux.HandleFunc("/api/v1/renewal/start", s.stripAPIPrefix(renewalHandler))
	s.mux.HandleFunc("/api/v1/renewal/stop", s.stripAPIPrefix(renewalHandler))
	s.mux.HandleFunc("/api/v1/renewal/status", s.stripAPIPrefix(renewalHandler))

	// Phase 3 Audit Query API
	s.auditHandler.RegisterRoutes(s.mux)

	// Phase 3 & Phase 4 Operational Recovery and Diagnostics API
	recoveryH := recovery.NewHandler(s.diagnostics)
	recoveryH.RegisterRoutes(s.mux)

	// Phase 5 Admin Endpoints (RBAC protected)
	adminNodesHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		RespondSuccess(w, http.StatusOK, map[string]interface{}{
			"nodes":         s.cfg.Raft.ClusterNodes,
			"node_id":       s.cfg.Raft.NodeID,
			"cluster_state": "HEALTHY",
		})
	})
	s.mux.Handle("/v1/admin/cluster/nodes", s.authMiddleware.Authenticate(s.authMiddleware.RequireRole(auth.RoleAdmin)(adminNodesHandler)))
	s.mux.Handle("/api/v1/admin/cluster/nodes", s.authMiddleware.Authenticate(s.authMiddleware.RequireRole(auth.RoleAdmin)(adminNodesHandler)))

	// Admin Emergency Unlock: allows administrators to break any active lock
	adminEmergencyUnlockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			RespondError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed", nil)
			return
		}
		var req struct {
			ResourceName string `json:"resource_name"`
			OwnerID      string `json:"owner_id"`
			FencingToken uint64 `json:"fencing_token"`
		}
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			RespondError(w, http.StatusBadRequest, ErrCodeBadRequest, "failed to read body", nil)
			return
		}
		if err := json.Unmarshal(bodyBytes, &req); err != nil || strings.TrimSpace(req.ResourceName) == "" {
			RespondError(w, http.StatusBadRequest, ErrCodeBadRequest, "valid json with resource_name required", nil)
			return
		}
		req.ResourceName = strings.TrimSpace(req.ResourceName)

		leaderURL, err := s.discovery.GetLeader(r.Context())
		if err != nil || leaderURL == "" {
			RespondError(w, http.StatusServiceUnavailable, ErrCodeServiceUnavailable, "no leader available for emergency unlock", nil)
			return
		}

		// If OwnerID or FencingToken not provided, resolve current lock from leader
		if req.OwnerID == "" || req.FencingToken == 0 {
			lockURL := fmt.Sprintf("%s/v1/locks/%s", leaderURL, req.ResourceName)
			resp, err := http.Get(lockURL)
			if err != nil || resp.StatusCode != http.StatusOK {
				RespondError(w, http.StatusNotFound, ErrCodeLockNotFound, "active lock not found for resource", nil)
				if resp != nil {
					_ = resp.Body.Close()
				}
				return
			}
			var lockData struct {
				Data struct {
					OwnerID      string `json:"owner_id"`
					FencingToken uint64 `json:"fencing_token"`
				} `json:"data"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&lockData)
			_ = resp.Body.Close()
			if lockData.Data.OwnerID == "" {
				RespondError(w, http.StatusNotFound, ErrCodeLockNotFound, "active lock record not held", nil)
				return
			}
			req.OwnerID = lockData.Data.OwnerID
			req.FencingToken = lockData.Data.FencingToken
		}

		releasePayload, _ := json.Marshal(map[string]interface{}{
			"request_id":    fmt.Sprintf("admin-unlock-%d", time.Now().UnixNano()),
			"resource_name": req.ResourceName,
			"owner_id":      req.OwnerID,
			"fencing_token": req.FencingToken,
		})
		r.Body = io.NopCloser(bytes.NewReader(releasePayload))
		r.URL.Path = "/v1/locks/release"
		s.handleLockOperation(w, r)
	})
	s.mux.Handle("/v1/admin/locks/emergency-unlock", s.authMiddleware.Authenticate(s.authMiddleware.RequireRole(auth.RoleAdmin)(adminEmergencyUnlockHandler)))
	s.mux.Handle("/api/v1/admin/locks/emergency-unlock", s.authMiddleware.Authenticate(s.authMiddleware.RequireRole(auth.RoleAdmin)(adminEmergencyUnlockHandler)))

	// Admin Stepdown: allows administrators to trigger a Raft leadership transfer
	adminStepdownHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			RespondError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed", nil)
			return
		}
		leaderURL, err := s.discovery.GetLeader(r.Context())
		if err != nil || leaderURL == "" {
			RespondError(w, http.StatusServiceUnavailable, ErrCodeServiceUnavailable, "no leader available for stepdown", nil)
			return
		}
		stepURL := fmt.Sprintf("%s/v1/cluster/stepdown", leaderURL)
		reqStep, err := http.NewRequestWithContext(r.Context(), http.MethodPost, stepURL, nil)
		if err != nil {
			RespondError(w, http.StatusInternalServerError, ErrCodeInternal, err.Error(), nil)
			return
		}
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Do(reqStep)
		if err != nil {
			RespondError(w, http.StatusBadGateway, ErrCodeInternal, "failed to communicate with leader: "+err.Error(), nil)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
	s.mux.Handle("/v1/admin/cluster/stepdown", s.authMiddleware.Authenticate(s.authMiddleware.RequireRole(auth.RoleAdmin)(adminStepdownHandler)))
	s.mux.Handle("/api/v1/admin/cluster/stepdown", s.authMiddleware.Authenticate(s.authMiddleware.RequireRole(auth.RoleAdmin)(adminStepdownHandler)))
}

func (s *Server) stripAPIPrefix(handler interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
		s.mux.ServeHTTP(w, r)
	}
}

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	body       *bytes.Buffer
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

var recorderPool = sync.Pool{
	New: func() interface{} {
		return &responseRecorder{
			body: new(bytes.Buffer),
		}
	},
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		RespondError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}

	var req auth.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, ErrCodeBadRequest, "malformed login payload", nil)
		return
	}

	resp, err := s.authService.Authenticate(req.Username, req.Password)
	if err != nil {
		s.metricsReg.IncAuthFailure("invalid_credentials")
		RespondError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "invalid username or password", nil)
		return
	}

	s.metricsReg.IncAuthSuccess(resp.User.Role)
	RespondSuccess(w, http.StatusOK, resp)
}

func (s *Server) handleLockOperation(w http.ResponseWriter, r *http.Request) {
	var bodyBytes []byte
	if r.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				s.metricsReg.IncSecurityViolation("payload_too_large")
				RespondError(w, http.StatusRequestEntityTooLarge, "ERR_PAYLOAD_TOO_LARGE", "request body exceeds maximum allowed size (1MB)", nil)
				return
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	// Security Verification: If Authorization header provided or strict production mode enabled
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" || (s.cfg.Auth.Enabled && s.cfg.Environment == "production") {
		if authHeader == "" {
			s.metricsReg.IncAuthFailure("missing_header")
			RespondError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "authentication required", nil)
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := s.authService.ValidateToken(tokenStr)
		if err != nil {
			s.metricsReg.IncAuthFailure("invalid_token")
			RespondError(w, http.StatusUnauthorized, ErrCodeUnauthorized, err.Error(), nil)
			return
		}
		s.metricsReg.IncAuthSuccess(claims.Role)

		// RBAC: ReadOnly role cannot execute mutating operations
		if claims.Role == auth.RoleReadOnly && (r.Method == http.MethodPost || r.Method == http.MethodDelete) {
			s.metricsReg.IncAuthzDenial(claims.Role)
			RespondError(w, http.StatusForbidden, ErrCodeForbidden, "read-only role cannot execute lock mutations", nil)
			return
		}

		// Ownership isolation: CLIENT role cannot mutate locks with different owner_id
		if claims.Role == auth.RoleClient && (r.Method == http.MethodPost || r.Method == http.MethodDelete) {
			var payload struct {
				OwnerID string `json:"owner_id"`
			}
			if len(bodyBytes) > 0 {
				_ = json.Unmarshal(bodyBytes, &payload)
				if payload.OwnerID != "" && !strings.EqualFold(payload.OwnerID, claims.Subject) {
					s.metricsReg.IncAuthzDenial(claims.Role)
					s.metricsReg.IncSecurityViolation("cross_client_lock_access")
					RespondError(w, http.StatusForbidden, ErrCodeOwnerMismatch, "authenticated identity mismatch with requested owner ID", nil)
					return
				}
			}
		}
	}

	rec := recorderPool.Get().(*responseRecorder)
	rec.ResponseWriter = w
	rec.statusCode = http.StatusOK
	rec.body.Reset()
	defer func() {
		rec.ResponseWriter = nil
		recorderPool.Put(rec)
	}()

	s.router.ProxyRequest(rec, r)

	// Non-blocking asynchronous audit event capture:
	// CRITICAL CONCURRENCY HARDENING: Clone response bytes before defer puts rec back into recorderPool
	// to eliminate any possibility of data race, stale buffer reuse, or data leakage across concurrent requests!
	if s.auditPipeline != nil {
		respCopy := append([]byte(nil), rec.body.Bytes()...)
		statusCode := rec.statusCode
		go s.recordProxyAuditEvent(r, bodyBytes, statusCode, respCopy)
	}
}

func (s *Server) recordProxyAuditEvent(r *http.Request, reqBytes []byte, statusCode int, respBytes []byte) {
	path := strings.ToLower(r.URL.Path)

	eventType := audit.TypeLockAcquire
	if strings.Contains(path, "renew") {
		eventType = audit.TypeLockRenew
	} else if strings.Contains(path, "release") {
		eventType = audit.TypeLockRelease
	} else if strings.Contains(path, "expire") {
		eventType = audit.TypeLockExpire
	}

	// Only audit mutating write operations (POST / DELETE)
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		return
	}

	var reqData struct {
		ResourceName string `json:"resource_name"`
		ResourceID   string `json:"resource_id"`
		OwnerID      string `json:"owner_id"`
		ClientID     string `json:"client_id"`
	}
	if len(reqBytes) > 0 {
		_ = json.Unmarshal(reqBytes, &reqData)
	}

	resName := reqData.ResourceName
	if resName == "" {
		resName = reqData.ResourceID
	}

	status := audit.StatusCommitted
	errorCode := ""
	var fToken, lVer uint64

	if statusCode == http.StatusOK {
		status = audit.StatusCommitted
		var respData struct {
			Data struct {
				FencingToken uint64 `json:"fencing_token"`
				LeaseVersion uint64 `json:"lease_version"`
				Record       *struct {
					FencingToken uint64 `json:"fencing_token"`
					LeaseVersion uint64 `json:"lease_version"`
				} `json:"record"`
			} `json:"data"`
		}
		if err := json.Unmarshal(respBytes, &respData); err == nil {
			if respData.Data.Record != nil {
				fToken = respData.Data.Record.FencingToken
				lVer = respData.Data.Record.LeaseVersion
			} else {
				fToken = respData.Data.FencingToken
				lVer = respData.Data.LeaseVersion
			}
		}
	} else if statusCode == http.StatusConflict {
		status = audit.StatusRejected
		var errResp struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(respBytes, &errResp); err == nil {
			errorCode = errResp.Error.Code
		}
	} else {
		status = audit.StatusFailed
		var errResp struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(respBytes, &errResp); err == nil {
			errorCode = errResp.Error.Code
		}
	}

	clientID := reqData.ClientID
	if clientID == "" {
		clientID = reqData.OwnerID
	}
	ownerID := reqData.OwnerID
	if ownerID == "" {
		ownerID = clientID
	}

	ev := &audit.Event{
		EventType:       eventType,
		EventCategory:   audit.CategoryLockOperation,
		RequestID:       r.Header.Get("X-Request-ID"),
		ResourceID:      resName,
		OwnerID:         ownerID,
		ClientID:        clientID,
		FencingToken:    fToken,
		LeaseVersion:    lVer,
		OperationStatus: status,
		ErrorCode:       errorCode,
		CoordinatorID:   s.cfg.Raft.NodeID,
		EventTimestamp:  time.Now().UTC(),
		Metadata: map[string]interface{}{
			"http_method": r.Method,
			"status_code": statusCode,
		},
	}

	s.auditPipeline.RecordEvent(ev)
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		RespondError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}

	report := s.diagnostics.CollectReport(r.Context())
	status := http.StatusOK
	if report.OverallStatus == recovery.StatusUnhealthy {
		status = http.StatusServiceUnavailable
	}

	RespondSuccess(w, status, report)
}

func (s *Server) handleClusterStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		RespondError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed", nil)
		return
	}

	// First try proxying to active leader coordinator if available
	leaderURL, err := s.discovery.GetLeader(r.Context())
	if err == nil && leaderURL != "" {
		s.router.ProxyRequest(w, r)
		return
	}

	// Fallback to local Gateway cluster topology metadata if leader unreachable
	subsystems := map[string]string{
		"api_gateway":        "operational_phase2",
		"heartbeat_manager":  "operational_phase2",
		"renewal_manager":    "operational_phase2",
		"metrics_registry":   "operational_phase2",
		"raft_consensus":     "coordinator_cluster_phase1b",
		"state_machine":      "deterministic_fsm_phase1a",
		"snapshot_manager":   "operational_phase3",
		"persistence_layer":  "operational_phase3",
		"audit_pipeline":     "operational_phase3",
		"protected_resource": "operational_phase4",
		"fault_engine":       "operational_phase4",
		"security_rbac":      "operational_phase5",
		"benchmark_module":   "operational_phase5",
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

// Start begins listening on the configured address and starts background scrubbers.
func (s *Server) Start() error {
	s.heartbeatMgr.Start()
	if s.auditPipeline != nil {
		s.auditPipeline.Start()
		s.auditPipeline.RecordEvent(&audit.Event{
			EventType:       audit.TypeSystemStartup,
			EventCategory:   audit.CategorySystem,
			OperationStatus: audit.StatusCommitted,
			CoordinatorID:   s.cfg.Raft.NodeID,
			EventTimestamp:  time.Now().UTC(),
		})
	}

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

// Shutdown gracefully stops the HTTP server, renewal manager, audit pipeline, and heartbeat scrubber.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("server_shutting_down", slog.String("service", s.serviceName))
	if s.router != nil {
		s.router.Close()
	}
	if s.auditPipeline != nil {
		s.auditPipeline.RecordEvent(&audit.Event{
			EventType:       audit.TypeSystemShutdown,
			EventCategory:   audit.CategorySystem,
			OperationStatus: audit.StatusCommitted,
			CoordinatorID:   s.cfg.Raft.NodeID,
			EventTimestamp:  time.Now().UTC(),
		})
		_ = s.auditPipeline.Shutdown(ctx)
	}
	s.renewalMgr.Shutdown()
	s.heartbeatMgr.Stop()
	if s.db != nil {
		_ = s.db.Close()
	}
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

// Close gracefully releases server resources and idle network connections.
func (s *Server) Close() error {
	return s.Shutdown(context.Background())
}

// Handler returns the underlying http.Handler (used for testing).
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// Discovery returns the leader discovery service.
func (s *Server) Discovery() *gateway.LeaderDiscovery {
	return s.discovery
}

// Router returns the reverse-proxy router.
func (s *Server) Router() *gateway.Router {
	return s.router
}

// HeartbeatManager returns the client heartbeat manager.
func (s *Server) HeartbeatManager() *heartbeat.Manager {
	return s.heartbeatMgr
}

// RenewalManager returns the lease renewal manager.
func (s *Server) RenewalManager() *renewal.Manager {
	return s.renewalMgr
}

// Metrics returns the Prometheus metrics registry.
func (s *Server) Metrics() *metrics.Registry {
	return s.metricsReg
}

// Database returns the persistence connection pool.
func (s *Server) Database() *persistence.Database {
	return s.db
}

// SetDatabase updates the database handle, migrator, audit repo, and diagnostics references.
func (s *Server) SetDatabase(db *persistence.Database) {
	s.db = db
	if db != nil {
		s.migrator = persistence.NewMigrator(db)
		s.auditRepo = persistence.NewPostgresAuditRepo(db)
		if s.auditHandler != nil {
			s.auditHandler.SetRepository(s.auditRepo)
		}
	} else {
		s.migrator = nil
		s.auditRepo = nil
		if s.auditHandler != nil {
			s.auditHandler.SetRepository(nil)
		}
	}
	if s.diagnostics != nil {
		s.diagnostics.SetDatabase(db, s.migrator)
	}
}

// SetAuditRepository allows updating or disconnecting the audit repository.
func (s *Server) SetAuditRepository(repo persistence.AuditRepository) {
	s.auditRepo = repo
	if s.auditHandler != nil {
		s.auditHandler.SetRepository(repo)
	}
}

// AuditPipeline returns the active audit event pipeline.
func (s *Server) AuditPipeline() *audit.Pipeline {
	return s.auditPipeline
}

// AuditRepository returns the audit persistence repository.
func (s *Server) AuditRepository() persistence.AuditRepository {
	return s.auditRepo
}

// Diagnostics returns the operational health diagnostics service.
func (s *Server) Diagnostics() *recovery.DiagnosticsService {
	return s.diagnostics
}

// dynamicGatewayRenewalClient routes renewal requests through discovered coordinator leader with 421 retry.
type dynamicGatewayRenewalClient struct {
	discovery  *gateway.LeaderDiscovery
	httpClient *http.Client
}

func (c *dynamicGatewayRenewalClient) GetLock(ctx context.Context, resourceName string) (*renewal.LockRecord, bool, error) {
	for attempt := 0; attempt < 3; attempt++ {
		leaderURL, err := c.discovery.GetLeader(ctx)
		if err != nil {
			return nil, false, err
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, leaderURL+"/v1/locks/"+resourceName, nil)
		if err != nil {
			return nil, false, err
		}

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			c.discovery.InvalidateLeader()
			if attempt < 2 {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			return nil, false, err
		}

		if resp.StatusCode == http.StatusMisdirectedRequest {
			leaderID := resp.Header.Get("X-Raft-Leader-ID")
			resp.Body.Close()
			if leaderID != "" {
				c.discovery.SetLeaderByID(leaderID)
			} else {
				c.discovery.InvalidateLeader()
			}
			time.Sleep(20 * time.Millisecond)
			continue
		}

		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return nil, false, nil
		}

		respBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, false, err
		}

		var generic struct {
			Success bool                `json:"success"`
			Data    *renewal.LockRecord `json:"data"`
		}
		if err := json.Unmarshal(respBytes, &generic); err == nil && generic.Success && generic.Data != nil {
			return generic.Data, true, nil
		}

		return nil, false, nil
	}
	return nil, false, fmt.Errorf("exceeded maximum redirect attempts")
}

func (c *dynamicGatewayRenewalClient) RenewLock(ctx context.Context, req renewal.RenewLockRequest) (*renewal.RenewLockResponse, error) {
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < 3; attempt++ {
		leaderURL, err := c.discovery.GetLeader(ctx)
		if err != nil {
			return nil, err
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, leaderURL+"/v1/locks/renew", bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if req.RequestID != "" {
			httpReq.Header.Set("X-Request-ID", req.RequestID)
		}

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			c.discovery.InvalidateLeader()
			if attempt < 2 {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			return nil, err
		}

		if resp.StatusCode == http.StatusMisdirectedRequest {
			leaderID := resp.Header.Get("X-Raft-Leader-ID")
			resp.Body.Close()
			if leaderID != "" {
				c.discovery.SetLeaderByID(leaderID)
			} else {
				c.discovery.InvalidateLeader()
			}
			time.Sleep(20 * time.Millisecond)
			continue
		}

		respBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}

		var generic map[string]interface{}
		if err := json.Unmarshal(respBytes, &generic); err != nil {
			return nil, err
		}

		success, _ := generic["success"].(bool)
		renewResp := &renewal.RenewLockResponse{Success: success}

		if success {
			dataBytes, _ := json.Marshal(generic["data"])
			var result struct {
				Success            bool                `json:"success"`
				Code               string              `json:"code"`
				Message            string              `json:"message"`
				ResourceName       string              `json:"resource_name"`
				OwnerID            string              `json:"owner_id"`
				FencingToken       uint64              `json:"fencing_token"`
				LeaseVersion       uint64              `json:"lease_version"`
				ExpiresAtUnixNano  int64               `json:"expires_at_unix_nano"`
				AcquiredAtUnixNano int64               `json:"acquired_at_unix_nano"`
				TTLNanoseconds     int64               `json:"ttl_nanoseconds"`
				Record             *renewal.LockRecord `json:"record"`
			}
			if err := json.Unmarshal(dataBytes, &result); err == nil {
				renewResp.Code = result.Code
				renewResp.Message = result.Message
				if result.Record != nil {
					renewResp.Record = result.Record
				} else {
					renewResp.Record = &renewal.LockRecord{
						ResourceName:       result.ResourceName,
						OwnerID:            result.OwnerID,
						FencingToken:       result.FencingToken,
						LeaseVersion:       result.LeaseVersion,
						ExpiresAtUnixNano:  result.ExpiresAtUnixNano,
						AcquiredAtUnixNano: result.AcquiredAtUnixNano,
						TTLNanoseconds:     result.TTLNanoseconds,
					}
				}
			}
			return renewResp, nil
		}

		if errObj, ok := generic["error"].(map[string]interface{}); ok {
			if code, ok := errObj["code"].(string); ok {
				renewResp.Code = code
			}
			if msg, ok := errObj["message"].(string); ok {
				renewResp.Message = msg
			}
		}

		return renewResp, nil
	}

	return nil, fmt.Errorf("exceeded maximum redirect attempts")
}
