import { useQuery } from "@tanstack/react-query";
import { useEffect, useState, type PropsWithChildren } from "react";
import { Activity, AppWindow, Archive, Boxes, Database, Menu, Moon, Network, ServerCog, Sun, X } from "lucide-react";
import { Link, useLocation } from "wouter";
import { api } from "../api/client.ts";
import { CommandPalette } from "./CommandPalette.tsx";
import { keys } from "../api/keys.ts";
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
    /* storage can be unavailable */
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
      /* use default */
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
  const dockerOk = system.isSuccess && !system.data.dockerError;
  const crumbs =
    location
      .split("?")[0]
      ?.split("/")
      .filter(Boolean)
      .map((part) => decodeURIComponent(part)) ?? [];
  return (
    <div
      className="min-h-screen md:grid"
      style={{ gridTemplateColumns: collapsed ? "4.5rem minmax(0,1fr)" : "15rem minmax(0,1fr)" }}
    >
      {menuOpen && (
        <button
          className="fixed inset-0 z-40 bg-black/50 md:hidden"
          aria-label="Close navigation"
          onClick={() => setMenuOpen(false)}
        />
      )}
      <aside
        className={`fixed inset-y-0 left-0 z-50 flex w-60 flex-col border-r bg-sidebar transition-transform md:sticky md:top-0 md:h-screen md:w-auto ${menuOpen ? "visible translate-x-0" : "invisible -translate-x-full md:visible md:translate-x-0"}`}
      >
        <div className="flex h-16 items-center gap-3 border-b px-4">
          <Link
            href="/"
            className="flex min-w-0 flex-1 items-center gap-3 text-inherit no-underline"
            onClick={() => setMenuOpen(false)}
          >
            <img src="/bento-logo-3d.png" alt="" className="size-9 rounded-lg" />
            {!collapsed && <strong>Bento</strong>}
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
        <nav className="grid gap-1 p-2" aria-label="Main navigation">
          {navigation.map((item) => {
            const active =
              item.href === "/" ? location === "/" : location === item.href || location.startsWith(`${item.href}/`);
            const Icon = item.icon;
            return (
              <Link
                key={item.href}
                href={item.href}
                title={collapsed ? item.label : undefined}
                onClick={() => setMenuOpen(false)}
                className={`flex h-10 items-center gap-3 rounded-md px-3 text-sm font-medium no-underline ${active ? "bg-sidebar-primary text-sidebar-primary-foreground" : "text-sidebar-foreground hover:bg-sidebar-accent"}`}
              >
                <Icon className="size-4 shrink-0" />
                {!collapsed && item.label}
              </Link>
            );
          })}
        </nav>
        <div className="mt-auto border-t p-3">
          <div className="flex items-center gap-2 text-xs" title={system.data?.dockerError ?? ""}>
            <span className={`size-2.5 shrink-0 rounded-full ${dockerOk ? "bg-success" : "bg-destructive"}`} />
            {!collapsed && (
              <span className="min-w-0 truncate">
                {dockerOk ? `Docker ${system.data.dockerVersion}` : "Docker unavailable"}
              </span>
            )}
          </div>
          {!collapsed && (
            <div className="mt-1 truncate text-xs text-muted-foreground">
              {system.data ? `Stack ${system.data.stackName}` : "Loading stack…"}
            </div>
          )}
          <Button
            className="mt-3 hidden w-full md:inline-flex"
            size="sm"
            variant="ghost"
            onClick={() => setCollapsed((value) => !value)}
          >
            {collapsed ? <Menu /> : "Collapse sidebar"}
          </Button>
        </div>
      </aside>
      <div className="min-w-0">
        <header className="sticky top-0 z-30 flex h-16 items-center gap-3 border-b bg-background/95 px-4 backdrop-blur md:px-6">
          <Button
            className="md:hidden"
            size="icon-sm"
            variant="ghost"
            aria-label="Open navigation"
            onClick={() => setMenuOpen(true)}
          >
            <Menu />
          </Button>
          <nav className="min-w-0 flex-1 truncate text-sm text-muted-foreground" aria-label="Breadcrumb">
            <Link href="/" className="text-inherit no-underline">
              Bento
            </Link>
            {crumbs.map((crumb) => (
              <span key={crumb}>
                {" "}
                / <span className="capitalize text-foreground">{crumb}</span>
              </span>
            ))}
          </nav>
          <CommandPalette />
          <Link
            href="/activity"
            className="relative inline-flex size-9 items-center justify-center rounded-md border text-foreground"
            aria-label={`${activeOps} active operations`}
          >
            <Activity className="size-4" />
            {activeOps > 0 && (
              <span className="absolute -top-1 -right-1 min-w-4 rounded-full bg-info px-1 text-center text-xs text-white">
                {activeOps}
              </span>
            )}
          </Link>
          <label className="relative">
            <span className="sr-only">Theme</span>
            {theme === "dark" ? (
              <Moon className="pointer-events-none absolute top-2.5 left-2 size-4" />
            ) : (
              <Sun className="pointer-events-none absolute top-2.5 left-2 size-4" />
            )}
            <NativeSelect
              className="w-24 pl-8"
              value={theme}
              onChange={(event) => setTheme(event.target.value as Theme)}
            >
              <option value="system">System</option>
              <option value="light">Light</option>
              <option value="dark">Dark</option>
            </NativeSelect>
          </label>
          <Button variant="outline" size="sm" onClick={() => logout.mutate()} disabled={logout.isPending}>
            Sign out
          </Button>
        </header>
        <main>{children}</main>
      </div>
    </div>
  );
}
