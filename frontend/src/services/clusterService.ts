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
};
