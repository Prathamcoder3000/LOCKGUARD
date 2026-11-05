package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"lockguard/backend/internal/metrics"
)

var (
	ErrLeaderNotFound   = errors.New("no authoritative raft leader found across coordinator endpoints")
	ErrAllEndpointsDown = errors.New("all coordinator endpoints are unreachable")
)

// LeaderDiscovery probes coordinator endpoints and maintains a cached leader HTTP URL.
type LeaderDiscovery struct {
	mu             sync.RWMutex
	endpoints      []string
	cacheTTL       time.Duration
	httpClient     *http.Client
	metrics        *metrics.Registry

	cachedLeader   string
	cacheExpiresAt time.Time
	nodeToEndpoint map[string]string // coordinator NodeID -> HTTP Base URL
}

type clusterStatusEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		NodeID        string `json:"node_id"`
		State         string `json:"state"`
		IsLeader      bool   `json:"is_leader"`
		LeaderID      string `json:"leader_id"`
		LeaderAddress string `json:"leader_address"`
	} `json:"data"`
}

// NewLeaderDiscovery constructs a new discovery service.
func NewLeaderDiscovery(endpoints []string, cacheTTL time.Duration, httpClient *http.Client, reg *metrics.Registry) *LeaderDiscovery {
	cleanEndpoints := make([]string, 0, len(endpoints))
	for _, ep := range endpoints {
		trimmed := strings.TrimRight(strings.TrimSpace(ep), "/")
		if trimmed != "" {
			if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
				trimmed = "http://" + trimmed
			}
			cleanEndpoints = append(cleanEndpoints, trimmed)
		}
	}

	if httpClient == nil {
		httpClient = &http.Client{Timeout: 3 * time.Second}
	}
	if reg == nil {
		reg = metrics.DefaultRegistry
	}

	return &LeaderDiscovery{
		endpoints:      cleanEndpoints,
		cacheTTL:       cacheTTL,
		httpClient:     httpClient,
		metrics:        reg,
		nodeToEndpoint: make(map[string]string),
	}
}

// GetLeader returns the currently cached leader base URL or discovers a fresh one.
func (d *LeaderDiscovery) GetLeader(ctx context.Context) (string, error) {
	d.mu.RLock()
	if d.cachedLeader != "" && time.Now().Before(d.cacheExpiresAt) {
		leader := d.cachedLeader
		d.mu.RUnlock()
		d.metrics.IncCounter("lockguard_gateway_leader_resolutions_total", map[string]string{"status": "hit"})
		return leader, nil
	}
	d.mu.RUnlock()

	return d.Discover(ctx)
}

// InvalidateLeader clears the cached leader URL forcing next resolution to probe coordinators.
func (d *LeaderDiscovery) InvalidateLeader() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cachedLeader = ""
	d.cacheExpiresAt = time.Time{}
}

// SetLeaderByID attempts to update the cached leader using a reported NodeID or host.
func (d *LeaderDiscovery) SetLeaderByID(nodeID string) bool {
	if strings.TrimSpace(nodeID) == "" {
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// 1. Direct match in nodeToEndpoint
	if ep, found := d.nodeToEndpoint[nodeID]; found && ep != "" {
		d.cachedLeader = ep
		d.cacheExpiresAt = time.Now().Add(d.cacheTTL)
		d.metrics.IncCounter("lockguard_gateway_leader_resolutions_total", map[string]string{"status": "redirect_mapped"})
		return true
	}

	// 2. Substring match against configured endpoints
	for _, ep := range d.endpoints {
		if strings.Contains(ep, nodeID) {
			d.nodeToEndpoint[nodeID] = ep
			d.cachedLeader = ep
			d.cacheExpiresAt = time.Now().Add(d.cacheTTL)
			d.metrics.IncCounter("lockguard_gateway_leader_resolutions_total", map[string]string{"status": "redirect_matched"})
			return true
		}
	}

	return false
}

// Discover probes all endpoints to identify the active Raft leader.
func (d *LeaderDiscovery) Discover(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Double check cache under write lock
	if d.cachedLeader != "" && time.Now().Before(d.cacheExpiresAt) {
		d.metrics.IncCounter("lockguard_gateway_leader_resolutions_total", map[string]string{"status": "hit"})
		return d.cachedLeader, nil
	}

	var foundLeaderURL string
	var reportedLeaderID string
	var reportedLeaderAddr string
	probeSuccessCount := 0

	for _, ep := range d.endpoints {
		statusURL := ep + "/v1/cluster/status"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
		if err != nil {
			continue
		}

		resp, err := d.httpClient.Do(req)
		if err != nil {
			continue
		}
		probeSuccessCount++

		var env clusterStatusEnvelope
		err = json.NewDecoder(resp.Body).Decode(&env)
		resp.Body.Close()

		if err == nil && env.Success {
			nodeID := env.Data.NodeID
			if nodeID != "" {
				d.nodeToEndpoint[nodeID] = ep
			}

			if env.Data.IsLeader {
				foundLeaderURL = ep
				break
			}

			if env.Data.LeaderID != "" {
				reportedLeaderID = env.Data.LeaderID
				reportedLeaderAddr = env.Data.LeaderAddress
			}
		}
	}

	// If direct leader was found
	if foundLeaderURL != "" {
		d.cachedLeader = foundLeaderURL
		d.cacheExpiresAt = time.Now().Add(d.cacheTTL)
		d.metrics.IncCounter("lockguard_gateway_leader_resolutions_total", map[string]string{"status": "discovered"})
		return foundLeaderURL, nil
	}

	// If a follower gave us a valid leader ID and we know its endpoint
	if reportedLeaderID != "" {
		if ep, ok := d.nodeToEndpoint[reportedLeaderID]; ok {
			d.cachedLeader = ep
			d.cacheExpiresAt = time.Now().Add(d.cacheTTL)
			d.metrics.IncCounter("lockguard_gateway_leader_resolutions_total", map[string]string{"status": "discovered_indirect"})
			return ep, nil
		}

		// Try matching by host in reportedLeaderAddr (e.g. coordinator-a:7000)
		for _, ep := range d.endpoints {
			u, err := url.Parse(ep)
			if err == nil {
				if reportedLeaderID != "" && strings.Contains(u.Host, reportedLeaderID) {
					d.nodeToEndpoint[reportedLeaderID] = ep
					d.cachedLeader = ep
					d.cacheExpiresAt = time.Now().Add(d.cacheTTL)
					d.metrics.IncCounter("lockguard_gateway_leader_resolutions_total", map[string]string{"status": "discovered_host"})
					return ep, nil
				}
				if reportedLeaderAddr != "" {
					parts := strings.Split(reportedLeaderAddr, ":")
					if len(parts) > 0 && strings.Contains(u.Host, parts[0]) {
						d.nodeToEndpoint[reportedLeaderID] = ep
						d.cachedLeader = ep
						d.cacheExpiresAt = time.Now().Add(d.cacheTTL)
						d.metrics.IncCounter("lockguard_gateway_leader_resolutions_total", map[string]string{"status": "discovered_addr"})
						return ep, nil
					}
				}
			}
		}
	}

	d.metrics.IncCounter("lockguard_gateway_leader_resolutions_total", map[string]string{"status": "miss"})
	if probeSuccessCount == 0 {
		return "", ErrAllEndpointsDown
	}

	return "", ErrLeaderNotFound
}

// Endpoints returns the list of configured coordinator endpoints.
func (d *LeaderDiscovery) Endpoints() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	res := make([]string, len(d.endpoints))
	copy(res, d.endpoints)
	return res
}
