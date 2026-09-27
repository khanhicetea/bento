import { useQuery } from "@tanstack/react-query";
import { useEffect, useState, type PropsWithChildren } from "react";
import { Link, useLocation } from "wouter";
import { api } from "../api/client.ts";
import { keys } from "../api/keys.ts";
import { useLogout } from "../features/session/useSession.ts";
import { Button } from "@/components/ui/button";

const navigation = [
  { href: "/applications", icon: "◫", label: "Applications" },
  { href: "/databases", icon: "▤", label: "Data services" },
  { href: "/backups", icon: "◧", label: "Backups" },
  { href: "/routing", icon: "↗", label: "Ingress" },
  { href: "/operations", icon: "✓", label: "Operations" },
] as const;

export function AppShell({ children }: PropsWithChildren) {
  const [location] = useLocation();
  const [menuOpen, setMenuOpen] = useState(false);
  const system = useQuery({
    queryKey: keys.system,
    queryFn: ({ signal }) => api.system.status(signal),
    refetchInterval: 30_000,
  });
  const logout = useLogout();
  const [theme, setTheme] = useState<"light" | "dark">(() => {
    const stored = localStorage.getItem("bento-theme");
    if (stored === "dark" || stored === "light") return stored;
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

  const dockerOk = system.isSuccess && !system.data.dockerError;
  return (
    <div className="min-h-screen">
      <header className="sticky top-0 z-40 border-b border-border bg-card/95 backdrop-blur supports-[backdrop-filter]:bg-card/60">
        <div className="flex min-h-[68px] items-center gap-[clamp(0.75rem,2vw,2rem)] px-[clamp(1rem,2.5vw,2.5rem)] py-[0.65rem] max-[900px]:relative">
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
              <small className="block text-[0.65rem] tracking-[0.08em] text-muted-foreground uppercase max-[480px]:hidden">
                {system.data ? `stack ${system.data.stackName}` : "Control plane"}
              </small>
            </span>
          </Link>
          <Button
            className="hidden max-[900px]:ml-auto max-[900px]:inline-flex"
            variant="ghost"
            size="icon-sm"
            aria-label="Toggle navigation"
            aria-expanded={menuOpen}
            onClick={() => setMenuOpen((open) => !open)}
          >
            ☰
          </Button>
          <nav
            className={`flex min-w-0 items-center gap-1 max-[900px]:absolute max-[900px]:top-[calc(100%+0.4rem)] max-[900px]:right-4 max-[900px]:left-4 max-[900px]:grid-cols-2 max-[900px]:gap-1.5 max-[900px]:rounded-lg max-[900px]:border max-[900px]:border-border max-[900px]:bg-card max-[900px]:p-2 max-[900px]:shadow-2xl ${menuOpen ? "max-[900px]:grid" : "max-[900px]:hidden"}`}
            aria-label="Main navigation"
          >
            {navigation.map((item) => (
              <Link
                key={item.href}
                href={item.href}
                className={`flex items-center gap-2 rounded-md px-3 py-2 text-[0.84rem] font-medium whitespace-nowrap text-muted-foreground no-underline hover:bg-accent hover:text-accent-foreground ${location === item.href ? "bg-primary text-primary-foreground hover:bg-primary/90 hover:text-primary-foreground" : ""}`}
                onClick={() => setMenuOpen(false)}
              >
                <span className="w-4 text-center">{item.icon}</span>
                <span>{item.label}</span>
              </Link>
            ))}
          </nav>
          <div className="ml-auto flex items-center gap-3 max-[900px]:ml-0">
            <div
              className="flex items-center gap-2 text-xs whitespace-nowrap text-muted-foreground max-[1050px]:hidden"
              title={system.data?.dockerError ?? ""}
            >
              <span
                className={`size-2.5 rounded-full ${dockerOk ? "bg-emerald-500 ring-4 ring-emerald-500/20" : "bg-red-500 ring-4 ring-red-500/20"}`}
              />
              <span>{dockerOk ? `Docker ${system.data.dockerVersion}` : "Docker unavailable"}</span>
            </div>
            <Button variant="ghost" size="icon-sm" aria-label="Toggle theme" onClick={toggleTheme}>
              ◐
            </Button>
            <Button variant="outline" size="sm" onClick={() => logout.mutate()} disabled={logout.isPending}>
              Sign out
            </Button>
          </div>
        </div>
      </header>
      <main className="min-w-0">{children}</main>
    </div>
  );
}
