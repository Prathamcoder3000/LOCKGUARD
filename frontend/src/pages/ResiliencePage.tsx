import React, { useEffect, useState, useCallback } from 'react';
import { Activity } from 'lucide-react';
import { useAuth } from '../context/AuthContext';
import {
  resilienceService,
  SystemHealthReport,
  ClusterRecoveryReport,
  SnapshotDiagnosticsReport,
  ChaosStatusReport,
  FailureEvent,
} from '../services/resilienceService';
import { ClusterHealth } from '../components/resilience/ClusterHealth';
import { NodeStatus } from '../components/resilience/NodeStatus';
import { SnapshotStatus } from '../components/resilience/SnapshotStatus';
import { RecoveryTimeline } from '../components/resilience/RecoveryTimeline';
import { FailureHistory } from '../components/resilience/FailureHistory';
import { ResilienceMetrics } from '../components/resilience/ResilienceMetrics';
import { ChaosPartitionPanel } from '../components/resilience/ChaosPartitionPanel';

export const ResiliencePage: React.FC = () => {
  const { user } = useAuth();
  const isAdmin = (user?.role || '').toUpperCase() === 'ADMIN';

  const [health, setHealth] = useState<SystemHealthReport | null>(null);
  const [cluster, setCluster] = useState<ClusterRecoveryReport | null>(null);
  const [snapshots, setSnapshots] = useState<SnapshotDiagnosticsReport | null>(null);
  const [chaosStatus, setChaosStatus] = useState<ChaosStatusReport | null>(null);
  const [events, setEvents] = useState<FailureEvent[]>([]);
  const [loading, setLoading] = useState<boolean>(true);
  const [lastUpdated, setLastUpdated] = useState<Date>(new Date());
  const [metricsRefreshKey, setMetricsRefreshKey] = useState<number>(0);

  const fetchData = useCallback(async () => {
    setLoading(true);
    try {
      const [healthData, clusterData, snapshotData, chaosData, eventData] = await Promise.allSettled([
        resilienceService.getDiagnostics(),
        resilienceService.getClusterRecovery(),
        resilienceService.getSnapshots(),
        resilienceService.getChaosStatus(),
        resilienceService.getFailureHistory(),
      ]);

      setHealth(healthData.status === 'fulfilled' ? healthData.value : null);
      setCluster(clusterData.status === 'fulfilled' ? clusterData.value : null);
      setSnapshots(snapshotData.status === 'fulfilled' ? snapshotData.value : null);
      setChaosStatus(chaosData.status === 'fulfilled' ? chaosData.value : null);
      if (eventData.status === 'fulfilled') setEvents(eventData.value);

      setMetricsRefreshKey((k) => k + 1);
      setLastUpdated(new Date());
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchData();
    const interval = setInterval(fetchData, 10000);
    return () => clearInterval(interval);
  }, [fetchData]);

  return (
    <div className="space-y-6 pb-12">
      {/* Header */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div>
          <div className="flex items-center space-x-2">
            <div className="p-2 rounded-lg bg-blue-600 text-white">
              <Activity className="h-5 w-5" />
            </div>
            <h1 className="text-2xl font-bold tracking-tight text-slate-900">
              Resilience &amp; Disaster Recovery
            </h1>
          </div>
          <p className="text-sm text-slate-500 mt-1">
            Real-time Raft consensus quorum status, leader failover recovery, and chaos engineering metrics
          </p>
        </div>

        <div className="flex items-center space-x-3 text-xs text-slate-500">
          <span className="font-mono">
            Auto-refresh (10s) · Updated {lastUpdated.toLocaleTimeString()}
          </span>
        </div>
      </div>

      {/* Main Cluster Health & Quorum Card */}
      <ClusterHealth
        health={health}
        cluster={cluster}
        loading={loading}
        onRefresh={fetchData}
      />

      {/* Coordinator Node Topology Cards */}
      <NodeStatus coordinators={cluster?.coordinators || []} />

      {/* Prometheus Resilience Gauges */}
      <ResilienceMetrics refreshKey={metricsRefreshKey} />

      {/* Controlled Chaos Engineering & Network Partition Injection */}
      <ChaosPartitionPanel
        cluster={cluster}
        chaosStatus={chaosStatus}
        isAdmin={isAdmin}
        onActionCompleted={fetchData}
      />

      {/* Failover Lifecycle Flowchart / Steps */}
      <RecoveryTimeline />

      {/* Raft Snapshot & Compaction Status */}
      <SnapshotStatus
        snapshot={snapshots}
        isAdmin={isAdmin}
        onActionCompleted={fetchData}
      />

      {/* Historical Audit of Failures & Chaos Tests */}
      <FailureHistory events={events} />
    </div>
  );
};
