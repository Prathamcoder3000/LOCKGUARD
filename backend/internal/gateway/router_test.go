package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lockguard/backend/internal/config"
	"lockguard/backend/internal/metrics"
)

func TestGateway_LeaderDiscoveryAndCaching(t *testing.T) {
	var leaderHits int32

	// Mock Leader Coordinator (coordinator-a)
	leaderServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&leaderHits, 1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/cluster/status" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"node_id":   "coordinator-a",
					"is_leader": true,
					"state":     "Leader",
				},
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"from-leader"}`))
	}))
	defer leaderServer.Close()

	// Mock Follower Coordinator (coordinator-b)
	followerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"node_id":        "coordinator-b",
				"is_leader":      false,
				"state":          "Follower",
				"leader_id":      "coordinator-a",
				"leader_address": leaderServer.URL,
			},
		})
	}))
	defer followerServer.Close()

	reg := metrics.NewRegistry()
	disc := NewLeaderDiscovery([]string{followerServer.URL, leaderServer.URL}, 500*time.Millisecond, leaderServer.Client(), reg)

	// First discovery
	leaderURL, err := disc.GetLeader(context.Background())
	if err != nil {
		t.Fatalf("unexpected error discovering leader: %v", err)
	}
	if leaderURL != leaderServer.URL {
		t.Errorf("expected leader URL %s, got %s", leaderServer.URL, leaderURL)
	}

	// Second resolution should hit cache without increasing probe count on /v1/cluster/status
	currHits := atomic.LoadInt32(&leaderHits)
	cachedURL, err := disc.GetLeader(context.Background())
	if err != nil {
		t.Fatalf("unexpected error on cached leader: %v", err)
	}
	if cachedURL != leaderServer.URL {
		t.Errorf("expected cached leader URL %s, got %s", leaderServer.URL, cachedURL)
	}
	if atomic.LoadInt32(&leaderHits) != currHits {
		t.Errorf("cache hit should not have probed leader again")
	}

	// Invalidate and verify rediscovery occurs
	disc.InvalidateLeader()
	_, err = disc.GetLeader(context.Background())
	if err != nil {
		t.Fatalf("failed rediscovery after invalidation: %v", err)
	}
	if atomic.LoadInt32(&leaderHits) <= currHits {
		t.Errorf("rediscovery should have probed leader again")
	}
}

func TestGateway_Follower421Redirection(t *testing.T) {
	var leaderCalled int32
	var followerCalled int32

	var leaderServer *httptest.Server
	var followerServer *httptest.Server

	leaderServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&leaderCalled, 1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/cluster/status" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"node_id":   "coordinator-a",
					"is_leader": true,
				},
			})
			return
		}
		// Echo request body
		b, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"received":` + string(b) + `}`))
	}))
	defer leaderServer.Close()

	followerServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&followerCalled, 1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/cluster/status" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"node_id":   "coordinator-b",
					"is_leader": false,
					"leader_id": "coordinator-a",
				},
			})
			return
		}
		// Return 421 Misdirected Request with leader headers
		w.Header().Set("X-Raft-Leader-ID", "coordinator-a")
		w.Header().Set("X-Raft-Leader-Address", leaderServer.URL)
		w.WriteHeader(http.StatusMisdirectedRequest)
		_, _ = w.Write([]byte(`{"success":false,"error":{"code":"ERR_NOT_LEADER"}}`))
	}))
	defer followerServer.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: []string{followerServer.URL, leaderServer.URL},
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  2,
			RequestTimeout:       2 * time.Second,
		},
	}

	reg := metrics.NewRegistry()
	disc := NewLeaderDiscovery([]string{followerServer.URL, leaderServer.URL}, 5*time.Second, leaderServer.Client(), reg)
	// Seed follower node and leader node in map
	disc.nodeToEndpoint["coordinator-a"] = leaderServer.URL
	disc.nodeToEndpoint["coordinator-b"] = followerServer.URL

	// Intentionally set cached leader to the follower to test 421 redirection!
	disc.cachedLeader = followerServer.URL
	disc.cacheExpiresAt = time.Now().Add(10 * time.Second)

	router := NewRouter(cfg, disc, reg)

	reqPayload := `{"resource_name":"doc-123","owner_id":"worker-1"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewBufferString(reqPayload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ProxyRequest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK after 421 redirection, got %d, body: %s", rec.Code, rec.Body.String())
	}

	if atomic.LoadInt32(&followerCalled) == 0 {
		t.Errorf("expected follower to be called initially")
	}
	if atomic.LoadInt32(&leaderCalled) == 0 {
		t.Errorf("expected leader to be called after redirection")
	}

	var res map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if !res["success"].(bool) {
		t.Errorf("expected success true, got %v", res)
	}
}

func TestGateway_QuorumUnavailable(t *testing.T) {
	// All coordinator endpoints are down
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: []string{"http://127.0.0.1:59999", "http://127.0.0.1:59998"},
			LeaderCacheTTL:       1 * time.Second,
			MaxRedirectAttempts:  1,
			RequestTimeout:       500 * time.Millisecond,
		},
	}

	reg := metrics.NewRegistry()
	disc := NewLeaderDiscovery(cfg.Gateway.CoordinatorEndpoints, 1*time.Second, nil, reg)
	router := NewRouter(cfg, disc, reg)

	req := httptest.NewRequest(http.MethodGet, "/v1/locks", nil)
	rec := httptest.NewRecorder()

	router.ProxyRequest(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable when quorum is down, got %d", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "ERR_QUORUM_UNAVAILABLE") {
		t.Errorf("expected ERR_QUORUM_UNAVAILABLE code, got: %s", rec.Body.String())
	}
}

func TestGateway_OversizedRequestBodyRejected(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: []string{"http://127.0.0.1:59999"},
			LeaderCacheTTL:       1 * time.Second,
			MaxRedirectAttempts:  1,
			RequestTimeout:       500 * time.Millisecond,
		},
	}

	reg := metrics.NewRegistry()
	disc := NewLeaderDiscovery(cfg.Gateway.CoordinatorEndpoints, 1*time.Second, nil, reg)
	router := NewRouter(cfg, disc, reg)

	// Create payload larger than 1 MB (MaxRequestBodyBytes)
	largeBody := bytes.Repeat([]byte("X"), MaxRequestBodyBytes+2048)
	req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(largeBody))
	rec := httptest.NewRecorder()

	router.ProxyRequest(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 Payload Too Large, got %d: %s", rec.Code, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), "ERR_PAYLOAD_TOO_LARGE") {
		t.Errorf("expected ERR_PAYLOAD_TOO_LARGE code, got: %s", rec.Body.String())
	}
}

func TestGateway_PathMetricNormalization(t *testing.T) {
	leaderServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/cluster/status" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"node_id":   "coord-1",
					"is_leader": true,
					"state":     "Leader",
				},
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer leaderServer.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: []string{leaderServer.URL},
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  1,
			RequestTimeout:       1 * time.Second,
		},
	}

	reg := metrics.NewRegistry()
	disc := NewLeaderDiscovery(cfg.Gateway.CoordinatorEndpoints, 5*time.Second, leaderServer.Client(), reg)
	router := NewRouter(cfg, disc, reg)

	// Send requests with distinct dynamic path segments
	paths := []string{"/v1/locks/order-12345", "/v1/locks/inventory-9876", "/v1/clients/session-alpha"}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		router.ProxyRequest(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d", p, rec.Code)
		}
	}

	// Verify Prometheus metrics have normalized path labels
	recMetrics := httptest.NewRecorder()
	reg.Handler().ServeHTTP(recMetrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricsText := recMetrics.Body.String()

	if strings.Contains(metricsText, "order-12345") || strings.Contains(metricsText, "inventory-9876") {
		t.Errorf("unnormalized dynamic lock resource leaked into metric labels:\n%s", metricsText)
	}
	if strings.Contains(metricsText, "session-alpha") {
		t.Errorf("unnormalized dynamic client ID leaked into metric labels:\n%s", metricsText)
	}

	if !strings.Contains(metricsText, `path="/v1/locks/:resource"`) {
		t.Errorf("expected normalized path /v1/locks/:resource in metrics, got:\n%s", metricsText)
	}
	if !strings.Contains(metricsText, `path="/v1/clients/:id"`) {
		t.Errorf("expected normalized path /v1/clients/:id in metrics, got:\n%s", metricsText)
	}
}

func TestGateway_ClientContextCancellation(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: []string{"http://127.0.0.1:59999"},
			LeaderCacheTTL:       1 * time.Second,
			MaxRedirectAttempts:  2,
			RequestTimeout:       500 * time.Millisecond,
		},
	}

	reg := metrics.NewRegistry()
	disc := NewLeaderDiscovery(cfg.Gateway.CoordinatorEndpoints, 1*time.Second, nil, reg)
	router := NewRouter(cfg, disc, reg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel context before request execution

	req := httptest.NewRequest(http.MethodGet, "/v1/locks", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	router.ProxyRequest(rec, req)

	if rec.Code != 499 {
		t.Fatalf("expected 499 Client Closed Request on cancelled context, got %d: %s", rec.Code, rec.Body.String())
	}
}

