import { lazy, Suspense } from "react";
import { Route, Routes } from "react-router-dom";

import { Layout } from "@/components/Layout";
import { FilterProvider } from "@/hooks/filters";
import { ThemeProvider } from "@/hooks/theme";

const OverviewPage = lazy(() => import("@/pages/OverviewPage").then((module) => ({ default: module.OverviewPage })));
const TrendsPage = lazy(() => import("@/pages/TrendsPage").then((module) => ({ default: module.TrendsPage })));
const ModelsPage = lazy(() => import("@/pages/ModelsPage").then((module) => ({ default: module.ModelsPage })));
const AgentsPage = lazy(() => import("@/pages/AgentsPage").then((module) => ({ default: module.AgentsPage })));
const SessionsPage = lazy(() => import("@/pages/SessionsPage").then((module) => ({ default: module.SessionsPage })));
const SlowPage = lazy(() => import("@/pages/SlowPage").then((module) => ({ default: module.SlowPage })));
const ImportsPage = lazy(() => import("@/pages/ImportsPage").then((module) => ({ default: module.ImportsPage })));
const SettingsPage = lazy(() => import("@/pages/SettingsPage").then((module) => ({ default: module.SettingsPage })));

export default function App() {
  return (
    <ThemeProvider>
      <FilterProvider>
        <Suspense fallback={<div className="route-loading">页面加载中...</div>}>
          <Routes>
            <Route element={<Layout />}>
              <Route path="/" element={<OverviewPage />} />
              <Route path="/trends" element={<TrendsPage />} />
              <Route path="/models" element={<ModelsPage />} />
              <Route path="/agents" element={<AgentsPage />} />
              <Route path="/sessions" element={<SessionsPage />} />
              <Route path="/slow" element={<SlowPage />} />
              <Route path="/imports" element={<ImportsPage />} />
              <Route path="/settings" element={<SettingsPage />} />
              <Route path="*" element={<OverviewPage />} />
            </Route>
          </Routes>
        </Suspense>
      </FilterProvider>
    </ThemeProvider>
  );
}
