import { useEffect, useState, type PropsWithChildren } from "react";
import { Link, useLocation } from "wouter";

const navigation = [
  { href: "/applications", icon: "◫", label: "Applications" },
  { href: "/databases", icon: "▤", label: "Databases" },
  { href: "/routing", icon: "↗", label: "Routing & TLS" },
  { href: "/jobs", icon: "↻", label: "Jobs & workers" },
  { href: "/operations", icon: "✓", label: "Operations" },
] as const;

export function AppShell({ children }: PropsWithChildren) {
  const [location] = useLocation();
  const [menuOpen, setMenuOpen] = useState(false);
  const [theme, setTheme] = useState(() => {
    const stored = localStorage.getItem("bento-theme");
    return stored ?? (matchMedia("(prefers-color-scheme: dark)").matches ? "night" : "bento");
  });
  const current = navigation.find((item) => item.href === location);

  const toggleTheme = () => {
    const next = theme === "night" ? "bento" : "night";
    document.documentElement.dataset.theme = next;
    localStorage.setItem("bento-theme", next);
    setTheme(next);
  };

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  return (
    <div className="app-shell">
      <aside className={`sidebar ${menuOpen ? "open" : ""}`}>
        <Link className="brand" href="/applications" onClick={() => setMenuOpen(false)}>
          <span className="brand-mark">B</span>
          <span>
            <strong>Bento</strong>
            <small>Control plane</small>
          </span>
        </Link>
        <nav aria-label="Main navigation">
          {navigation.map((item) => (
            <Link
              key={item.href}
              href={item.href}
              className={`nav-link ${location === item.href ? "active" : ""}`}
              onClick={() => setMenuOpen(false)}
            >
              <span className="nav-icon">{item.icon}</span>
              <span>{item.label}</span>
            </Link>
          ))}
        </nav>
        <div className="sidebar-foot">
          <span className="status status-success" />
          <span>Typed oRPC API</span>
        </div>
      </aside>
      <main>
        <header className="topbar">
          <div>
            <button
              id="menu-button"
              className="btn btn-ghost btn-sm"
              aria-label="Toggle navigation"
              onClick={() => setMenuOpen((open) => !open)}
            >
              ☰
            </button>
            <p className="eyebrow">SINGLE-HOST OPERATIONS</p>
            <h1>{current?.label ?? "Bento"}</h1>
          </div>
          <div className="top-actions">
            <button
              className="btn btn-ghost btn-sm"
              aria-label="Toggle theme"
              onClick={toggleTheme}
            >
              ◐
            </button>
          </div>
        </header>
        {children}
      </main>
    </div>
  );
}
