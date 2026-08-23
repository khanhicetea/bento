import { useEffect, useState, type PropsWithChildren } from "react";
import { Link, useLocation } from "wouter";
import { Button } from "@/components/ui/button";

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
  const [theme, setTheme] = useState<"light" | "dark">(() => {
    const stored = localStorage.getItem("bento-theme");
    if (stored === "dark" || stored === "night") return "dark";
    if (stored === "light" || stored === "bento") return "light";
    return matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  });
  const current = navigation.find((item) => item.href === location);

  const toggleTheme = () => {
    const next = theme === "dark" ? "light" : "dark";
    localStorage.setItem("bento-theme", next);
    setTheme(next);
  };

  useEffect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark");
  }, [theme]);

  return (
    <div className="app-shell">
      <header className="site-header">
        <div className="header-inner">
          <Link className="brand" href="/applications" onClick={() => setMenuOpen(false)}>
            <span className="brand-mark">B</span>
            <span>
              <strong>Bento</strong>
              <small>Control plane</small>
            </span>
          </Link>
          <Button
            id="menu-button"
            variant="ghost"
            size="icon-sm"
            aria-label="Toggle navigation"
            aria-controls="main-navigation"
            aria-expanded={menuOpen}
            onClick={() => setMenuOpen((open) => !open)}
          >
            ☰
          </Button>
          <nav
            id="main-navigation"
            className={`header-nav ${menuOpen ? "open" : ""}`}
            aria-label="Main navigation"
          >
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
          <div className="header-actions">
            <div className="header-status">
              <span className="size-2.5 rounded-full bg-emerald-500 ring-4 ring-emerald-500/20" />
              <span>Typed oRPC API</span>
            </div>
            <Button variant="ghost" size="icon-sm" aria-label="Toggle theme" onClick={toggleTheme}>
              ◐
            </Button>
          </div>
        </div>
        <div className="page-heading">
          <p className="eyebrow">SINGLE-HOST OPERATIONS</p>
          <h1>{current?.label ?? "Bento"}</h1>
        </div>
      </header>
      <main>{children}</main>
    </div>
  );
}
