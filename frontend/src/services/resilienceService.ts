import { apiClient } from './api';

export interface SubsystemHealth {
  name: string;
  status: 'HEALTHY' | 'DEGRADED' | 'UNHEALTHY';
  message: string;
  details?: Record<string, any>;
  checked_at: string;
}

export interface SystemHealthReport {
  overall_status: 'HEALTHY' | 'DEGRADED' | 'UNHEALTHY';
  node_id: string;
  service_name: string;
  raft_quorum_available: boolean;
  leader_available: boolean;
  database_available: boolean;
  subsystems: Record<string, SubsystemHealth>;
  timestamp: string;
}

export interface CoordinatorNodeStatus {
  node_id: string;
  endpoint: string;
  reachable: boolean;
  is_leader: boolean;
  role: string;
}

export interface ClusterRecoveryReport {
  total_nodes: number;
  available_nodes: string[];
  current_leader_id: string;
  current_leader_url: string;
  quorum_available: boolean;
  quorum_state: string;
  coordinators: CoordinatorNodeStatus[];
  generated_at: string;
}

export interface SnapshotDiagnosticsReport {
  node_id: string;
  snapshot_available: boolean;
  last_snapshot_index: number;
  last_snapshot_term: number;
  snapshot_threshold: number;
  snapshot_interval: string;
  safe_snapshot_storage: boolean;
  generated_at: string;
}

export interface FailureEvent {
  event_id: string;
  event_type: string;
  event_category: string;
  resource_id?: string;
  owner_id?: string;
  operation_status: string;
  recorded_at: string;
  error_code?: string;
}

export const resilienceService = {
  // Fetch unified system health diagnostics
  getDiagnostics: async (): Promise<SystemHealthReport> => {
    const res = await apiClient.get<{ success: boolean; data: SystemHealthReport }>('/recovery/diagnostics');
    return res.data.data;
  },

  // Fetch cluster coordinator topology and quorum state
  getClusterRecovery: async (): Promise<ClusterRecoveryReport> => {
    const res = await apiClient.get<{ success: boolean; data: ClusterRecoveryReport }>('/recovery/cluster');
    return res.data.data;
  },

  // Fetch Raft snapshot and BoltDB compaction status
  getSnapshots: async (): Promise<SnapshotDiagnosticsReport> => {
    const res = await apiClient.get<{ success: boolean; data: SnapshotDiagnosticsReport }>('/recovery/snapshots');
    return res.data.data;
  },

  // Fetch historical recovery and topology failure events from the audit trail
  getFailureHistory: async (): Promise<FailureEvent[]> => {
    try {
      const res = await apiClient.get<{ success: boolean; data: { events: FailureEvent[] } }>(
        '/audit/events?limit=15'
      );
      return res.data.data.events || [];
    } catch {
      return [];
    }
  },
};

