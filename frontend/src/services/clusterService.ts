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

  async stopRenewal(resource_name: string): Promise<void> {
    await apiClient.post('/renewal/stop', { resource_name });
  },
};
