import { NavLink, Outlet } from "react-router-dom";

import { FilterBar } from "@/components/FilterBar";
import { ThemeToggle } from "@/components/ThemeToggle";

const links = [
  { to: "/", label: "总览", end: true },
  { to: "/trends", label: "趋势" },
  { to: "/models", label: "模型" },
  { to: "/agents", label: "渠道" },
  { to: "/sessions", label: "会话" },
  { to: "/slow", label: "慢请求" },
  { to: "/imports", label: "导入" },
  { to: "/settings", label: "设置" },
];

export function Layout() {
  return (
    <div className="shell">
      <header className="topbar">
        <div className="brand">
          <span>本地用量分析</span>
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
      <section className="control-strip">
        <FilterBar />
      </section>
      <main>
        <Outlet />
      </main>
    </div>
  );
}
