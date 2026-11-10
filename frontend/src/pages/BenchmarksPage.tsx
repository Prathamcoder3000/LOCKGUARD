import React, { useState } from 'react';
import { Zap, Activity, Clock, ShieldCheck, Play, CheckCircle2 } from 'lucide-react';
import { apiClient } from '../services/api';

interface LiveProbeResult {
  iteration: number;
  latencyMs: number;
  status: number;
}

export const BenchmarksPage: React.FC = () => {
  const [probing, setProbing] = useState<boolean>(false);
  const [probeResults, setProbeResults] = useState<LiveProbeResult[]>([]);
  const [probeStats, setProbeStats] = useState<{ p50: number; p95: number; min: number; max: number; avg: number } | null>(null);

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
      // Small pause between probes
      await new Promise((r) => setTimeout(r, 100));
    }

    // Calculate percentiles
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
            System latency profiles, Raft consensus write limits, and automated recovery timings
          </p>
        </div>

        <button
          onClick={runLiveProbe}
          disabled={probing}
          className="inline-flex items-center space-x-2 rounded-lg bg-indigo-600 px-4 py-2 text-xs font-semibold text-white shadow-sm hover:bg-indigo-700 disabled:opacity-50"
        >
          {probing ? (
            <Activity className="h-4 w-4 animate-spin" />
          ) : (
            <Play className="h-4 w-4 fill-white" />
          )}
          <span>{probing ? 'Probing Cluster (10 iters)...' : 'Run Live Latency Probe'}</span>
        </button>
      </div>

      {/* Live Probe Results if run */}
      {probeStats && (
        <div className="rounded-xl border border-indigo-200 bg-indigo-50/50 p-5 shadow-card">
          <div className="flex items-center space-x-2 mb-3">
            <CheckCircle2 className="h-5 w-5 text-indigo-600" />
            <h2 className="text-sm font-bold text-slate-900">Live Cluster Round-Trip Telemetry (10 Samples)</h2>
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
              <span className="text-[11px] text-slate-500 mr-1 font-medium">Telemetry Log:</span>
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

      {/* Documented Reference Benchmark Measurements Grid */}
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-bold text-slate-800">Documented Reference Baselines (Phase 5 Performance Testing)</h2>
          <span className="inline-flex items-center px-2 py-0.5 rounded text-[10px] font-medium bg-slate-100 text-slate-600 border border-slate-200">
            Historical Benchmark Data
          </span>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-5">
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
          <div className="flex items-center space-x-2 text-blue-600 mb-3">
            <Zap className="h-4 w-4" />
            <h3 className="text-xs font-bold uppercase tracking-wider text-slate-800">In-Memory FSM Throughput</h3>
          </div>
          <p className="text-3xl font-bold font-mono text-slate-900">25,400 <span className="text-sm font-normal text-slate-500">ops/s</span></p>
          <div className="mt-3 border-t border-slate-100 pt-3 space-y-1.5 text-xs text-slate-600">
            <div className="flex justify-between">
              <span className="text-slate-400">P50 Latency:</span>
              <span className="font-mono font-medium text-slate-800">&lt; 0.05 ms</span>
            </div>
            <div className="flex justify-between">
              <span className="text-slate-400">P99 Latency:</span>
              <span className="font-mono font-medium text-slate-800">0.12 ms</span>
            </div>
            <div className="flex justify-between">
              <span className="text-slate-400">Execution Mode:</span>
              <span className="font-medium text-slate-700">Non-replicated unit tests</span>
            </div>
          </div>
        </div>

        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
          <div className="flex items-center space-x-2 text-indigo-600 mb-3">
            <Activity className="h-4 w-4" />
            <h3 className="text-xs font-bold uppercase tracking-wider text-slate-800">3-Node Quorum Mutation</h3>
          </div>
          <p className="text-3xl font-bold font-mono text-indigo-600">1,250 <span className="text-sm font-normal text-slate-500">ops/s</span></p>
          <div className="mt-3 border-t border-slate-100 pt-3 space-y-1.5 text-xs text-slate-600">
            <div className="flex justify-between">
              <span className="text-slate-400">P50 Latency:</span>
              <span className="font-mono font-medium text-indigo-600">1.8 ms</span>
            </div>
            <div className="flex justify-between">
              <span className="text-slate-400">P95 Latency:</span>
              <span className="font-mono font-medium text-indigo-700">4.2 ms</span>
            </div>
            <div className="flex justify-between">
              <span className="text-slate-400">P99 Latency:</span>
              <span className="font-mono font-medium text-indigo-800">11.5 ms</span>
            </div>
          </div>
        </div>

        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
          <div className="flex items-center space-x-2 text-emerald-600 mb-3">
            <Clock className="h-4 w-4" />
            <h3 className="text-xs font-bold uppercase tracking-wider text-slate-800">Failover &amp; Compaction</h3>
          </div>
          <p className="text-3xl font-bold font-mono text-emerald-700">190 <span className="text-sm font-normal text-slate-500">ms</span></p>
          <div className="mt-3 border-t border-slate-100 pt-3 space-y-1.5 text-xs text-slate-600">
            <div className="flex justify-between">
              <span className="text-slate-400">Election Duration:</span>
              <span className="font-mono font-medium text-slate-800">150–250 ms</span>
            </div>
            <div className="flex justify-between">
              <span className="text-slate-400">Snapshot (1K locks):</span>
              <span className="font-mono font-medium text-slate-800">1.2 ms</span>
            </div>
            <div className="flex justify-between">
              <span className="text-slate-400">Restore (1K locks):</span>
              <span className="font-mono font-medium text-slate-800">1.8 ms</span>
            </div>
          </div>
        </div>
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
              Every mutating operation (acquire, renew, release) commits to BoltDB on disk and waits for acknowledgement from at least 2 of 3 nodes before returning HTTP 200 OK. Linearizable throughput is governed by disk fsync and internal network round-trip time.
            </p>
          </div>

          <div className="p-3.5 rounded-lg bg-slate-50 border border-slate-100">
            <p className="font-semibold text-slate-800 mb-1">Audit Log Pipeline Decoupling</p>
            <p className="text-slate-500 leading-relaxed">
              Operational audit persistence to PostgreSQL runs asynchronously via a bounded ring buffer (50,000 capacity). Raft state machine commit latencies are completely decoupled from relational database write times.
            </p>
          </div>
        </div>
      </div>
    </div>
  );
};
