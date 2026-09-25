import { useQuery } from "@tanstack/react-query";
import { useEffect, useState, type PropsWithChildren } from "react";
import { Link, useLocation } from "wouter";
import { orpc } from "../api/client.ts";
import { Button } from "@/components/ui/button";

const navigation = [
  { href: "/applications", icon: "◫", label: "Applications" },
  { href: "/databases", icon: "▤", label: "Databases" },
  { href: "/routing", icon: "↗", label: "Routing & TLS" },
  { href: "/operations", icon: "✓", label: "Operations" },
] as const;

export function AppShell({ children }: PropsWithChildren) {
  const [location] = useLocation();
  const [menuOpen, setMenuOpen] = useState(false);
  const health = useQuery(orpc.system.health.queryOptions({ input: {} }));
  const [theme, setTheme] = useState<"light" | "dark">(() => {
    const stored = localStorage.getItem("bento-theme");
    if (stored === "dark" || stored === "night") return "dark";
    if (stored === "light" || stored === "bento") return "light";
    return matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  });
  const toggleTheme = () => {
    const next = theme === "dark" ? "light" : "dark";
    localStorage.setItem("bento-theme", next);
    setTheme(next);
  };

  useEffect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark");
  }, [theme]);

  return (
    <div className="min-h-screen">
      <header className="sticky top-0 z-40 border-b border-border bg-card/95 backdrop-blur supports-[backdrop-filter]:bg-card/60">
        <div className="flex min-h-[68px] items-center gap-[clamp(0.75rem,2vw,2rem)] px-[clamp(1rem,2.5vw,2.5rem)] py-[0.65rem] max-[1050px]:gap-3 max-[900px]:relative">
          <Link
            className="flex shrink-0 items-center gap-3 text-inherit no-underline"
            href="/applications"
            onClick={() => setMenuOpen(false)}
          >
            <img
              src="/bento-logo-3d.png"
              alt=""
              aria-hidden="true"
              className="size-[38px] rounded-[11px] object-cover"
            />
            <span>
              <strong className="block text-[1.05rem]">Bento</strong>
              <small className="block text-[0.65rem] uppercase tracking-[0.08em] text-muted-foreground max-[480px]:hidden">
                Control plane
              </small>
            </span>
          </Link>
          <Button
            id="menu-button"
            className="hidden max-[900px]:ml-auto max-[900px]:inline-flex"
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
            className={`flex min-w-0 items-center gap-1 max-[1050px]:gap-0.5 max-[900px]:absolute max-[900px]:top-[calc(100%+0.4rem)] max-[900px]:right-4 max-[900px]:left-4 max-[900px]:grid-cols-2 max-[900px]:gap-1.5 max-[900px]:rounded-lg max-[900px]:border max-[900px]:border-border max-[900px]:bg-card max-[900px]:p-2 max-[900px]:shadow-2xl ${menuOpen ? "max-[900px]:grid" : "max-[900px]:hidden"}`}
            aria-label="Main navigation"
          >
            {navigation.map((item) => (
              <Link
                key={item.href}
                href={item.href}
                className={`flex items-center gap-2 whitespace-nowrap rounded-md px-3 py-2 text-[0.84rem] font-medium text-muted-foreground no-underline hover:bg-accent hover:text-accent-foreground max-[1050px]:px-2 max-[900px]:p-3 ${location === item.href ? "bg-primary text-primary-foreground hover:bg-primary/90 hover:text-primary-foreground" : ""}`}
                onClick={() => setMenuOpen(false)}
              >
                <span className="w-4 text-center">{item.icon}</span>
                <span>{item.label}</span>
              </Link>
            ))}
          </nav>
          <div className="ml-auto flex items-center gap-3 max-[900px]:ml-0">
            <div className="flex items-center gap-2 whitespace-nowrap text-xs text-muted-foreground max-[1050px]:hidden">
              <span
                className={`size-2.5 rounded-full ${
                  health.isSuccess
                    ? "bg-emerald-500 ring-4 ring-emerald-500/20"
                    : "bg-red-500 ring-4 ring-red-500/20"
                }`}
              />
              <span>{health.isSuccess ? "Connected" : "Disconnected"}</span>
            </div>
            <Button variant="ghost" size="icon-sm" aria-label="Toggle theme" onClick={toggleTheme}>
              ◐
            </Button>
          </div>
        </div>
      </header>
      <main className="min-w-0">{children}</main>
    </div>
  );
}
