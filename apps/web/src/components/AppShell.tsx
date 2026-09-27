import { useQuery } from "@tanstack/react-query";
import { useEffect, useState, type PropsWithChildren } from "react";
import {
  Activity,
  AppWindow,
  Archive,
  Boxes,
  Database,
  Menu,
  Moon,
  Network,
  PanelLeftClose,
  PanelLeftOpen,
  ServerCog,
  Sun,
  X,
} from "lucide-react";
import { Link, useLocation } from "wouter";
import { api } from "../api/client.ts";
import { keys } from "../api/keys.ts";
import { CommandPalette } from "./CommandPalette.tsx";
import { isTerminal } from "../features/operations/OperationTracker.tsx";
import { useLogout } from "../features/session/useSession.ts";
import { Button } from "@/components/ui/button";
import { NativeSelect } from "@/components/ui/native-select";

const navigation = [
  { href: "/", icon: Boxes, label: "Overview" },
  { href: "/apps", icon: AppWindow, label: "Applications" },
  { href: "/data", icon: Database, label: "Data" },
  { href: "/backups", icon: Archive, label: "Backups" },
  { href: "/ingress", icon: Network, label: "Ingress" },
  { href: "/activity", icon: Activity, label: "Activity" },
  { href: "/system", icon: ServerCog, label: "System" },
] as const;

type Theme = "light" | "dark" | "system";
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

export function AppShell({ children }: PropsWithChildren) {
  const [location] = useLocation();
  const [menuOpen, setMenuOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const [theme, setTheme] = useState<Theme>(() => {
    try {
      const value = localStorage.getItem("bento-theme");
      if (value === "light" || value === "dark" || value === "system") return value;
    } catch {
      /* Use the system setting. */
    }
    return "system";
  });
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
  useEffect(() => {
    if (!menuOpen) return;
    const close = (event: KeyboardEvent) => event.key === "Escape" && setMenuOpen(false);
    addEventListener("keydown", close);
    return () => removeEventListener("keydown", close);
  }, [menuOpen]);

  const activeOps = (operations.data?.operations ?? []).filter((op) => !isTerminal(op.state)).length;
  const dockerState = system.isPending
    ? "Checking Docker"
    : system.isError || system.data?.dockerError
      ? "Docker unavailable"
      : "Docker connected";
  const crumbs =
    location
      .split("?")[0]
      ?.split("/")
      .filter(Boolean)
      .map((part) => decodeURIComponent(part)) ?? [];

  return (
    <div className={`station-layout ${collapsed ? "station-layout--compact" : ""}`}>
      {menuOpen && (
        <button className="station-scrim md:hidden" aria-label="Close navigation" onClick={() => setMenuOpen(false)} />
      )}
      <aside className={`station-rail ${menuOpen ? "station-rail--open" : ""}`} aria-label="Bento navigation">
        <div className="station-brand">
          <Link href="/" className="station-brand__link" onClick={() => setMenuOpen(false)} aria-label="Bento overview">
            <img src="/bento-logo-3d.png" alt="" className="station-brand__mark" />
            <span className="station-brand__name">
              bento<span className="station-brand__period">.</span>
            </span>
          </Link>
          <Button
            className="md:hidden"
            size="icon-sm"
            variant="ghost"
            aria-label="Close navigation"
            onClick={() => setMenuOpen(false)}
          >
            <X />
          </Button>
        </div>
        <p className="station-rail__caption">Your stack, at a glance</p>
        <nav className="station-nav" aria-label="Main navigation">
          {navigation.map((item) => {
            const active =
              item.href === "/" ? location === "/" : location === item.href || location.startsWith(`${item.href}/`);
            const Icon = item.icon;
            return (
              <Link
                key={item.href}
                href={item.href}
                title={collapsed ? item.label : undefined}
                aria-current={active ? "page" : undefined}
                onClick={() => setMenuOpen(false)}
                className={`station-nav__item ${active ? "station-nav__item--active" : ""}`}
              >
                <Icon className="size-4 shrink-0" aria-hidden="true" />
                <span>{item.label}</span>
                {item.href === "/activity" && activeOps > 0 && <span className="station-nav__count">{activeOps}</span>}
              </Link>
            );
          })}
        </nav>
        <div className="station-rail__bottom">
          <Link
            href="/system"
            title={system.data?.dockerError ?? dockerState}
            className="station-connection"
            onClick={() => setMenuOpen(false)}
          >
            <span
              className={`station-connection__light ${system.isPending ? "station-connection__light--pending" : system.isError || system.data?.dockerError ? "station-connection__light--error" : ""}`}
            />
            <span className="station-connection__copy min-w-0">
              <strong>{dockerState}</strong>
              <small>{system.data ? system.data.stackName : "Stack status"}</small>
            </span>
          </Link>
          <button
            className="station-collapse"
            type="button"
            aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            onClick={() => setCollapsed((value) => !value)}
          >
            {collapsed ? (
              <PanelLeftOpen className="size-4" />
            ) : (
              <>
                <PanelLeftClose className="size-4" /> <span>Collapse sidebar</span>
              </>
            )}
          </button>
          <Button
            className="station-signout-mobile md:hidden"
            variant="ghost"
            size="sm"
            onClick={() => logout.mutate()}
            disabled={logout.isPending}
          >
            Sign out
          </Button>
        </div>
      </aside>
      <div className="station-workspace">
        <header className="station-topbar">
          <Button
            className="md:hidden"
            size="icon-sm"
            variant="ghost"
            aria-label="Open navigation"
            onClick={() => setMenuOpen(true)}
          >
            <Menu />
          </Button>
          <nav className="station-breadcrumb" aria-label="Breadcrumb">
            <span className="station-breadcrumb__mobile">{crumbs.at(-1)?.replaceAll("-", " ") || "Overview"}</span>
            <Link href="/">Bento</Link>
            {crumbs.length === 0 ? (
              <span className="station-breadcrumb__current"> / Overview</span>
            ) : (
              crumbs.map((crumb, index) => (
                <span
                  key={`${index}-${crumb}`}
                  className={index === crumbs.length - 1 ? "station-breadcrumb__current" : ""}
                >
                  {" "}
                  / {crumb.replaceAll("-", " ")}
                </span>
              ))
            )}
          </nav>
          <div className="station-topbar__tools">
            <CommandPalette />
            <Link
              href="/activity"
              className="station-activity"
              aria-label={activeOps ? `${activeOps} active operations` : "View activity"}
              title={activeOps ? `${activeOps} active operations` : "View activity"}
            >
              <Activity className="size-4" aria-hidden="true" />
              {activeOps > 0 && <span className="station-activity__count">{activeOps}</span>}
            </Link>
            <label className="station-theme">
              <span className="sr-only">Theme</span>
              {theme === "dark" ? <Moon className="size-4" /> : <Sun className="size-4" />}
              <NativeSelect value={theme} onChange={(event) => setTheme(event.target.value as Theme)}>
                <option value="system">System</option>
                <option value="light">Light</option>
                <option value="dark">Dark</option>
              </NativeSelect>
            </label>
            <Button
              className="station-signout-desktop"
              variant="outline"
              size="sm"
              onClick={() => logout.mutate()}
              disabled={logout.isPending}
            >
              Sign out
            </Button>
          </div>
        </header>
        <main id="main-content">{children}</main>
      </div>
    </div>
  );
}
