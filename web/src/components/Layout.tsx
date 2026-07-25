import type { MouseEvent, ReactNode } from "react";

import { FilterBar } from "@/components/FilterBar";
import { ThemeToggle } from "@/components/ThemeToggle";

const links = [
  { to: "/", label: "总览" },
  { to: "/trends", label: "趋势" },
  { to: "/models", label: "模型" },
  { to: "/agents", label: "渠道" },
  { to: "/sessions", label: "会话" },
  { to: "/slow", label: "慢请求" },
  { to: "/imports", label: "导入" },
  { to: "/settings", label: "设置" },
];

type LayoutProps = {
  pathname: string;
  onNavigate: (to: string) => void;
  children: ReactNode;
};

export function Layout({ pathname, onNavigate, children }: LayoutProps) {
  const handleNavigation = (event: MouseEvent<HTMLAnchorElement>, to: string) => {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
      return;
    }
    event.preventDefault();
    onNavigate(to);
  };

  return (
    <div className="shell">
      <header className="topbar">
        <div className="brand">
          <span>本地用量分析</span>
          <strong>AgentLedger</strong>
        </div>
        <nav aria-label="主导航">
          {links.map((link) => (
            <a key={link.to} href={link.to} className={pathname === link.to ? "active" : undefined} onClick={(event) => handleNavigation(event, link.to)}>
              {link.label}
            </a>
          ))}
        </nav>
        <ThemeToggle />
      </header>
      <section className="control-strip">
        <FilterBar />
      </section>
      <main>
        {children}
      </main>
    </div>
  );
}
