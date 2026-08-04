import { lazy, Suspense, useCallback, useEffect, useState } from "react";

import { Layout } from "@/components/Layout";
import { FilterProvider } from "@/hooks/filters";
import { ThemeProvider } from "@/hooks/theme";

const OverviewPage = lazy(() => import("@/pages/OverviewPage").then((module) => ({ default: module.OverviewPage })));
const TrendsPage = lazy(() => import("@/pages/TrendsPage").then((module) => ({ default: module.TrendsPage })));
const ModelsPage = lazy(() => import("@/pages/ModelsPage").then((module) => ({ default: module.ModelsPage })));
const AgentsPage = lazy(() => import("@/pages/AgentsPage").then((module) => ({ default: module.AgentsPage })));
const SourcesPage = lazy(() => import("@/pages/SourcesPage").then((module) => ({ default: module.SourcesPage })));
const ProjectsPage = lazy(() => import("@/pages/ProjectsPage").then((module) => ({ default: module.ProjectsPage })));
const SessionsPage = lazy(() => import("@/pages/SessionsPage").then((module) => ({ default: module.SessionsPage })));
const ImportsPage = lazy(() => import("@/pages/ImportsPage").then((module) => ({ default: module.ImportsPage })));
const SettingsPage = lazy(() => import("@/pages/SettingsPage").then((module) => ({ default: module.SettingsPage })));

const routes = {
  "/": OverviewPage,
  "/trends": TrendsPage,
  "/models": ModelsPage,
  "/agents": AgentsPage,
  "/sources": SourcesPage,
  "/projects": ProjectsPage,
  "/sessions": SessionsPage,
  "/imports": ImportsPage,
  "/settings": SettingsPage,
} as const;

function currentPathname() {
  return window.location.pathname;
}

export default function App() {
  const [pathname, setPathname] = useState(currentPathname);
  useEffect(() => {
    const handlePopState = () => setPathname(currentPathname());
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, []);

  const navigate = useCallback((to: string) => {
    if (to === currentPathname()) {
      return;
    }
    window.history.pushState(null, "", to);
    setPathname(to);
    window.scrollTo({ top: 0 });
  }, []);

  const Page = routes[pathname as keyof typeof routes] ?? OverviewPage;
  return (
    <ThemeProvider>
      <FilterProvider>
        <Suspense fallback={<div className="route-loading">页面加载中...</div>}>
          <Layout pathname={pathname} onNavigate={navigate}>
            <Page />
          </Layout>
        </Suspense>
      </FilterProvider>
    </ThemeProvider>
  );
}
