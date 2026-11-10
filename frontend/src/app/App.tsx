import React from 'react';
import { Routes, Route, Navigate } from 'react-router-dom';
import { DashboardLayout } from '../layouts/DashboardLayout';
import { ProtectedRoute } from '../components/ProtectedRoute';
import { OverviewPage } from '../pages/OverviewPage';
import { LocksPage } from '../pages/LocksPage';
import { ClusterPage } from '../pages/ClusterPage';
import { AuditPage } from '../pages/AuditPage';
import { ResiliencePage } from '../pages/ResiliencePage';
import { BenchmarksPage } from '../pages/BenchmarksPage';
import { SettingsPage } from '../pages/SettingsPage';
import { LoginPage } from '../pages/LoginPage';
import { NotFoundPage } from '../pages/NotFoundPage';

export const App: React.FC = () => {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />

      {/* Protected Cluster Dashboard Routes */}
      <Route element={<ProtectedRoute />}>
        <Route element={<DashboardLayout />}>
          <Route path="/" element={<OverviewPage />} />
          <Route path="/locks" element={<LocksPage />} />
          <Route path="/cluster" element={<ClusterPage />} />
          <Route path="/consensus" element={<ClusterPage />} />
          <Route path="/resilience" element={<ResiliencePage />} />
          <Route path="/audit" element={<AuditPage />} />
          <Route path="/benchmarks" element={<BenchmarksPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="/404" element={<NotFoundPage />} />
          <Route path="*" element={<Navigate to="/404" replace />} />
        </Route>
      </Route>
    </Routes>
  );
};

export default App;

