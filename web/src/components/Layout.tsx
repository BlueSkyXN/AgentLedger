import { NavLink, Outlet } from "react-router-dom";

import { FilterBar } from "@/components/FilterBar";
import { ThemeToggle } from "@/components/ThemeToggle";

const links = [
  { to: "/", label: "总览", end: true },
  { to: "/trends", label: "趋势" },
  { to: "/models", label: "模型" },
  { to: "/agents", label: "Agent" },
  { to: "/sessions", label: "会话" },
  { to: "/imports", label: "导入" },
  { to: "/settings", label: "设置" },
];

export function Layout() {
  return (
    <div className="shell">
      <header className="topbar">
        <div className="brand">
          <span>本地用量账本</span>
          <strong>AgentLedger</strong>
        </div>
        <nav aria-label="主导航">
          {links.map((link) => (
            <NavLink key={link.to} to={link.to} end={link.end} className={({ isActive }) => isActive ? "active" : undefined}>
              {link.label}
            </NavLink>
          ))}
        </nav>
        <ThemeToggle />
      </header>
      <section className="hero">
        <div>
          <p className="eyebrow">只读 SQLite 面板</p>
          <h1>把本机 Agent 用量变成可筛选、可对比、可追踪的账本。</h1>
          <p className="hero-copy">所有数据实时来自当前 SQLite 聚合查询；面板不触发 import、merge、vacuum 或配置写入。</p>
        </div>
        <FilterBar />
      </section>
      <main>
        <Outlet />
      </main>
    </div>
  );
}
