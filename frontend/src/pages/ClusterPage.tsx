import React from 'react';
import { Server, Network, CheckCircle2 } from 'lucide-react';

export const ClusterPage: React.FC = () => {
  const nodes = [
    {
      id: 'coordinator-a',
      role: 'Leader Candidate',
      raftAddr: 'coordinator-a:7000',
      apiAddr: '0.0.0.0:8001',
      dataPath: '/var/lib/lockguard/data/a',
      state: 'Phase 0 Configured',
    },
    {
      id: 'coordinator-b',
      role: 'Follower',
      raftAddr: 'coordinator-b:7000',
      apiAddr: '0.0.0.0:8002',
      dataPath: '/var/lib/lockguard/data/b',
      state: 'Phase 0 Configured',
    },
    {
      id: 'coordinator-c',
      role: 'Follower',
      raftAddr: 'coordinator-c:7000',
      apiAddr: '0.0.0.0:8003',
      dataPath: '/var/lib/lockguard/data/c',
      state: 'Phase 0 Configured',
    },
  ];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight text-slate-900">Coordinator Nodes &amp; Consensus</h1>
        <p className="text-sm text-slate-500">
          3-Node HashiCorp Raft consensus topology, quorum metrics, and network partitions
        </p>
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        {nodes.map((node) => (
          <div key={node.id} className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
            <div className="flex items-center justify-between">
              <div className="flex items-center space-x-2">
                <div className="rounded-lg bg-blue-50 p-2 text-blue-600">
                  <Server className="h-4 w-4" />
                </div>
                <h3 className="font-mono text-sm font-bold text-slate-900">{node.id}</h3>
              </div>
              <span className="inline-flex items-center rounded-full bg-emerald-50 px-2 py-0.5 text-[10px] font-medium text-emerald-700">
                <CheckCircle2 className="mr-1 h-3 w-3" />
                Valid
              </span>
            </div>

            <div className="mt-4 space-y-2 border-t border-slate-100 pt-3 text-xs text-slate-600">
              <div className="flex justify-between">
                <span className="text-slate-400">Raft Peer Address:</span>
                <span className="font-mono text-slate-800">{node.raftAddr}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-slate-400">Internal API:</span>
                <span className="font-mono text-slate-800">{node.apiAddr}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-slate-400">Storage Volume:</span>
                <span className="font-mono text-slate-800 text-[11px] truncate max-w-[150px]">{node.dataPath}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-slate-400">Role Status:</span>
                <span className="font-medium text-blue-600">{node.role}</span>
              </div>
            </div>
          </div>
        ))}
      </div>

      <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card">
        <div className="flex items-center space-x-2 mb-3">
          <Network className="h-5 w-5 text-indigo-600" />
          <h2 className="text-base font-semibold text-slate-900">Consensus Network Isolation Rules</h2>
        </div>
        <p className="text-xs text-slate-600">
          The Raft network runs on an isolated internal bridge (`172.28.0.0/16`). Peer ports (7000) are never exposed publicly. Coordinator nodes communicate with the client application via the REST API gateway, preserving fail-closed semantics across network partitions.
        </p>
      </div>
    </div>
  );
};
