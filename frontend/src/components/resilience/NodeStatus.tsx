import React from 'react';
import { Server, Crown, CheckCircle2, XCircle } from 'lucide-react';
import { CoordinatorNodeStatus } from '../../services/resilienceService';

interface NodeStatusProps {
  coordinators: CoordinatorNodeStatus[];
}

export const NodeStatus: React.FC<NodeStatusProps> = ({ coordinators }) => {
  // When coordinators list is not yet loaded, show neutral connecting state rather than hardcoding a leader
  const displayNodes: CoordinatorNodeStatus[] =
    coordinators && coordinators.length > 0
      ? coordinators
      : [
          {
            node_id: 'coordinator-a',
            endpoint: 'http://coordinator-a:8001',
            reachable: false,
            is_leader: false,
            role: 'Probing...',
          },
          {
            node_id: 'coordinator-b',
            endpoint: 'http://coordinator-b:8001',
            reachable: false,
            is_leader: false,
            role: 'Probing...',
          },
          {
            node_id: 'coordinator-c',
            endpoint: 'http://coordinator-c:8001',
            reachable: false,
            is_leader: false,
            role: 'Probing...',
          },
        ];

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-base font-bold text-slate-900">Coordinator Cluster Nodes</h3>
        <span className="text-xs text-slate-500">
          Independent processes replicating state machine log entries
        </span>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        {displayNodes.map((node) => (
          <div
            key={node.node_id}
            className={`rounded-xl border bg-white p-5 shadow-sm transition-all ${
              node.is_leader
                ? 'border-blue-300 ring-2 ring-blue-100'
                : 'border-slate-200 hover:border-slate-300'
            }`}
          >
            <div className="flex items-center justify-between">
              <div className="flex items-center space-x-2.5">
                <div
                  className={`rounded-lg p-2 ${
                    node.is_leader
                      ? 'bg-blue-600 text-white shadow-sm shadow-blue-500/20'
                      : 'bg-slate-100 text-slate-600'
                  }`}
                >
                  <Server className="h-4 w-4" />
                </div>
                <div>
                  <h4 className="font-mono text-sm font-bold text-slate-900">{node.node_id}</h4>
                  <span className="text-[11px] text-slate-500 font-mono">{node.endpoint}</span>
                </div>
              </div>

              {node.is_leader && (
                <span className="inline-flex items-center rounded-full bg-blue-50 px-2 py-0.5 text-xs font-semibold text-blue-700">
                  <Crown className="mr-1 h-3 w-3 text-amber-500" />
                  Leader
                </span>
              )}
            </div>

            <div className="mt-4 pt-3 border-t border-slate-100 space-y-2 text-xs">
              <div className="flex justify-between items-center">
                <span className="text-slate-500">Raft Role:</span>
                <span className="font-semibold text-slate-800 capitalize">{node.role || 'Follower'}</span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-slate-500">Connectivity:</span>
                <span
                  className={`inline-flex items-center font-medium ${
                    node.reachable ? 'text-emerald-600' : 'text-rose-600'
                  }`}
                >
                  {node.reachable ? (
                    <>
                      <CheckCircle2 className="h-3.5 w-3.5 mr-1" />
                      Reachable
                    </>
                  ) : (
                    <>
                      <XCircle className="h-3.5 w-3.5 mr-1" />
                      Unreachable / Severed
                    </>
                  )}
                </span>
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
};

