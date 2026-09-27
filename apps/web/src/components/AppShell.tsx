import { useQuery } from "@tanstack/react-query";
import { useEffect, useState, type PropsWithChildren } from "react";
import {
  Activity,
  Archive,
  Boxes,
  Database,
  LayoutGrid,
  LogOut,
  Monitor,
  Moon,
  Network,
  Server,
  Sun,
} from "lucide-react";
import { Link, useLocation } from "wouter";
import { api } from "../api/client.ts";
import { keys } from "../api/keys.ts";
import { CommandPalette } from "./CommandPalette.tsx";
import { isTerminal } from "../features/operations/OperationTracker.tsx";
import { useLogout } from "../features/session/useSession.ts";

const navigation = [
  { href: "/", icon: LayoutGrid, label: "Home" },
  { href: "/apps", icon: Boxes, label: "Apps" },
  { href: "/data", icon: Database, label: "Data" },
  { href: "/backups", icon: Archive, label: "Backups" },
  { href: "/ingress", icon: Network, label: "Ingress" },
  { href: "/activity", icon: Activity, label: "Activity" },
  { href: "/system", icon: Server, label: "System" },
] as const;

type Theme = "light" | "dark" | "system";
const nextTheme: Record<Theme, Theme> = { system: "light", light: "dark", dark: "system" };
const themeIcon = { system: Monitor, light: Sun, dark: Moon } as const;

function applyTheme(theme: Theme) {
  const dark = theme === "dark" || (theme === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", dark);
  document.documentElement.dataset.theme = theme;
  try {
    localStorage.setItem("bento-theme", theme);
  } catch {
    /* Storage may be unavailable. */
  }
}

function readTheme(): Theme {
  try {
    const value = localStorage.getItem("bento-theme");
    if (value === "light" || value === "dark" || value === "system") return value;
  } catch {
    /* Use the system setting. */
  }
  return "system";
}

export function AppShell({ children }: PropsWithChildren) {
  const [location] = useLocation();
  const [theme, setTheme] = useState<Theme>(readTheme);
  const system = useQuery({
    queryKey: keys.system,
    queryFn: ({ signal }) => api.system.status(signal),
    refetchInterval: 30_000,
  });
  const operations = useQuery({
    queryKey: keys.operations.list(),
    queryFn: ({ signal }) => api.operations.list(undefined, signal),
    refetchInterval: 5_000,
  });
  const logout = useLogout();

  useEffect(() => {
    applyTheme(theme);
    const media = matchMedia("(prefers-color-scheme: dark)");
    const update = () => theme === "system" && applyTheme(theme);
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, [theme]);

  const activeOps = (operations.data?.operations ?? []).filter((op) => !isTerminal(op.state)).length;
  const dockerBad = system.isError || !!system.data?.dockerError;
  const dockerLabel = system.isPending ? "Checking Docker" : dockerBad ? "Docker unavailable" : "Docker connected";
  const isActive = (href: string) =>
    href === "/" ? location === "/" : location === href || location.startsWith(`${href}/`);
  const ThemeIcon = themeIcon[theme];

  const links = (withBadge: boolean) =>
    navigation.map((item) => {
      const Icon = item.icon;
      return (
        <Link
          key={item.href}
          href={item.href}
          title={item.label}
          aria-current={isActive(item.href) ? "page" : undefined}
        >
          <Icon className="size-4" aria-hidden="true" />
          <span className="nav__label">{item.label}</span>
          {withBadge && item.href === "/activity" && activeOps > 0 && <span className="nav__badge">{activeOps}</span>}
        </Link>
      );
    });

  return (
    <div className="shell">
      <header className="topbar">
        <Link href="/" className="brand" aria-label="Bento home">
          <img src="/bento-logo-3d.png" alt="" />
          <span>
            bento<b>.</b>
          </span>
        </Link>
        <nav className="nav" aria-label="Main navigation">
          {links(true)}
        </nav>
        <div className="tools">
          <CommandPalette />
          <Link href="/system" className="icon-btn" title={system.data?.dockerError ?? dockerLabel}>
            <Server aria-hidden="true" />
            <span className={`dot ${system.isPending ? "dot--wait" : dockerBad ? "dot--bad" : ""}`} />
            <span className="sr-only">{dockerLabel}</span>
          </Link>
          <button
            type="button"
            className="icon-btn"
            title={`Theme: ${theme}`}
            aria-label={`Theme: ${theme}. Switch to ${nextTheme[theme]}`}
            onClick={() => setTheme(nextTheme[theme])}
          >
            <ThemeIcon />
          </button>
          <button
            type="button"
            className="icon-btn"
            title="Sign out"
            aria-label="Sign out"
            disabled={logout.isPending}
            onClick={() => logout.mutate()}
          >
            <LogOut />
          </button>
        </div>
      </header>
      <main id="main-content" className="main">
        {children}
      </main>
      <nav className="tabbar" aria-label="Main navigation">
        {links(false)}
      </nav>
    </div>
  );
}
