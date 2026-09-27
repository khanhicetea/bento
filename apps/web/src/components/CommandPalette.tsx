import { useEffect, useState, type KeyboardEvent } from "react";
import { Activity, AppWindow, Archive, Boxes, Database, Network, Search, ServerCog } from "lucide-react";
import { useLocation } from "wouter";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";

const commands = [
  { href: "/", label: "Overview", keywords: "dashboard health", icon: Boxes },
  { href: "/apps", label: "Applications", keywords: "apps runtimes", icon: AppWindow },
  { href: "/apps/new", label: "Create application", keywords: "new app", icon: AppWindow },
  { href: "/data", label: "Data services", keywords: "mysql postgres redis database", icon: Database },
  { href: "/backups", label: "Backups", keywords: "artifacts restore schedule", icon: Archive },
  { href: "/ingress", label: "Ingress", keywords: "edge tunnel proxies routes", icon: Network },
  { href: "/activity", label: "Activity", keywords: "operations events", icon: Activity },
  { href: "/system", label: "System", keywords: "docker retained theme", icon: ServerCog },
] as const;

export function CommandPalette() {
  const [, navigate] = useLocation();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  useEffect(() => {
    const shortcut = (event: globalThis.KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setOpen((value) => !value);
      }
    };
    addEventListener("keydown", shortcut);
    return () => removeEventListener("keydown", shortcut);
  }, []);
  const matches = commands.filter((command) =>
    `${command.label} ${command.keywords}`.toLowerCase().includes(query.trim().toLowerCase()),
  );
  const select = (href: string) => {
    setOpen(false);
    setQuery("");
    setActive(0);
    navigate(href);
  };
  function keyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActive((value) => (matches.length ? (value + 1) % matches.length : 0));
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      setActive((value) => (matches.length ? (value - 1 + matches.length) % matches.length : 0));
    }
    if (event.key === "Enter" && matches[active]) {
      event.preventDefault();
      select(matches[active].href);
    }
  }
  return (
    <>
      <button
        type="button"
        className="hidden h-9 items-center gap-2 rounded-md border px-3 text-sm text-muted-foreground hover:bg-accent sm:flex"
        onClick={() => setOpen(true)}
        aria-label="Open command palette"
      >
        <Search className="size-4" />
        <span>Navigate</span>
        <kbd className="rounded border bg-muted px-1.5 py-0.5 text-xs">⌘K</kbd>
      </button>
      <button
        type="button"
        className="inline-flex size-9 items-center justify-center rounded-md border sm:hidden"
        onClick={() => setOpen(true)}
        aria-label="Open command palette"
      >
        <Search className="size-4" />
      </button>
      <Dialog
        open={open}
        onOpenChange={(value) => {
          setOpen(value);
          if (!value) {
            setQuery("");
            setActive(0);
          }
        }}
      >
        <DialogContent className="top-[20%] translate-y-0 p-0 sm:max-w-xl" showCloseButton={false}>
          <DialogHeader className="sr-only">
            <DialogTitle>Navigate</DialogTitle>
            <DialogDescription>Search control-plane destinations.</DialogDescription>
          </DialogHeader>
          <div className="flex items-center gap-2 border-b px-3">
            <Search className="size-4 text-muted-foreground" />
            <Input
              autoFocus
              className="h-12 border-0 px-0 shadow-none focus-visible:ring-0"
              placeholder="Search pages…"
              value={query}
              onChange={(event) => {
                setQuery(event.target.value);
                setActive(0);
              }}
              onKeyDown={keyDown}
              role="combobox"
              aria-expanded="true"
              aria-controls="command-results"
              aria-activedescendant={
                matches[active] ? `command-${matches[active].href.replaceAll("/", "-") || "home"}` : undefined
              }
            />
          </div>
          <div id="command-results" role="listbox" className="max-h-80 overflow-y-auto p-2">
            {matches.length === 0 ? (
              <p className="p-4 text-center text-sm text-muted-foreground">No matching pages.</p>
            ) : (
              matches.map((command, index) => {
                const Icon = command.icon;
                const id = `command-${command.href.replaceAll("/", "-") || "home"}`;
                return (
                  <button
                    id={id}
                    key={command.href}
                    type="button"
                    role="option"
                    aria-selected={index === active}
                    className={`flex w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm ${index === active ? "bg-accent text-accent-foreground" : ""}`}
                    onMouseEnter={() => setActive(index)}
                    onClick={() => select(command.href)}
                  >
                    <Icon className="size-4" />
                    {command.label}
                  </button>
                );
              })
            )}
          </div>
          <div className="border-t px-3 py-2 text-xs text-muted-foreground">
            Use ↑ ↓ to choose · Enter to open · Esc to close
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}
