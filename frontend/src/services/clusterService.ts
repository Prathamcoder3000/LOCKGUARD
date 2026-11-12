import { apiClient } from './api';

export interface HealthStatus {
  status: string;
  service: string;
  version: string;
  node_id?: string;
  timestamp: string;
}

export interface ReadinessStatus {
  status: string;
  service: string;
  node_id: string;
  components: Record<string, string>;
  timestamp: string;
}

export interface ClusterStatus {
  service: string;
  node_id: string;
  environment: string;
  raft_cluster: string[];
  quorum_rule: string;
  design_principle: string;
  storage_path: string;
  subsystems: Record<string, string>;
  timestamp: string;
}

export interface LockRecord {
  resource_name: string;
  owner_id: string;
  fencing_token: number;
  lease_version: number;
  acquired_at_unix_nano: number;
  expires_at_unix_nano: number;
  status: string;
}

export interface ClientSession {
  client_id: string;
  owner_id: string;
  session_id: string;
  ttl_seconds: number;
  created_at: string;
  last_heartbeat: string;
  expires_at: string;
  active: boolean;
  registered_locks: string[];
}

export interface RenewalStatus {
  resource_name: string;
  owner_id: string;
  client_id: string;
  session_id: string;
  fencing_token: number;
  lease_version: number;
  expires_at_unix_nano: number;
  next_renewal_at: string;
  active: boolean;
  last_attempt_status: string;
  last_error?: string;
  attempt_count: number;
}

export const clusterService = {
  async getHealth(): Promise<HealthStatus> {
    const res = await apiClient.get<{ success: boolean; data: HealthStatus }>('/health');
    return res.data.data;
  },

  async getReadiness(): Promise<ReadinessStatus> {
    const res = await apiClient.get<{ success: boolean; data: ReadinessStatus }>('/ready');
    return res.data.data;
  },

  async getClusterStatus(): Promise<ClusterStatus> {
    const res = await apiClient.get<{ success: boolean; data: ClusterStatus }>('/cluster/status');
    return res.data.data;
  },

  async getLocks(): Promise<LockRecord[]> {
    const res = await apiClient.get<{ success: boolean; data: { locks: LockRecord[]; count: number } }>('/locks');
    return res.data.data?.locks || [];
  },

  async acquireLock(payload: { resource_name: string; owner_id: string; ttl_seconds: number }): Promise<LockRecord> {
    const res = await apiClient.post<{ success: boolean; data: LockRecord }>('/locks/acquire', {
      request_id: `req-ui-${Date.now()}`,
      ...payload,
    });
    return res.data.data;
  },

  async releaseLock(payload: { resource_name: string; owner_id: string; fencing_token: number }): Promise<void> {
    await apiClient.post('/locks/release', {
      request_id: `req-ui-rel-${Date.now()}`,
      ...payload,
    });
  },

  async getClientSessions(): Promise<ClientSession[]> {
    const res = await apiClient.get<{ success: boolean; data: { sessions: ClientSession[]; count: number } }>('/clients');
    return res.data.data?.sessions || [];
  },

  async registerClient(payload: { client_id: string; owner_id: string; ttl_seconds: number }): Promise<ClientSession> {
    const res = await apiClient.post<{ success: boolean; data: ClientSession }>('/clients/register', payload);
    return res.data.data;
  },

  async sendHeartbeat(payload: { client_id: string; session_id: string }): Promise<ClientSession> {
    const res = await apiClient.post<{ success: boolean; data: ClientSession }>('/clients/heartbeat', payload);
    return res.data.data;
  },

  async getRenewals(): Promise<RenewalStatus[]> {
    const res = await apiClient.get<{ success: boolean; data: { renewals: RenewalStatus[]; count: number } }>('/renewal/status');
    return res.data.data?.renewals || [];
  },

  async startRenewal(payload: {
    resource_name: string;
    owner_id: string;
    client_id: string;
    session_id: string;
    fencing_token: number;
    lease_version: number;
    expires_at_unix_nano: number;
    ttl_seconds: number;
  }): Promise<RenewalStatus> {
    const res = await apiClient.post<{ success: boolean; data: RenewalStatus }>('/renewal/start', payload);
    return res.data.data;
  },

  async stopRenewal(resource_name: string, session_id?: string): Promise<void> {
    await apiClient.post('/renewal/stop', session_id ? { resource_name, session_id } : { resource_name });
  },

  async adminEmergencyUnlock(resource_name: string): Promise<void> {
    await apiClient.post('/admin/locks/emergency-unlock', { resource_name });
  },

  async adminStepdown(): Promise<{ message: string; node_id?: string }> {
    const res = await apiClient.post<{ success: boolean; data: { message: string; node_id?: string } }>('/admin/cluster/stepdown');
    return res.data.data;
  },

  async getConfig(): Promise<ClusterRuntimeConfig> {
    const res = await apiClient.get<{ success: boolean; data: ClusterRuntimeConfig }>('/cluster/config');
    return res.data.data;
  },

  async getBenchmarkRuns(limit = 20): Promise<BenchmarkListResponse> {
    const res = await apiClient.get<{ success: boolean; data: BenchmarkListResponse }>(`/benchmarks/runs?limit=${limit}`);
    return res.data.data;
  },

  async runBenchmark(payload: {
    title?: string;
    scenario: string;
    concurrency_level: number;
    total_operations: number;
  }): Promise<BenchmarkRun> {
    const res = await apiClient.post<{ success: boolean; data: BenchmarkRun }>('/benchmarks/run', payload);
    return res.data.data;
  },
};

export interface ClusterRuntimeConfig {
  consensus_engine: {
    protocol: string;
    storage_engine: string;
    quorum_rule: string;
    heartbeat_timeout_ms: number;
    election_timeout_ms: number;
    snapshot_interval_sec: number;
    snapshot_threshold: number;
    mtls_enabled: boolean;
    current_leader_id: string;
    current_leader_url: string;
    coordinators_endpoints: string[];
  };
  lease_policy: {
    default_ttl_sec: number;
    min_ttl_sec: number;
    max_ttl_sec: number;
    heartbeat_interval_ms: number;
    heartbeat_timeout_sec: number;
    renewal_safety_margin: string;
  };
  security: {
    auth_enabled: boolean;
    jwt_algorithm: string;
    jwt_expiration_hours: number;
    jwt_secret_configured: boolean;
    rbac_roles: string[];
  };
  runtime: {
    environment: string;
    service_name: string;
    version: string;
    api_listen: string;
    database_target: string;
    protected_resource_url: string;
    coordinator_count: number;
  };
}

export interface BenchmarkRun {
  id: string;
  title: string;
  scenario: string;
  concurrency_level: number;
  total_operations: number;
  target_cluster: string;
  status: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
  throughput_rps: number;
  p50_latency_ms: number;
  p95_latency_ms: number;
  p99_latency_ms: number;
  min_latency_ms: number;
  max_latency_ms: number;
  success_count: number;
  error_count: number;
  metrics_summary?: Record<string, any>;
}

export interface BenchmarkReferenceBaseline {
  id: string;
  title: string;
  scenario: string;
  category: string;
  target: string;
  measurement: string;
  baseline: number;
  unit: string;
  source: string;
}

export interface BenchmarkListResponse {
  running: boolean;
  storage_source: string;
  runs: BenchmarkRun[];
  reference_baselines: BenchmarkReferenceBaseline[];
}
