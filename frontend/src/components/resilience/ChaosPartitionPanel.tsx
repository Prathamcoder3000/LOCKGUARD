import React, { useState, useEffect } from 'react';
import {
  Flame,
  WifiOff,
  ShieldCheck,
  AlertTriangle,
  CheckCircle2,
  XCircle,
  Play,
  RefreshCw,
  Lock,
} from 'lucide-react';
import {
  ChaosStatusReport,
  ClusterRecoveryReport,
  resilienceService,
  extractResilienceError,
} from '../../services/resilienceService';

interface ChaosPartitionPanelProps {
  cluster: ClusterRecoveryReport | null;
  chaosStatus: ChaosStatusReport | null;
  isAdmin: boolean;
  onActionCompleted: () => Promise<void> | void;
}

export const ChaosPartitionPanel: React.FC<ChaosPartitionPanelProps> = ({
  cluster,
  chaosStatus,
  isAdmin,
  onActionCompleted,
}) => {
  const [selectedScenario, setSelectedScenario] = useState<string>('leader_stepdown');
  const [chaosConfirmPending, setChaosConfirmPending] = useState<boolean>(false);
  const [chaosExecuting, setChaosExecuting] = useState<boolean>(false);
  const [chaosBanner, setChaosBanner] = useState<{ kind: 'success' | 'error'; text: string } | null>(null);

  const [targetNode, setTargetNode] = useState<string>('');
  const [durationSec, setDurationSec] = useState<number>(30);
  const [partitionConfirmPending, setPartitionConfirmPending] = useState<boolean>(false);
  const [partitionExecuting, setPartitionExecuting] = useState<boolean>(false);
  const [partitionBanner, setPartitionBanner] = useState<{ kind: 'success' | 'error'; text: string } | null>(null);

  const coordinators = cluster?.coordinators ?? [];
  const totalNodes = cluster?.total_nodes && cluster.total_nodes > 0 ? cluster.total_nodes : 3;
  const healthyNodesCount = cluster?.available_nodes?.length ?? 0;
  const majorityQuorum = Math.floor(totalNodes / 2) + 1;
  const activePartition = chaosStatus?.active_partition;
  const partitionedCoord = coordinators.find((c) => c.partitioned);
  const isPartitionActive = Boolean(activePartition?.active || partitionedCoord);
  const activePartitionNodeId = activePartition?.node_id || partitionedCoord?.node_id || '';

  useEffect(() => {
    if (!targetNode && coordinators.length > 0) {
      const follower = coordinators.find((c) => c.reachable && !c.partitioned && !c.is_leader);
      const fallback = coordinators.find((c) => c.reachable && !c.partitioned) || coordinators[0];
      setTargetNode((follower || fallback)?.node_id || '');
    }
  }, [coordinators, targetNode]);

  const scenarios =
    chaosStatus?.available_scenarios && chaosStatus.available_scenarios.length > 0
      ? chaosStatus.available_scenarios
      : [
          {
            id: 'leader_stepdown',
            name: 'Controlled Leader Stepdown',
            description:
              'Transfers Raft leadership from the active leader to a healthy follower while verifying quorum stays intact and recording failover latency.',
            safe: true,
            confirmation_required: true,
          },
          {
            id: 'quorum_read_barrier',
            name: 'Quorum Linearizability & Read Barrier Probe',
            description:
              'Verifies majority quorum connectivity and authoritative lock read consistency across coordinators.',
            safe: true,
            confirmation_required: true,
          },
          {
            id: 'follower_partition_drill',
            name: 'Bounded Follower Partition Drill',
            description:
              'Temporarily isolates one healthy follower for ~1.2s while preserving majority quorum, then heals and measures rejoin time.',
            safe: true,
            confirmation_required: true,
          },
        ];

  const activeScenarioObj = scenarios.find((s) => s.id === selectedScenario) || scenarios[0];

  const selectedCoordObj = coordinators.find((c) => c.node_id === targetNode);
  const healthyAfterPartition = selectedCoordObj?.reachable && !selectedCoordObj?.partitioned ? healthyNodesCount - 1 : healthyNodesCount;
  const partitionSafeToInject =
    !isPartitionActive &&
    Boolean(selectedCoordObj?.reachable) &&
    !selectedCoordObj?.partitioned &&
    totalNodes >= 3 &&
    healthyAfterPartition >= majorityQuorum;

  const handleRunChaosConfirmed = async () => {
    setChaosExecuting(true);
    setChaosBanner(null);
    try {
      const res = await resilienceService.runChaosTest(selectedScenario, true);
      setChaosConfirmPending(false);
      setChaosBanner({
        kind: res.status === 'PASSED' ? 'success' : 'error',
        text: `${res.status}: ${res.summary}`,
      });
      await onActionCompleted();
    } catch (err: any) {
      setChaosBanner({
        kind: 'error',
        text: extractResilienceError(err, 'Failed to execute controlled chaos drill'),
      });
    } finally {
      setChaosExecuting(false);
    }
  };

  const handleInjectPartitionConfirmed = async () => {
    setPartitionExecuting(true);
    setPartitionBanner(null);
    try {
      const res = await resilienceService.injectPartition(targetNode, true, durationSec);
      setPartitionConfirmPending(false);
      setPartitionBanner({
        kind: 'success',
        text: res.message,
      });
      await onActionCompleted();
    } catch (err: any) {
      setPartitionBanner({
        kind: 'error',
        text: extractResilienceError(err, 'Partition injection rejected by safety guard'),
      });
    } finally {
      setPartitionExecuting(false);
    }
  };

  const handleHealPartition = async () => {
    setPartitionExecuting(true);
    setPartitionBanner(null);
    try {
      const res = await resilienceService.healPartition(activePartitionNodeId);
      setPartitionConfirmPending(false);
      setPartitionBanner({
        kind: 'success',
        text: res.message,
      });
      await onActionCompleted();
    } catch (err: any) {
      setPartitionBanner({
        kind: 'error',
        text: extractResilienceError(err, 'Failed to heal network partition'),
      });
    } finally {
      setPartitionExecuting(false);
    }
  };

  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
      {/* Chaos Testing Control Card */}
      <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm flex flex-col justify-between">
        <div>
          <div className="flex items-center justify-between pb-3 border-b border-slate-100">
            <div className="flex items-center space-x-2">
              <div className="rounded-lg bg-orange-50 p-2 text-orange-600">
                <Flame className="h-4 w-4" />
              </div>
              <div>
                <h3 className="text-base font-bold text-slate-900">Controlled Chaos Engineering</h3>
                <p className="text-xs text-slate-500">
                  Explicitly triggered, quorum-safe resilience verification drills
                </p>
              </div>
            </div>
            <div className="flex items-center space-x-2">
              <span className="inline-flex items-center rounded-full bg-slate-100 px-2.5 py-0.5 text-xs font-mono font-semibold text-slate-700">
                Runs: {chaosStatus?.execution_count ?? 0}
              </span>
              <span
                className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-semibold ${
                  chaosExecuting || chaosStatus?.running
                    ? 'bg-amber-100 text-amber-800'
                    : chaosStatus?.last_result?.status === 'PASSED'
                    ? 'bg-emerald-100 text-emerald-800'
                    : chaosStatus?.last_result?.status === 'FAILED'
                    ? 'bg-rose-100 text-rose-800'
                    : 'bg-slate-100 text-slate-600'
                }`}
              >
                {chaosExecuting || chaosStatus?.running
                  ? 'RUNNING'
                  : chaosStatus?.last_result?.status || 'IDLE'}
              </span>
            </div>
          </div>

          {!isAdmin && (
            <div className="mt-3 flex items-center space-x-2 rounded-lg border border-slate-200 bg-slate-50 p-3 text-xs text-slate-600">
              <Lock className="h-4 w-4 text-slate-400 shrink-0" />
              <span>Admin privileges required to trigger chaos drills. Viewing read-only execution telemetry.</span>
            </div>
          )}

          <div className="mt-4 space-y-3">
            <div>
              <label htmlFor="chaos-scenario-select" className="block text-xs font-semibold text-slate-700 mb-1">
                Chaos Scenario
              </label>
              <select
                id="chaos-scenario-select"
                value={selectedScenario}
                onChange={(e) => {
                  setSelectedScenario(e.target.value);
                  setChaosConfirmPending(false);
                }}
                disabled={!isAdmin || chaosExecuting}
                className="w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs font-medium text-slate-800 focus:border-blue-500 focus:outline-none disabled:bg-slate-50 disabled:text-slate-400"
              >
                {scenarios.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </select>
              {activeScenarioObj && (
                <p className="mt-1.5 text-xs text-slate-500 leading-relaxed">{activeScenarioObj.description}</p>
              )}
            </div>

            {chaosConfirmPending ? (
              <div className="rounded-lg border border-amber-200 bg-amber-50 p-3 space-y-2">
                <div className="flex items-start space-x-2 text-xs text-amber-900">
                  <AlertTriangle className="h-4 w-4 text-amber-600 shrink-0 mt-0.5" />
                  <span>
                    Confirm execution of <strong>{activeScenarioObj?.name}</strong>? This runs live consensus checks
                    and increments <span className="font-mono">lockguard_chaos_tests_total</span>.
                  </span>
                </div>
                <div className="flex items-center space-x-2">
                  <button
                    type="button"
                    onClick={handleRunChaosConfirmed}
                    disabled={chaosExecuting}
                    className="inline-flex items-center rounded-lg bg-orange-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-orange-700 disabled:opacity-50"
                  >
                    {chaosExecuting ? (
                      <>
                        <RefreshCw className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                        Executing...
                      </>
                    ) : (
                      <>
                        <CheckCircle2 className="mr-1.5 h-3.5 w-3.5" />
                        Confirm &amp; Execute Chaos Test
                      </>
                    )}
                  </button>
                  <button
                    type="button"
                    onClick={() => setChaosConfirmPending(false)}
                    disabled={chaosExecuting}
                    className="rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50"
                  >
                    Cancel
                  </button>
                </div>
              </div>
            ) : (
              <button
                type="button"
                onClick={() => setChaosConfirmPending(true)}
                disabled={!isAdmin || chaosExecuting || isPartitionActive}
                className="inline-flex items-center rounded-lg bg-slate-900 px-3.5 py-2 text-xs font-semibold text-white hover:bg-slate-800 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                <Play className="mr-1.5 h-3.5 w-3.5" />
                Run Chaos Test
              </button>
            )}

            {chaosBanner && (
              <div
                className={`rounded-lg border p-3 text-xs flex items-start space-x-2 ${
                  chaosBanner.kind === 'success'
                    ? 'border-emerald-200 bg-emerald-50 text-emerald-800'
                    : 'border-rose-200 bg-rose-50 text-rose-800'
                }`}
              >
                {chaosBanner.kind === 'success' ? (
                  <CheckCircle2 className="h-4 w-4 shrink-0 mt-0.5" />
                ) : (
                  <XCircle className="h-4 w-4 shrink-0 mt-0.5" />
                )}
                <span>{chaosBanner.text}</span>
              </div>
            )}

            {chaosStatus?.last_result && (
              <div className="rounded-lg border border-slate-100 bg-slate-50 p-3 text-xs space-y-1">
                <div className="flex items-center justify-between font-semibold text-slate-700">
                  <span>Latest Result: {chaosStatus.last_result.scenario}</span>
                  <span className="font-mono text-slate-500">{chaosStatus.last_result.duration_ms} ms</span>
                </div>
                <p className="text-slate-600">{chaosStatus.last_result.summary}</p>
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Network Partition Injection Control Card */}
      <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm flex flex-col justify-between">
        <div>
          <div className="flex items-center justify-between pb-3 border-b border-slate-100">
            <div className="flex items-center space-x-2">
              <div className="rounded-lg bg-amber-50 p-2 text-amber-600">
                <WifiOff className="h-4 w-4" />
              </div>
              <div>
                <h3 className="text-base font-bold text-slate-900">Controlled Network Partition Testing</h3>
                <p className="text-xs text-slate-500">
                  Isolate one coordinator Raft transport while enforcing majority quorum safety
                </p>
              </div>
            </div>
            <span
              className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-semibold ${
                isPartitionActive ? 'bg-amber-100 text-amber-800' : 'bg-emerald-100 text-emerald-800'
              }`}
            >
              {isPartitionActive ? `PARTITIONED: ${activePartitionNodeId}` : 'NO ACTIVE PARTITION'}
            </span>
          </div>

          <div className="mt-3 flex items-center justify-between rounded-lg border border-slate-100 bg-slate-50 px-3 py-2 text-xs">
            <div className="flex items-center space-x-1.5 text-slate-700">
              <ShieldCheck className="h-4 w-4 text-emerald-600" />
              <span>
                Quorum Safety Gate: <strong>{healthyNodesCount}/{totalNodes}</strong> healthy (min{' '}
                <strong>{majorityQuorum}</strong> required)
              </span>
            </div>
            <span className="font-mono text-slate-500">Injected: {chaosStatus?.partitions_injected ?? 0}</span>
          </div>

          {isPartitionActive ? (
            <div className="mt-4 rounded-lg border border-amber-200 bg-amber-50 p-4 space-y-3">
              <div className="flex items-start space-x-2 text-xs text-amber-900">
                <AlertTriangle className="h-4 w-4 text-amber-600 shrink-0 mt-0.5" />
                <div>
                  <p className="font-semibold">
                    Simulated Partition Active on <span className="font-mono">{activePartitionNodeId}</span>
                  </p>
                  <p className="mt-0.5 text-amber-800">
                    Remaining nodes maintain consensus quorum.
                    {activePartition?.auto_heal_at &&
                      ` Auto-heal scheduled at ${new Date(activePartition.auto_heal_at).toLocaleTimeString()}.`}
                  </p>
                </div>
              </div>
              <button
                type="button"
                onClick={handleHealPartition}
                disabled={!isAdmin || partitionExecuting}
                className="inline-flex items-center rounded-lg bg-emerald-600 px-3.5 py-2 text-xs font-semibold text-white hover:bg-emerald-700 disabled:opacity-50"
              >
                {partitionExecuting ? (
                  <>
                    <RefreshCw className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                    Healing Partition...
                  </>
                ) : (
                  <>
                    <CheckCircle2 className="mr-1.5 h-3.5 w-3.5" />
                    Recover / Heal Partition
                  </>
                )}
              </button>
            </div>
          ) : (
            <div className="mt-4 space-y-3">
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <div>
                  <label htmlFor="partition-target-select" className="block text-xs font-semibold text-slate-700 mb-1">
                    Target Coordinator
                  </label>
                  <select
                    id="partition-target-select"
                    value={targetNode}
                    onChange={(e) => {
                      setTargetNode(e.target.value);
                      setPartitionConfirmPending(false);
                    }}
                    disabled={!isAdmin || partitionExecuting}
                    className="w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs font-mono text-slate-800 focus:border-blue-500 focus:outline-none disabled:bg-slate-50"
                  >
                    {coordinators.map((c) => (
                      <option key={c.node_id} value={c.node_id}>
                        {c.node_id} ({c.is_leader ? 'Leader' : c.reachable ? 'Follower' : 'Offline'})
                      </option>
                    ))}
                  </select>
                </div>
                <div>
                  <label htmlFor="partition-duration-select" className="block text-xs font-semibold text-slate-700 mb-1">
                    Auto-Heal Safety TTL
                  </label>
                  <select
                    id="partition-duration-select"
                    value={durationSec}
                    onChange={(e) => setDurationSec(Number(e.target.value))}
                    disabled={!isAdmin || partitionExecuting}
                    className="w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs font-mono text-slate-800 focus:border-blue-500 focus:outline-none disabled:bg-slate-50"
                  >
                    <option value={15}>15 seconds</option>
                    <option value={30}>30 seconds</option>
                    <option value={45}>45 seconds</option>
                    <option value={60}>60 seconds</option>
                  </select>
                </div>
              </div>

              {!partitionSafeToInject && targetNode && (
                <div className="rounded-lg border border-rose-200 bg-rose-50 p-2.5 text-xs text-rose-800 flex items-center space-x-2">
                  <ShieldCheck className="h-4 w-4 text-rose-600 shrink-0" />
                  <span>
                    Safety Lock: Isolating <strong>{targetNode}</strong> would drop healthy nodes below majority quorum (
                    {healthyAfterPartition}/{totalNodes} &lt; {majorityQuorum}).
                  </span>
                </div>
              )}

              {partitionConfirmPending ? (
                <div className="rounded-lg border border-amber-200 bg-amber-50 p-3 space-y-2">
                  <div className="flex items-start space-x-2 text-xs text-amber-900">
                    <AlertTriangle className="h-4 w-4 text-amber-600 shrink-0 mt-0.5" />
                    <span>
                      Confirm simulated partition of <strong className="font-mono">{targetNode}</strong>? Remaining{' '}
                      {healthyAfterPartition}/{totalNodes} nodes preserve quorum; auto-heals after {durationSec}s.
                    </span>
                  </div>
                  <div className="flex items-center space-x-2">
                    <button
                      type="button"
                      onClick={handleInjectPartitionConfirmed}
                      disabled={partitionExecuting || !partitionSafeToInject}
                      className="inline-flex items-center rounded-lg bg-amber-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-amber-700 disabled:opacity-50"
                    >
                      {partitionExecuting ? (
                        <>
                          <RefreshCw className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                          Isolating...
                        </>
                      ) : (
                        <>
                          <WifiOff className="mr-1.5 h-3.5 w-3.5" />
                          Confirm Inject Partition
                        </>
                      )}
                    </button>
                    <button
                      type="button"
                      onClick={() => setPartitionConfirmPending(false)}
                      disabled={partitionExecuting}
                      className="rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50"
                    >
                      Cancel
                    </button>
                  </div>
                </div>
              ) : (
                <button
                  type="button"
                  onClick={() => setPartitionConfirmPending(true)}
                  disabled={!isAdmin || partitionExecuting || !partitionSafeToInject}
                  className="inline-flex items-center rounded-lg bg-amber-600 px-3.5 py-2 text-xs font-semibold text-white hover:bg-amber-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
                >
                  <WifiOff className="mr-1.5 h-3.5 w-3.5" />
                  Inject Partition
                </button>
              )}
            </div>
          )}

          {partitionBanner && (
            <div
              className={`mt-3 rounded-lg border p-3 text-xs flex items-start space-x-2 ${
                partitionBanner.kind === 'success'
                  ? 'border-emerald-200 bg-emerald-50 text-emerald-800'
                  : 'border-rose-200 bg-rose-50 text-rose-800'
              }`}
            >
              {partitionBanner.kind === 'success' ? (
                <CheckCircle2 className="h-4 w-4 shrink-0 mt-0.5" />
              ) : (
                <XCircle className="h-4 w-4 shrink-0 mt-0.5" />
              )}
              <span>{partitionBanner.text}</span>
            </div>
          )}

          {chaosStatus?.partition_history && chaosStatus.partition_history.length > 0 && (
            <div className="mt-4 pt-3 border-t border-slate-100">
              <div className="flex items-center justify-between mb-2">
                <span className="text-[11px] font-semibold uppercase tracking-wide text-slate-500">
                  Recent Partition Timeline
                </span>
                <span className="text-[10px] font-mono text-slate-400">
                  Last {Math.min(5, chaosStatus.partition_history.length)} events
                </span>
              </div>
              <div className="space-y-1.5 max-h-36 overflow-y-auto">
                {chaosStatus.partition_history.slice(0, 5).map((item, idx) => (
                  <div
                    key={item.id || `${item.timestamp}-${idx}`}
                    className="flex items-center justify-between rounded border border-slate-100 bg-slate-50 px-2.5 py-1.5 text-[11px]"
                  >
                    <div className="flex items-center space-x-2">
                      <span
                        className={`rounded px-1.5 py-0.5 font-mono text-[10px] font-semibold ${
                          item.action === 'INJECTED'
                            ? 'bg-rose-100 text-rose-700'
                            : 'bg-emerald-100 text-emerald-700'
                        }`}
                      >
                        {item.action}
                      </span>
                      <span className="font-mono text-slate-800">{item.node_id || 'cluster'}</span>
                      <span className="text-slate-500">
                        {item.topology ? `${item.topology} · ` : ''}
                        {item.available_nodes}/{item.total_nodes} nodes
                        {item.recovery_duration_ms && item.recovery_duration_ms > 0
                          ? ` (${item.recovery_duration_ms} ms)`
                          : ''}
                      </span>
                    </div>
                    <span className="font-mono text-[10px] text-slate-400">
                      {new Date(item.timestamp).toLocaleTimeString()}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
};
