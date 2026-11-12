import React, { useEffect, useState } from 'react';
import { Zap, Activity, Clock, ShieldCheck, Play, CheckCircle2, AlertTriangle, Database } from 'lucide-react';
import { apiClient } from '../services/api';
import { useAuth } from '../context/AuthContext';
import {
  BenchmarkReferenceBaseline,
  BenchmarkRun,
  clusterService,
} from '../services/clusterService';

interface LiveProbeResult {
  iteration: number;
  latencyMs: number;
  status: number;
}

export const BenchmarksPage: React.FC = () => {
  const { user } = useAuth();
  const isAdmin = (user?.role || '').toUpperCase() === 'ADMIN';

  // Live concurrent Raft benchmark suite state (PostgreSQL-persisted)
  const [scenario, setScenario] = useState<'acquire_release_cycle' | 'contention_acquire' | 'cluster_read_barrier'>('acquire_release_cycle');
  const [concurrency, setConcurrency] = useState<number>(4);
  const [totalOps, setTotalOps] = useState<number>(40);
  const [title, setTitle] = useState<string>('');
  const [runningBenchmark, setRunningBenchmark] = useState<boolean>(false);
  const [loadingRuns, setLoadingRuns] = useState<boolean>(true);
  const [runs, setRuns] = useState<BenchmarkRun[]>([]);
  const [referenceBaselines, setReferenceBaselines] = useState<BenchmarkReferenceBaseline[]>([]);
  const [storageSource, setStorageSource] = useState<string>('postgres');
  const [benchmarkError, setBenchmarkError] = useState<string>('');
  const [latestRun, setLatestRun] = useState<BenchmarkRun | null>(null);

  // Lightweight HTTP Gateway round-trip probe
  const [probing, setProbing] = useState<boolean>(false);
  const [probeResults, setProbeResults] = useState<LiveProbeResult[]>([]);
  const [probeStats, setProbeStats] = useState<{ p50: number; p95: number; min: number; max: number; avg: number } | null>(null);

  const fetchBenchmarkRuns = async () => {
    setLoadingRuns(true);
    try {
      const data = await clusterService.getBenchmarkRuns(20);
      setRuns(data.runs || []);
      setReferenceBaselines(data.reference_baselines || []);
      setStorageSource(data.storage_source || 'postgres');
      setRunningBenchmark(Boolean(data.running));
      if (!latestRun && data.runs && data.runs.length > 0) {
        setLatestRun(data.runs[0]);
      }
    } catch (err: any) {
      setBenchmarkError(err?.response?.data?.error?.message || err?.message || 'Failed to load benchmark runs');
    } finally {
      setLoadingRuns(false);
    }
  };

  useEffect(() => {
    fetchBenchmarkRuns();
  }, []);

  const handleRunSuite = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!isAdmin) return;

    setRunningBenchmark(true);
    setBenchmarkError('');
    try {
      const created = await clusterService.runBenchmark({
        title: title.trim() || undefined,
        scenario,
        concurrency_level: concurrency,
        total_operations: totalOps,
      });
      setLatestRun(created);
      setTitle('');
      await fetchBenchmarkRuns();
    } catch (err: any) {
      setBenchmarkError(err?.response?.data?.error?.message || err?.message || 'Failed to execute live benchmark');
    } finally {
      setRunningBenchmark(false);
    }
  };

  const runLiveProbe = async () => {
    setProbing(true);
    setProbeResults([]);
    setProbeStats(null);

    const latencies: number[] = [];
    const results: LiveProbeResult[] = [];

    for (let i = 1; i <= 10; i++) {
      const start = performance.now();
      try {
        const res = await apiClient.get('/cluster/status');
        const duration = Math.round((performance.now() - start) * 10) / 10;
        latencies.push(duration);
        results.push({ iteration: i, latencyMs: duration, status: res.status });
      } catch {
        const duration = Math.round((performance.now() - start) * 10) / 10;
        latencies.push(duration);
        results.push({ iteration: i, latencyMs: duration, status: 500 });
      }
      setProbeResults([...results]);
      await new Promise((r) => setTimeout(r, 100));
    }

    const sorted = [...latencies].sort((a, b) => a - b);
    const min = sorted[0];
    const max = sorted[sorted.length - 1];
    const avg = Math.round((sorted.reduce((acc, v) => acc + v, 0) / sorted.length) * 10) / 10;
    const p50 = sorted[Math.floor(sorted.length * 0.5)];
    const p95 = sorted[Math.floor(sorted.length * 0.95)];

    setProbeStats({ min, max, avg, p50, p95 });
    setProbing(false);
  };

  return (
    <div className="space-y-6 max-w-6xl">
      {/* Header */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-slate-900">Distributed Benchmarking &amp; Metrics</h1>
          <p className="text-sm text-slate-500">
            Execute live concurrent Raft quorum workloads persisted to PostgreSQL and inspect gateway latency profiles
          </p>
        </div>

        <button
          onClick={runLiveProbe}
          disabled={probing}
          className="inline-flex items-center space-x-2 rounded-lg border border-indigo-200 bg-white px-4 py-2 text-xs font-semibold text-indigo-700 shadow-sm hover:bg-indigo-50 disabled:opacity-50"
          title="Measures HTTP round-trip latency of GET /api/v1/cluster/status from browser"
        >
          {probing ? <Activity className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
          <span>{probing ? 'Probing Gateway (10 iters)...' : 'Run Gateway Ping Probe'}</span>
        </button>
      </div>

      {/* Live Concurrent Raft Benchmark Runner */}
      <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 border-b border-slate-100 pb-4">
          <div className="flex items-center space-x-2">
            <Zap className="h-5 w-5 text-blue-600" />
            <div>
              <h2 className="text-sm font-bold text-slate-900">Live Raft Cluster Benchmark Runner</h2>
              <p className="text-xs text-slate-500">
                Dispatches bounded concurrent operations through the active Raft leader and persists metrics to{' '}
                <span className="font-mono text-slate-700">{storageSource}</span>
              </p>
            </div>
          </div>
          {!isAdmin && (
            <span className="rounded-full bg-amber-50 border border-amber-200 px-2.5 py-0.5 text-[11px] font-medium text-amber-800">
              Admin role required to launch benchmark runs
            </span>
          )}
        </div>

        <form onSubmit={handleRunSuite} className="mt-4 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-3 items-end">
          <div>
            <label className="block text-[11px] font-semibold uppercase text-slate-500 mb-1">Workload Scenario</label>
            <select
              value={scenario}
              onChange={(e) => setScenario(e.target.value as any)}
              disabled={!isAdmin || runningBenchmark}
              className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-xs font-medium text-slate-800 focus:border-blue-500 focus:outline-none disabled:bg-slate-100"
            >
              <option value="acquire_release_cycle">Acquire &amp; Release Quorum Cycle</option>
              <option value="contention_acquire">Hot-Key Contention Serialization</option>
              <option value="cluster_read_barrier">Leader Status &amp; Linearizable Read</option>
            </select>
          </div>

          <div>
            <label className="block text-[11px] font-semibold uppercase text-slate-500 mb-1">Concurrency (1–32)</label>
            <input
              type="number"
              min={1}
              max={32}
              value={concurrency}
              onChange={(e) => setConcurrency(Math.max(1, Math.min(32, Number(e.target.value) || 1)))}
              disabled={!isAdmin || runningBenchmark}
              className="w-full rounded-lg border border-slate-300 px-3 py-2 text-xs font-mono text-slate-800 focus:border-blue-500 focus:outline-none disabled:bg-slate-100"
            />
          </div>

          <div>
            <label className="block text-[11px] font-semibold uppercase text-slate-500 mb-1">Operations (1–200)</label>
            <input
              type="number"
              min={1}
              max={200}
              value={totalOps}
              onChange={(e) => setTotalOps(Math.max(1, Math.min(200, Number(e.target.value) || 1)))}
              disabled={!isAdmin || runningBenchmark}
              className="w-full rounded-lg border border-slate-300 px-3 py-2 text-xs font-mono text-slate-800 focus:border-blue-500 focus:outline-none disabled:bg-slate-100"
            />
          </div>

          <div>
            <label className="block text-[11px] font-semibold uppercase text-slate-500 mb-1">Run Label (Optional)</label>
            <input
              type="text"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="e.g. Quorum stress check"
              disabled={!isAdmin || runningBenchmark}
              className="w-full rounded-lg border border-slate-300 px-3 py-2 text-xs text-slate-800 focus:border-blue-500 focus:outline-none disabled:bg-slate-100"
            />
          </div>

          <button
            type="submit"
            disabled={!isAdmin || runningBenchmark}
            className="inline-flex items-center justify-center space-x-2 rounded-lg bg-blue-600 px-4 py-2 text-xs font-semibold text-white shadow-sm hover:bg-blue-700 disabled:opacity-50"
          >
            {runningBenchmark ? <Activity className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4 fill-white" />}
            <span>{runningBenchmark ? 'Running Benchmark...' : 'Run Benchmark Suite'}</span>
          </button>
        </form>

        {benchmarkError && (
          <div className="mt-3 rounded-lg border border-rose-200 bg-rose-50 p-3 text-xs text-rose-700 flex items-center space-x-2">
            <AlertTriangle className="h-4 w-4 shrink-0" />
            <span>{benchmarkError}</span>
          </div>
        )}

        {latestRun && (
          <div className="mt-4 rounded-xl border border-blue-200 bg-blue-50/40 p-4">
            <div className="flex flex-wrap items-center justify-between gap-2 mb-3">
              <div className="flex items-center space-x-2">
                <CheckCircle2 className="h-4 w-4 text-blue-600" />
                <span className="text-xs font-bold text-slate-900">Latest Benchmark Run: {latestRun.title}</span>
                <span className="rounded bg-blue-100 px-2 py-0.5 font-mono text-[10px] font-semibold text-blue-800">
                  {latestRun.scenario}
                </span>
              </div>
              <span className="font-mono text-[11px] text-slate-500">
                {latestRun.success_count} ok / {latestRun.error_count} err
              </span>
            </div>

            <div className="grid grid-cols-2 sm:grid-cols-6 gap-2.5 text-xs">
              <div className="rounded-lg bg-white p-2.5 border border-blue-100">
                <span className="text-[10px] text-slate-400 uppercase">Throughput</span>
                <p className="mt-0.5 font-mono font-bold text-blue-700">{latestRun.throughput_rps.toFixed(1)} ops/s</p>
              </div>
              <div className="rounded-lg bg-white p-2.5 border border-blue-100">
                <span className="text-[10px] text-slate-400 uppercase">Min</span>
                <p className="mt-0.5 font-mono font-bold text-slate-800">{latestRun.min_latency_ms.toFixed(2)} ms</p>
              </div>
              <div className="rounded-lg bg-white p-2.5 border border-blue-100">
                <span className="text-[10px] text-slate-400 uppercase">p50</span>
                <p className="mt-0.5 font-mono font-bold text-slate-900">{latestRun.p50_latency_ms.toFixed(2)} ms</p>
              </div>
              <div className="rounded-lg bg-white p-2.5 border border-blue-100">
                <span className="text-[10px] text-slate-400 uppercase">p95</span>
                <p className="mt-0.5 font-mono font-bold text-indigo-700">{latestRun.p95_latency_ms.toFixed(2)} ms</p>
              </div>
              <div className="rounded-lg bg-white p-2.5 border border-blue-100">
                <span className="text-[10px] text-slate-400 uppercase">p99</span>
                <p className="mt-0.5 font-mono font-bold text-indigo-800">{latestRun.p99_latency_ms.toFixed(2)} ms</p>
              </div>
              <div className="rounded-lg bg-white p-2.5 border border-blue-100">
                <span className="text-[10px] text-slate-400 uppercase">Max</span>
                <p className="mt-0.5 font-mono font-bold text-slate-800">{latestRun.max_latency_ms.toFixed(2)} ms</p>
              </div>
            </div>
          </div>
        )}
      </div>

      {/* Live Gateway Probe Results if run */}
      {probeStats && (
        <div className="rounded-xl border border-indigo-200 bg-indigo-50/50 p-5 shadow-card">
          <div className="flex items-center space-x-2 mb-3">
            <CheckCircle2 className="h-5 w-5 text-indigo-600" />
            <h2 className="text-sm font-bold text-slate-900">Gateway HTTP Ping Probe (10 Sequential Status Checks)</h2>
          </div>

          <div className="grid grid-cols-2 sm:grid-cols-5 gap-3 text-xs">
            <div className="bg-white p-3 rounded-lg border border-indigo-100">
              <span className="text-slate-400">Min Latency</span>
              <p className="mt-1 font-mono text-base font-bold text-slate-900">{probeStats.min} ms</p>
            </div>
            <div className="bg-white p-3 rounded-lg border border-indigo-100">
              <span className="text-slate-400">Average</span>
              <p className="mt-1 font-mono text-base font-bold text-slate-900">{probeStats.avg} ms</p>
            </div>
            <div className="bg-white p-3 rounded-lg border border-indigo-100">
              <span className="text-slate-400">P50 Latency</span>
              <p className="mt-1 font-mono text-base font-bold text-indigo-600">{probeStats.p50} ms</p>
            </div>
            <div className="bg-white p-3 rounded-lg border border-indigo-100">
              <span className="text-slate-400">P95 Latency</span>
              <p className="mt-1 font-mono text-base font-bold text-indigo-700">{probeStats.p95} ms</p>
            </div>
            <div className="bg-white p-3 rounded-lg border border-indigo-100">
              <span className="text-slate-400">Max Latency</span>
              <p className="mt-1 font-mono text-base font-bold text-slate-900">{probeStats.max} ms</p>
            </div>
          </div>

          {probeResults.length > 0 && (
            <div className="mt-3 pt-3 border-t border-indigo-100 flex flex-wrap gap-1.5 items-center">
              <span className="text-[11px] text-slate-500 mr-1 font-medium">Probe Log:</span>
              {probeResults.map((p) => (
                <span
                  key={p.iteration}
                  className={`px-2 py-0.5 rounded text-[10px] font-mono font-medium ${
                    p.status === 200 ? 'bg-indigo-100 text-indigo-700' : 'bg-red-100 text-red-700'
                  }`}
                >
                  #{p.iteration}: {p.latencyMs}ms
                </span>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Persisted Benchmark Run History */}
      <div className="rounded-xl border border-slate-200 bg-white shadow-card overflow-hidden">
        <div className="border-b border-slate-200 px-6 py-4 flex items-center justify-between">
          <div className="flex items-center space-x-2">
            <Database className="h-5 w-5 text-emerald-600" />
            <div>
              <h2 className="text-sm font-bold text-slate-900">Persisted Benchmark Run History</h2>
              <p className="text-xs text-slate-500">
                Executed cluster runs stored in PostgreSQL (<code>benchmark_runs</code> &amp; <code>benchmark_results</code>)
              </p>
            </div>
          </div>
          <span className="rounded-full bg-slate-100 px-2.5 py-0.5 text-[11px] font-mono text-slate-600">
            Source: {storageSource} ({runs.length} runs)
          </span>
        </div>

        <div className="overflow-x-auto">
          {loadingRuns ? (
            <div className="p-8 text-center text-xs text-slate-400">Loading persisted benchmark runs...</div>
          ) : runs.length === 0 ? (
            <div className="p-8 text-center text-xs text-slate-400">
              No persisted benchmark runs yet. Run the live benchmark suite above as Admin to generate PostgreSQL-backed telemetry.
            </div>
          ) : (
            <table className="min-w-full divide-y divide-slate-200 text-left text-xs">
              <thead className="bg-slate-50 text-slate-600 uppercase font-medium">
                <tr>
                  <th className="px-5 py-3">Run Title / Scenario</th>
                  <th className="px-4 py-3">Workers / Ops</th>
                  <th className="px-4 py-3">Throughput</th>
                  <th className="px-4 py-3">p50 / p95 / p99</th>
                  <th className="px-4 py-3">Min / Max</th>
                  <th className="px-4 py-3">Success / Err</th>
                  <th className="px-4 py-3">Executed</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-200 text-slate-800">
                {runs.map((run) => (
                  <tr key={run.id} className="hover:bg-slate-50/70">
                    <td className="px-5 py-3">
                      <div className="font-semibold text-slate-900">{run.title}</div>
                      <div className="font-mono text-[10px] text-slate-500">{run.scenario}</div>
                    </td>
                    <td className="px-4 py-3 font-mono text-slate-700">
                      {run.concurrency_level}w &times; {run.total_operations}
                    </td>
                    <td className="px-4 py-3 font-mono font-bold text-blue-700">
                      {run.throughput_rps.toFixed(1)} ops/s
                    </td>
                    <td className="px-4 py-3 font-mono text-slate-700">
                      {run.p50_latency_ms.toFixed(2)} / {run.p95_latency_ms.toFixed(2)} / {run.p99_latency_ms.toFixed(2)} ms
                    </td>
                    <td className="px-4 py-3 font-mono text-slate-500">
                      {run.min_latency_ms.toFixed(2)} / {run.max_latency_ms.toFixed(2)} ms
                    </td>
                    <td className="px-4 py-3 font-mono">
                      <span className="text-emerald-700 font-semibold">{run.success_count}</span>
                      {' / '}
                      <span className={run.error_count > 0 ? 'text-rose-600 font-semibold' : 'text-slate-400'}>
                        {run.error_count}
                      </span>
                    </td>
                    <td className="px-4 py-3 font-mono text-[11px] text-slate-500">
                      {new Date(run.completed_at || run.created_at).toLocaleTimeString()}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      {/* Documented Reference Baselines (Separate Static Target Grid) */}
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-bold text-slate-800">Documented Reference Baselines (Phase 5 Design Targets)</h2>
          <span className="inline-flex items-center px-2 py-0.5 rounded text-[10px] font-medium bg-slate-100 text-slate-600 border border-slate-200">
            Reference Targets (Non-Live)
          </span>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          {referenceBaselines.length > 0 ? (
            referenceBaselines.map((ref) => (
              <div key={ref.id} className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
                <div className="flex items-center space-x-2 text-indigo-600 mb-2">
                  <Clock className="h-4 w-4" />
                  <h3 className="text-xs font-bold uppercase tracking-wider text-slate-800">{ref.title}</h3>
                </div>
                <p className="text-2xl font-bold font-mono text-slate-900">
                  {ref.baseline.toLocaleString()}{' '}
                  <span className="text-xs font-normal text-slate-500">{ref.unit}</span>
                </p>
                <div className="mt-3 border-t border-slate-100 pt-2.5 space-y-1 text-xs text-slate-600">
                  <div className="flex justify-between">
                    <span className="text-slate-400">Scenario:</span>
                    <span className="font-mono text-slate-700">{ref.scenario}</span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-slate-400">Target / Source:</span>
                    <span className="font-medium text-slate-700">{ref.source}</span>
                  </div>
                </div>
              </div>
            ))
          ) : (
            <>
              <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
                <div className="flex items-center space-x-2 text-blue-600 mb-2">
                  <Zap className="h-4 w-4" />
                  <h3 className="text-xs font-bold uppercase tracking-wider text-slate-800">In-Memory FSM Baseline</h3>
                </div>
                <p className="text-2xl font-bold font-mono text-slate-900">
                  25,400 <span className="text-xs font-normal text-slate-500">ops/s</span>
                </p>
                <p className="mt-2 text-xs text-slate-500">Reference Go unit benchmark target (non-replicated)</p>
              </div>
              <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
                <div className="flex items-center space-x-2 text-indigo-600 mb-2">
                  <Activity className="h-4 w-4" />
                  <h3 className="text-xs font-bold uppercase tracking-wider text-slate-800">3-Node Quorum Baseline</h3>
                </div>
                <p className="text-2xl font-bold font-mono text-indigo-700">
                  1,250 <span className="text-xs font-normal text-slate-500">ops/s</span>
                </p>
                <p className="mt-2 text-xs text-slate-500">Reference 3-node BoltDB quorum write target</p>
              </div>
              <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
                <div className="flex items-center space-x-2 text-emerald-600 mb-2">
                  <Clock className="h-4 w-4" />
                  <h3 className="text-xs font-bold uppercase tracking-wider text-slate-800">Failover Recovery Target</h3>
                </div>
                <p className="text-2xl font-bold font-mono text-emerald-700">
                  190 <span className="text-xs font-normal text-slate-500">ms</span>
                </p>
                <p className="mt-2 text-xs text-slate-500">Reference Raft election &amp; snapshot restore target</p>
              </div>
            </>
          )}
        </div>
      </div>

      {/* Benchmark Methodology Card */}
      <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card">
        <div className="flex items-center space-x-2 border-b border-slate-100 pb-3">
          <ShieldCheck className="h-5 w-5 text-indigo-600" />
          <h2 className="text-sm font-bold text-slate-900">Benchmark Pipeline &amp; Verification Methodology</h2>
        </div>

        <div className="mt-4 grid grid-cols-1 sm:grid-cols-2 gap-4 text-xs text-slate-600">
          <div className="p-3.5 rounded-lg bg-slate-50 border border-slate-100">
            <p className="font-semibold text-slate-800 mb-1">Raft Quorum Commit Latency</p>
            <p className="text-slate-500 leading-relaxed">
              Every mutating operation (acquire, renew, release) commits to BoltDB on disk and waits for acknowledgement from at least 2 of 3 nodes before returning HTTP 200 OK.
            </p>
          </div>

          <div className="p-3.5 rounded-lg bg-slate-50 border border-slate-100">
            <p className="font-semibold text-slate-800 mb-1">Audit Log Pipeline Decoupling</p>
            <p className="text-slate-500 leading-relaxed">
              Operational audit persistence to PostgreSQL runs asynchronously via a bounded ring buffer (10,000 capacity), decoupling audit I/O from Raft lock commit paths.
            </p>
          </div>
        </div>
      </div>
    </div>
  );
};
