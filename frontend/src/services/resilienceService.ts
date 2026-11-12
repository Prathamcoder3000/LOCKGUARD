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
  partitioned?: boolean;
  applied_index?: number;
  commit_index?: number;
  last_index?: number;
  current_term?: number;
  raft_bind_addr?: string;
  raft_advertise_addr?: string;
  data_dir?: string;
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

export interface SnapshotInfo {
  id: string;
  index: number;
  term: number;
  size: number;
  resources_count: number;
  idempotency_count: number;
  last_applied_unix_nano: number;
  valid: boolean;
}

export interface SnapshotDiagnosticsReport {
  node_id: string;
  leader_id?: string;
  snapshot_available: boolean;
  last_snapshot_index: number;
  last_snapshot_term: number;
  snapshot_threshold: number;
  snapshot_interval: string;
  safe_snapshot_storage: boolean;
  snapshots?: SnapshotInfo[];
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

export interface ChaosScenarioDescriptor {
  id: string;
  name: string;
  description: string;
  safe: boolean;
  confirmation_required: boolean;
}

export interface ChaosExecutionResult {
  id: string;
  scenario: string;
  fault_type: string;
  target: string;
  status: 'PASSED' | 'FAILED' | string;
  summary: string;
  started_at: string;
  completed_at: string;
  duration_ms: number;
  quorum_preserved: boolean;
  details?: Record<string, any>;
}

export interface ActivePartitionState {
  active: boolean;
  node_id?: string;
  endpoint?: string;
  topology?: string;
  injected_at?: string;
  auto_heal_at?: string;
}

export interface RecoveryDurationRecord {
  action: string;
  target?: string;
  duration_ms: number;
  recorded_at: string;
}

export interface PartitionActionRecord {
  id: string;
  action: 'INJECTED' | 'HEALED' | string;
  node_id: string;
  topology?: string;
  recovery_duration_ms?: number;
  quorum_available: boolean;
  available_nodes: number;
  total_nodes: number;
  timestamp: string;
}

export interface LeaderTransitionRecord {
  previous_leader: string;
  new_leader: string;
  timestamp: string;
}

export interface ChaosStatusReport {
  enabled: boolean;
  environment: string;
  running: boolean;
  execution_count: number;
  partitions_injected: number;
  snapshot_restores: number;
  leader_changes: number;
  last_recovery?: RecoveryDurationRecord;
  active_partition: ActivePartitionState;
  last_result?: ChaosExecutionResult;
  history: ChaosExecutionResult[];
  partition_history?: PartitionActionRecord[];
  leader_history?: LeaderTransitionRecord[];
  available_scenarios: ChaosScenarioDescriptor[];
  generated_at: string;
}

export interface PartitionActionResult {
  action: 'INJECTED' | 'HEALED' | string;
  node_id: string;
  partitioned: boolean;
  topology?: string;
  recovery_duration_ms?: number;
  quorum_available: boolean;
  available_nodes: number;
  total_nodes: number;
  majority_quorum: number;
  auto_heal_at?: string;
  message: string;
}

export interface SnapshotActionResult {
  action: 'CREATED' | 'RESTORED' | string;
  snapshot?: SnapshotInfo;
  already_up_to_date?: boolean;
  snapshots?: SnapshotInfo[];
  restored_by_leader?: string;
  duration_ms?: number;
  message: string;
}

export function extractResilienceError(err: any, fallback: string): string {
  const apiMsg = err?.response?.data?.error?.message;
  if (typeof apiMsg === 'string' && apiMsg.trim()) {
    return apiMsg;
  }
  if (typeof err?.message === 'string' && err.message.trim()) {
    return err.message;
  }
  return fallback;
}

export const resilienceService = {
  // Fetch unified system health diagnostics (503 still returns valid diagnostic report under quorum loss)
  getDiagnostics: async (): Promise<SystemHealthReport> => {
    try {
      const res = await apiClient.get<{ success: boolean; data: SystemHealthReport }>('/recovery/diagnostics');
      return res.data.data;
    } catch (err: any) {
      if (err?.response?.status === 503 && err?.response?.data?.data) {
        return err.response.data.data;
      }
      throw err;
    }
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

  // Fetch controlled chaos testing & partition state
  getChaosStatus: async (): Promise<ChaosStatusReport> => {
    const res = await apiClient.get<{ success: boolean; data: ChaosStatusReport }>('/recovery/chaos');
    return res.data.data;
  },

  // Execute an admin-confirmed controlled chaos drill
  runChaosTest: async (scenario: string, confirm: boolean): Promise<ChaosExecutionResult> => {
    const res = await apiClient.post<{ success: boolean; data: ChaosExecutionResult }>('/recovery/chaos/run', {
      scenario,
      confirm,
    });
    return res.data.data;
  },

  // Inject a quorum-safe simulated network partition against a coordinator node
  injectPartition: async (
    targetNodeId: string,
    confirm: boolean,
    durationSeconds = 30
  ): Promise<PartitionActionResult> => {
    const res = await apiClient.post<{ success: boolean; data: PartitionActionResult }>('/recovery/partition/inject', {
      target_node_id: targetNodeId,
      confirm,
      duration_seconds: durationSeconds,
    });
    return res.data.data;
  },

  // Heal an active simulated network partition and measure rejoin recovery duration
  healPartition: async (targetNodeId = ''): Promise<PartitionActionResult> => {
    const res = await apiClient.post<{ success: boolean; data: PartitionActionResult }>('/recovery/partition/heal', {
      target_node_id: targetNodeId,
    });
    return res.data.data;
  },

  // Trigger an on-demand Raft snapshot on the active leader
  createSnapshot: async (): Promise<SnapshotActionResult> => {
    const res = await apiClient.post<{ success: boolean; data: SnapshotActionResult }>('/recovery/snapshots/create', {});
    return res.data.data;
  },

  // Restore a persisted Raft snapshot cluster-wide after explicit admin confirmation
  restoreSnapshot: async (snapshotId: string, confirm: boolean): Promise<SnapshotActionResult> => {
    const res = await apiClient.post<{ success: boolean; data: SnapshotActionResult }>('/recovery/snapshots/restore', {
      snapshot_id: snapshotId,
      confirm,
    });
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
