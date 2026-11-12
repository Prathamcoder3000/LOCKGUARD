package metrics

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Registry manages in-memory Prometheus metrics for LockGuard Phase 2.
type Registry struct {
	mu sync.RWMutex

	// Counters with labels: metricName -> labelKey -> count
	counters map[string]map[string]*uint64

	// Gauges: metricName -> labelKey -> value
	gauges map[string]map[string]*int64

	// Metric descriptions for HELP / TYPE text
	descriptions map[string]metricMeta
}

type metricMeta struct {
	help       string
	metricType string
}

// DefaultRegistry is the singleton metrics registry.
var DefaultRegistry = NewRegistry()

// NewRegistry initializes an empty metrics registry with predefined LockGuard metrics.
func NewRegistry() *Registry {
	r := &Registry{
		counters:     make(map[string]map[string]*uint64),
		gauges:       make(map[string]map[string]*int64),
		descriptions: make(map[string]metricMeta),
	}

	r.registerMeta("lockguard_gateway_requests_total", "counter", "Total number of HTTP requests processed by the API gateway.")
	r.registerMeta("lockguard_gateway_leader_resolutions_total", "counter", "Total leader resolutions attempted by the gateway.")
	r.registerMeta("lockguard_active_client_sessions", "gauge", "Number of currently active client heartbeat sessions.")
	r.registerMeta("lockguard_heartbeat_received_total", "counter", "Total heartbeat pings received from clients.")
	r.registerMeta("lockguard_heartbeat_timeouts_total", "counter", "Total client sessions expired due to missed heartbeats.")
	r.registerMeta("lockguard_renewal_attempts_total", "counter", "Total automated lease renewal attempts.")
	r.registerMeta("lockguard_renewals_active", "gauge", "Number of currently active lease renewal workers.")
	r.registerMeta("lockguard_fencing_token_current", "gauge", "Highest fencing token observed or generated across all resources (global bounded gauge).")

	// Phase 3 Persistence and Audit subsystem metrics
	r.registerMeta("lockguard_audit_events_created_total", "counter", "Total audit events generated across application subsystems.")
	r.registerMeta("lockguard_audit_events_persisted_total", "counter", "Total audit events durably written to PostgreSQL.")
	r.registerMeta("lockguard_audit_events_failed_total", "counter", "Total audit events that failed persistence or were dropped under backpressure.")
	r.registerMeta("lockguard_audit_queue_size", "gauge", "Current depth of the in-memory audit event delivery buffer.")
	r.registerMeta("lockguard_audit_delivery_latency_seconds", "gauge", "Latest recorded batch flush latency in seconds.")
	r.registerMeta("lockguard_database_connections", "gauge", "Number of PostgreSQL database connection pool slots by state.")
	r.registerMeta("lockguard_database_health", "gauge", "Availability of the PostgreSQL storage engine (1 = healthy, 0 = down).")
	r.registerMeta("lockguard_database_query_errors_total", "counter", "Total database errors encountered during query or transaction execution.")
	r.registerMeta("lockguard_database_migrations_status", "gauge", "Schema migration completion status (1 = up to date, 0 = pending/error).")

	// Phase 4 Chaos, Fault Tolerance & Resilience metrics
	r.registerMeta("lockguard_chaos_tests_total", "counter", "Total chaos testing fault injections executed.")
	r.registerMeta("lockguard_coordinator_failures_total", "counter", "Total coordinator node failure/crash events observed.")
	r.registerMeta("lockguard_leader_changes_total", "counter", "Total Raft leader transition events observed.")
	r.registerMeta("lockguard_network_partitions_total", "counter", "Total network partition chaos events executed.")
	r.registerMeta("lockguard_recovery_attempts_total", "counter", "Total automatic or orchestrated recovery attempts initiated.")
	r.registerMeta("lockguard_recovery_failures_total", "counter", "Total recovery attempts that failed to restore operational state.")
	r.registerMeta("lockguard_snapshot_restore_total", "counter", "Total Raft snapshot restoration operations executed.")
	r.registerMeta("lockguard_quorum_available", "gauge", "Current Raft consensus majority quorum availability (1 = active, 0 = lost).")
	r.registerMeta("lockguard_cluster_nodes_available", "gauge", "Count of reachable and healthy coordinator nodes in the cluster.")
	r.registerMeta("lockguard_protected_resource_errors_total", "counter", "Total protected resource validation errors (e.g. stale fencing token).")
	r.registerMeta("lockguard_recovery_duration_seconds", "gauge", "Elapsed time in seconds for the most recent system recovery action.")

	// Phase 5 Security Hardening, API Protection & Performance metrics
	r.registerMeta("lockguard_auth_successes_total", "counter", "Total successful client and admin authentications.")
	r.registerMeta("lockguard_auth_failures_total", "counter", "Total failed authentication attempts by reason.")
	r.registerMeta("lockguard_authz_denials_total", "counter", "Total authorization denials and role access rejections.")
	r.registerMeta("lockguard_rate_limit_exceeded_total", "counter", "Total requests rejected under rate-limiting policies.")
	r.registerMeta("lockguard_http_requests_total", "counter", "Total incoming HTTP requests by method and status class.")
	r.registerMeta("lockguard_http_request_duration_seconds", "gauge", "Latest measured HTTP request handling duration in seconds.")
	r.registerMeta("lockguard_active_sessions", "gauge", "Number of currently active registered client heartbeat sessions.")
	r.registerMeta("lockguard_security_violations_total", "counter", "Total security constraint violations detected.")

	return r
}

