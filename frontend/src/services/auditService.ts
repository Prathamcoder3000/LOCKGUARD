import { apiClient } from './api';

export interface AuditEvent {
  id: number;
  event_id: string;
  event_type: string;
  event_category: string;
  request_id?: string;
  resource_id?: string;
  owner_id?: string;
  client_id?: string;
  fencing_token: number;
  lease_version: number;
  operation_status: 'COMMITTED' | 'REJECTED' | 'FAILED' | 'PENDING' | string;
  error_code?: string;
  coordinator_id: string;
  raft_index: number;
  event_timestamp: string;
  recorded_at: string;
  metadata?: Record<string, any>;
}

export interface AuditFilter {
  event_type?: string;
  event_category?: string;
  resource_id?: string;
  owner_id?: string;
  client_id?: string;
  request_id?: string;
  operation_status?: string;
  start_time?: string;
  end_time?: string;
  limit?: number;
  offset?: number;
}

export interface AuditListResponse {
  events: AuditEvent[];
  total: number;
  limit: number;
  offset: number;
}

export interface AuditHealthStatus {
  status: string;
  subsystem: string;
  stats: {
    queue_size: number;
    queue_capacity: number;
    created_total: number;
    persisted_total: number;
    failed_total: number;
  };
  db_attached: boolean;
  timestamp: string;
}

export interface SubsystemHealth {
  name: string;
  status: 'HEALTHY' | 'DEGRADED' | 'UNHEALTHY';
  message: string;
  details?: Record<string, any>;
  checked_at: string;
}

export interface DiagnosticsReport {
  overall_status: 'HEALTHY' | 'DEGRADED' | 'UNHEALTHY';
  node_id: string;
  service_name: string;
  raft_quorum_available: boolean;
  leader_available: boolean;
  database_available: boolean;
  subsystems: Record<string, SubsystemHealth>;
  timestamp: string;
}

export const auditService = {
  async getEvents(filter: AuditFilter = {}): Promise<AuditListResponse> {
    const params: Record<string, any> = {};
    if (filter.event_type) params.event_type = filter.event_type;
    if (filter.event_category) params.event_category = filter.event_category;
    if (filter.resource_id) params.resource_id = filter.resource_id;
    if (filter.owner_id) params.owner_id = filter.owner_id;
    if (filter.client_id) params.client_id = filter.client_id;
    if (filter.request_id) params.request_id = filter.request_id;
    if (filter.operation_status) params.operation_status = filter.operation_status;
    if (filter.start_time) params.start_time = filter.start_time;
    if (filter.end_time) params.end_time = filter.end_time;
    params.limit = filter.limit ?? 50;
    params.offset = filter.offset ?? 0;

    const res = await apiClient.get<{ success: boolean; data: AuditListResponse }>('/audit/events', { params });
    return res.data.data;
  },

  async getEventByID(eventId: string): Promise<AuditEvent> {
    const res = await apiClient.get<{ success: boolean; data: AuditEvent }>(`/audit/events/${encodeURIComponent(eventId)}`);
    return res.data.data;
  },

  async getHealth(): Promise<AuditHealthStatus> {
    const res = await apiClient.get<{ success: boolean; data: AuditHealthStatus }>('/audit/health');
    return res.data.data;
  },

  async getDiagnostics(): Promise<DiagnosticsReport> {
    const res = await apiClient.get<{ success: boolean; data: DiagnosticsReport }>('/recovery/diagnostics');
    return res.data.data;
  },
};

