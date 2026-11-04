import React from 'react';
import { BarChart3, Zap } from 'lucide-react';

export const BenchmarksPage: React.FC = () => {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight text-slate-900">Distributed Benchmarking &amp; Stress Testing</h1>
        <p className="text-sm text-slate-500">
          Measure throughput, P50/P95/P99 latency, and partition tolerance
        </p>
      </div>

      <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card">
        <div className="flex items-center space-x-3 text-slate-600 mb-4">
          <div className="rounded-lg bg-violet-50 p-2 text-violet-600">
            <BarChart3 className="h-5 w-5" />
          </div>
          <div>
            <h2 className="text-base font-semibold text-slate-900">Benchmark Suite Architecture</h2>
            <p className="text-xs text-slate-500">Planned Phase 5 performance and resilience evaluation</p>
          </div>
        </div>

        <div className="rounded-lg border border-dashed border-slate-200 bg-slate-50 p-12 text-center">
          <Zap className="mx-auto h-10 w-10 text-slate-400 mb-3" />
          <h3 className="text-sm font-semibold text-slate-800">Benchmark Pipeline Ready for Phase 5</h3>
          <p className="mt-1 max-w-md mx-auto text-xs text-slate-500">
            Database tables `benchmark_runs` and `benchmark_results` are established to store reproducible metrics under concurrency scales of 10 to 1,000 parallel clients.
          </p>
        </div>
      </div>
    </div>
  );
};