func (r *Registry) registerMeta(name, mType, help string) {
	r.descriptions[name] = metricMeta{
		help:       help,
		metricType: mType,
	}
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(k)
		sb.WriteString(`="`)
		sb.WriteString(strings.ReplaceAll(labels[k], `"`, `\"`))
		sb.WriteByte('"')
	}
	sb.WriteByte('}')
	return sb.String()
}

// IncCounter increments a labelled counter by 1.
func (r *Registry) IncCounter(name string, labels map[string]string) {
	r.AddCounter(name, labels, 1)
}

// AddCounter adds delta to a labelled counter.
func (r *Registry) AddCounter(name string, labels map[string]string, delta uint64) {
	lblStr := formatLabels(labels)

	r.mu.RLock()
	m, exists := r.counters[name]
	if exists {
		valPtr, valExists := m[lblStr]
		if valExists {
			atomic.AddUint64(valPtr, delta)
			r.mu.RUnlock()
			return
		}
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.counters[name]; !ok {
		r.counters[name] = make(map[string]*uint64)
	}
	if valPtr, ok := r.counters[name][lblStr]; ok {
		atomic.AddUint64(valPtr, delta)
	} else {
		val := delta
		r.counters[name][lblStr] = &val
	}
}

// SetGauge sets an arbitrary int64 value on a gauge.
func (r *Registry) SetGauge(name string, labels map[string]string, val int64) {
	lblStr := formatLabels(labels)

	r.mu.RLock()
	m, exists := r.gauges[name]
	if exists {
		valPtr, valExists := m[lblStr]
		if valExists {
			atomic.StoreInt64(valPtr, val)
			r.mu.RUnlock()
			return
		}
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.gauges[name]; !ok {
		r.gauges[name] = make(map[string]*int64)
	}
	if valPtr, ok := r.gauges[name][lblStr]; ok {
		atomic.StoreInt64(valPtr, val)
	} else {
		newVal := val
		r.gauges[name][lblStr] = &newVal
	}
}

// IncGauge increments a gauge by 1.
func (r *Registry) IncGauge(name string, labels map[string]string) {
	r.AddGauge(name, labels, 1)
}

// DecGauge decrements a gauge by 1.
func (r *Registry) DecGauge(name string, labels map[string]string) {
	r.AddGauge(name, labels, -1)
}

// AddGauge adds delta to a gauge.
func (r *Registry) AddGauge(name string, labels map[string]string, delta int64) {
	lblStr := formatLabels(labels)

	r.mu.RLock()
	m, exists := r.gauges[name]
	if exists {
		valPtr, valExists := m[lblStr]
		if valExists {
			atomic.AddInt64(valPtr, delta)
			r.mu.RUnlock()
			return
		}
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.gauges[name]; !ok {
		r.gauges[name] = make(map[string]*int64)
	}
	if valPtr, ok := r.gauges[name][lblStr]; ok {
		atomic.AddInt64(valPtr, delta)
	} else {
		newVal := delta
		r.gauges[name][lblStr] = &newVal
	}
}

// SetCurrentFencingToken records the highest fencing token as a single global gauge without per-resource labels.
func (r *Registry) SetCurrentFencingToken(token uint64) {
	r.SetGauge("lockguard_fencing_token_current", nil, int64(token))
}

// IncChaosTests records a chaos test execution with bounded fault type label.
func (r *Registry) IncChaosTests(faultType string) {
	r.IncCounter("lockguard_chaos_tests_total", map[string]string{"type": faultType})
}

// IncCoordinatorFailures records a coordinator node failure event.
func (r *Registry) IncCoordinatorFailures(nodeID string) {
	r.IncCounter("lockguard_coordinator_failures_total", map[string]string{"node_id": nodeID})
}

// IncLeaderChanges increments the cluster leader change counter.
func (r *Registry) IncLeaderChanges() {
	r.IncCounter("lockguard_leader_changes_total", nil)
}

// IncNetworkPartitions records a network partition chaos event with topology label.
func (r *Registry) IncNetworkPartitions(topology string) {
	r.IncCounter("lockguard_network_partitions_total", map[string]string{"topology": topology})
}

// IncRecoveryAttempts records a recovery attempt for a specific subsystem.
func (r *Registry) IncRecoveryAttempts(subsystem string) {
	r.IncCounter("lockguard_recovery_attempts_total", map[string]string{"subsystem": subsystem})
}

// IncRecoveryFailures records a failed recovery attempt for a subsystem.
func (r *Registry) IncRecoveryFailures(subsystem string) {
	r.IncCounter("lockguard_recovery_failures_total", map[string]string{"subsystem": subsystem})
}

// IncSnapshotRestore records a snapshot restore event.
func (r *Registry) IncSnapshotRestore(status string) {
	r.IncCounter("lockguard_snapshot_restore_total", map[string]string{"status": status})
}

// SetQuorumAvailable updates the consensus quorum availability gauge (1 = true, 0 = false).
func (r *Registry) SetQuorumAvailable(available bool) {
	val := int64(0)
	if available {
		val = 1
	}
	r.SetGauge("lockguard_quorum_available", nil, val)
}

// SetClusterNodesAvailable updates the count of active cluster coordinators.
func (r *Registry) SetClusterNodesAvailable(count int) {
	r.SetGauge("lockguard_cluster_nodes_available", nil, int64(count))
}

// IncProtectedResourceErrors records a protected resource validation failure.
func (r *Registry) IncProtectedResourceErrors(errType string) {
	r.IncCounter("lockguard_protected_resource_errors_total", map[string]string{"error_type": errType})
}

// SetRecoveryDuration records the duration in milliseconds of the most recent recovery operation.
// Clearing prior action series ensures dashboards summing this gauge always reflect the latest recovery duration.
func (r *Registry) SetRecoveryDuration(action string, durationSeconds float64) {
	ms := int64(math.Round(durationSeconds * 1000))
	if durationSeconds > 0 && ms < 1 {
		ms = 1
	}
	if ms < 0 {
		ms = 0
	}

	lblStr := formatLabels(map[string]string{"action": action})
	r.mu.Lock()
	defer r.mu.Unlock()
	series := make(map[string]*int64, 1)
	v := ms
	series[lblStr] = &v
	r.gauges["lockguard_recovery_duration_seconds"] = series
}

// GetCounterTotal returns the summed counter value across all label series for a metric.
func (r *Registry) GetCounterTotal(name string) uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	series, ok := r.counters[name]
	if !ok {
		return 0
	}
	var total uint64
	for _, ptr := range series {
		total += atomic.LoadUint64(ptr)
	}
	return total
}

// GetLatestGauge returns the current gauge value and its label key (deterministic across sorted keys).
func (r *Registry) GetLatestGauge(name string) (int64, string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	series, ok := r.gauges[name]
	if !ok || len(series) == 0 {
		return 0, ""
	}
	lbls := make([]string, 0, len(series))
	for l := range series {
		lbls = append(lbls, l)
	}
	sort.Strings(lbls)
	last := lbls[len(lbls)-1]
	return atomic.LoadInt64(series[last]), last
}

// IncAuthSuccess records a successful authentication.
func (r *Registry) IncAuthSuccess(role string) {
	r.IncCounter("lockguard_auth_successes_total", map[string]string{"role": strings.ToLower(role)})
}

// IncAuthFailure records an authentication failure.
func (r *Registry) IncAuthFailure(reason string) {
	r.IncCounter("lockguard_auth_failures_total", map[string]string{"reason": reason})
}

// IncAuthzDenial records an unauthorized access attempt.
func (r *Registry) IncAuthzDenial(role string) {
	r.IncCounter("lockguard_authz_denials_total", map[string]string{"role": strings.ToLower(role)})
}

// IncRateLimitExceeded records a rate-limit denial.
func (r *Registry) IncRateLimitExceeded(endpoint string) {
	r.IncCounter("lockguard_rate_limit_exceeded_total", map[string]string{"endpoint": endpoint})
}

// IncHTTPRequest records an incoming HTTP request by method and status class.
func (r *Registry) IncHTTPRequest(method, statusClass string) {
	r.IncCounter("lockguard_http_requests_total", map[string]string{
		"method":       strings.ToUpper(method),
		"status_class": statusClass,
	})
}

// SetActiveSessions records the current count of active client sessions.
func (r *Registry) SetActiveSessions(count int64) {
	r.SetGauge("lockguard_active_sessions", nil, count)
}

// IncSecurityViolation records an application security constraint violation.
func (r *Registry) IncSecurityViolation(violationType string) {
	r.IncCounter("lockguard_security_violations_total", map[string]string{"violation_type": violationType})
}

// Handler returns an http.Handler that emits Prometheus exposition format text.
func (r *Registry) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		r.mu.RLock()
		defer r.mu.RUnlock()

		names := make([]string, 0, len(r.descriptions))
		for name := range r.descriptions {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			meta := r.descriptions[name]
			fmt.Fprintf(w, "# HELP %s %s\n", name, meta.help)
			fmt.Fprintf(w, "# TYPE %s %s\n", name, meta.metricType)

			if meta.metricType == "counter" {
				if series, ok := r.counters[name]; ok {
					lbls := make([]string, 0, len(series))
					for l := range series {
						lbls = append(lbls, l)
					}
					sort.Strings(lbls)
					for _, l := range lbls {
						val := atomic.LoadUint64(series[l])
						fmt.Fprintf(w, "%s%s %d\n", name, l, val)
					}
				}
			} else if meta.metricType == "gauge" {
				if series, ok := r.gauges[name]; ok {
					lbls := make([]string, 0, len(series))
					for l := range series {
						lbls = append(lbls, l)
					}
					sort.Strings(lbls)
					for _, l := range lbls {
						val := atomic.LoadInt64(series[l])
						fmt.Fprintf(w, "%s%s %d\n", name, l, val)
					}
				}
			}
		}
	}
}

