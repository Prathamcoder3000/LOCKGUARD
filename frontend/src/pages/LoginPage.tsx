import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ShieldCheck, Lock, User, ArrowRight, AlertCircle, Key } from 'lucide-react';
import { useAuth } from '../context/AuthContext';
import { apiClient } from '../services/api';

export const LoginPage: React.FC = () => {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState<boolean>(false);
  const { login } = useAuth();
  const navigate = useNavigate();

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);

    try {
      const res = await apiClient.post('/auth/login', {
        username: username.trim(),
        password: password.trim(),
      });

      if (res.data && res.data.data) {
        const { token, user: userBrief } = res.data.data;
        const normalizedRole = userBrief.role ? userBrief.role.toUpperCase() : 'CLIENT';
        login(token, {
          id: userBrief.id || `usr-${username}`,
          username: userBrief.username,
          role: normalizedRole,
          owner_id: userBrief.owner_id || '',
          email: `${userBrief.username}@lockguard.internal`,
        });
        navigate('/');
      }
    } catch (err: any) {
      const msg = err.response?.data?.error?.message || err.message || 'Authentication failed. Please verify credentials.';
      setError(msg);
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleQuickFill = (u: string) => {
    setUsername(u);
    setPassword('');
    setError(null);
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-[#070C1E] px-4 py-12 sm:px-6 lg:px-8">
      <div className="w-full max-w-md space-y-6 rounded-2xl border border-white/10 bg-[#0B132B] p-8 shadow-2xl">
        <div className="text-center">
          <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-xl bg-gradient-to-tr from-blue-600 to-violet-600 shadow-lg shadow-blue-500/30">
            <ShieldCheck className="h-7 w-7 text-white" />
          </div>
          <h2 className="mt-4 text-2xl font-bold tracking-tight text-white">LOCKGUARD</h2>
          <p className="mt-1 text-xs uppercase tracking-wider text-blue-400 font-semibold">
            Fault-Tolerant Distributed Coordination
          </p>
          <p className="mt-2 text-xs text-slate-400">
            Phase 5 Hardened Access: Enter credentials to acquire JWT token.
          </p>
        </div>

        {error && (
          <div className="flex items-center gap-2 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-xs text-red-300">
            <AlertCircle className="h-4 w-4 shrink-0 text-red-400" />
            <span>{error}</span>
          </div>
        )}

        <form className="mt-6 space-y-4" onSubmit={handleSubmit}>
          <div>
            <label className="block text-xs font-medium text-slate-300">Username</label>
            <div className="relative mt-1">
              <div className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3">
                <User className="h-4 w-4 text-slate-500" />
              </div>
              <input
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className="block w-full rounded-lg border border-white/10 bg-white/5 py-2 pl-9 pr-3 text-sm text-white placeholder-slate-500 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                placeholder="admin"
                required
              />
            </div>
          </div>

          <div>
            <label className="block text-xs font-medium text-slate-300">Password</label>
            <div className="relative mt-1">
              <div className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3">
                <Lock className="h-4 w-4 text-slate-500" />
              </div>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className="block w-full rounded-lg border border-white/10 bg-white/5 py-2 pl-9 pr-3 text-sm text-white placeholder-slate-500 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                placeholder="••••••••"
                required
              />
            </div>
          </div>

          <button
            type="submit"
            disabled={isSubmitting}
            className="flex w-full items-center justify-center space-x-2 rounded-lg bg-blue-600 py-2.5 text-sm font-semibold text-white shadow-md shadow-blue-500/25 transition-all hover:bg-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 focus:ring-offset-[#0B132B] disabled:opacity-50"
          >
            <span>{isSubmitting ? 'Verifying Credentials...' : 'Sign In with JWT'}</span>
            <ArrowRight className="h-4 w-4" />
          </button>
        </form>

        {import.meta.env.DEV && (
          <div className="rounded-xl border border-white/5 bg-white/[0.02] p-3 text-left">
            <div className="flex items-center gap-1.5 text-[11px] font-medium text-slate-300 mb-2">
              <Key className="h-3.5 w-3.5 text-blue-400" />
              <span>Development Identity Helper (Dev Only)</span>
            </div>
            <div className="grid grid-cols-3 gap-1.5 text-[10px]">
              <button
                type="button"
                onClick={() => handleQuickFill('admin')}
                className="rounded border border-white/10 bg-white/5 px-2 py-1 text-slate-300 hover:bg-blue-500/20 hover:text-white"
              >
                admin
              </button>
              <button
                type="button"
                onClick={() => handleQuickFill('client_worker')}
                className="rounded border border-white/10 bg-white/5 px-2 py-1 text-slate-300 hover:bg-blue-500/20 hover:text-white"
              >
                client_worker
              </button>
              <button
                type="button"
                onClick={() => handleQuickFill('auditor')}
                className="rounded border border-white/10 bg-white/5 px-2 py-1 text-slate-300 hover:bg-blue-500/20 hover:text-white"
              >
                auditor
              </button>
            </div>
          </div>
        )}

        <div className="border-t border-white/10 pt-4 text-center text-[11px] text-slate-400">
          Academic Project • Fault Tolerant Distributed Lock Service
        </div>
      </div>
    </div>
  );
};
