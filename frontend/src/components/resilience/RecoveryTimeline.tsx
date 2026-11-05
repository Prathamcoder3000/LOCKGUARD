import React from 'react';
import { ArrowRight, CheckCircle2, ShieldAlert, Cpu, RefreshCw, Zap } from 'lucide-react';

export const RecoveryTimeline: React.FC = () => {
  const steps = [
    {
      phase: 'Step 1: Heartbeat Lease Scrutiny',
      title: 'Leader Failure Detected',
      desc: 'Followers detect leader absence when Raft election timer (1000ms–2000ms) elapses with no heartbeats.',
      icon: ShieldAlert,
      badge: 'Detection',
      color: 'rose',
    },
    {
      phase: 'Step 2: Term Increment & Quorum Vote',
      title: 'Election Triggered & Majority Vote',
      desc: 'Candidate node increments Raft term, requests votes, and secures quorum (2 of 3 votes).',
      icon: Cpu,
      badge: 'Consensus',
      color: 'amber',
    },
    {
      phase: 'Step 3: Linearizability Barrier',
      title: 'Leader Linearizability Established',
      desc: 'New leader issues Raft Barrier() to ensure all committed log entries are fully applied to FSM.',
      icon: Zap,
      badge: 'Integrity',
      color: 'blue',
    },
    {
      phase: 'Step 4: Transparent Client Handover',
      title: 'Gateway Redirection & Lease Continuity',
      desc: 'HTTP API Gateway routes requests to new leader; active lock leases and monotonic fencing tokens preserved.',
      icon: CheckCircle2,
      badge: 'Recovered',
      color: 'emerald',
    },
  ];

  return (
    <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
      <div className="flex items-center justify-between pb-3 border-b border-slate-100">
        <div>
          <h3 className="text-base font-bold text-slate-900">Automated Failover &amp; Recovery Workflow</h3>
          <p className="text-xs text-slate-500">
            Lifecycle of leader election and consensus state preservation under quorum
          </p>
        </div>
        <span className="inline-flex items-center text-xs text-blue-600 font-semibold">
          <RefreshCw className="mr-1 h-3.5 w-3.5" />
          Deterministic Protocol
        </span>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-4 gap-4 mt-5">
        {steps.map((step, idx) => {
          const Icon = step.icon;
          return (
            <div key={idx} className="relative rounded-lg border border-slate-100 bg-slate-50/70 p-4">
              <div className="flex items-center justify-between mb-2">
                <span className="text-[10px] font-bold uppercase tracking-wider text-slate-400">
                  {step.phase}
                </span>
                <span
                  className={`text-[10px] font-semibold px-2 py-0.5 rounded-full ${
                    step.color === 'rose'
                      ? 'bg-rose-100 text-rose-700'
                      : step.color === 'amber'
                      ? 'bg-amber-100 text-amber-700'
                      : step.color === 'blue'
                      ? 'bg-blue-100 text-blue-700'
                      : 'bg-emerald-100 text-emerald-700'
                  }`}
                >
                  {step.badge}
                </span>
              </div>

              <div className="flex items-start space-x-2 mt-2">
                <Icon
                  className={`h-4 w-4 shrink-0 mt-0.5 ${
                    step.color === 'rose'
                      ? 'text-rose-600'
                      : step.color === 'amber'
                      ? 'text-amber-600'
                      : step.color === 'blue'
                      ? 'text-blue-600'
                      : 'text-emerald-600'
                  }`}
                />
                <h4 className="text-xs font-bold text-slate-900">{step.title}</h4>
              </div>

              <p className="text-[11px] text-slate-600 mt-2 leading-relaxed">
                {step.desc}
              </p>

              {idx < steps.length - 1 && (
                <div className="hidden md:block absolute -right-2.5 top-1/2 -translate-y-1/2 z-10 text-slate-400 bg-white rounded-full p-0.5 border border-slate-200">
                  <ArrowRight className="h-3.5 w-3.5" />
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
};

