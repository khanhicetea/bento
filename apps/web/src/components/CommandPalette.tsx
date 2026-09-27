import { useEffect, useState, type KeyboardEvent } from "react";
import { Activity, Archive, Boxes, Database, LayoutGrid, Network, Plus, Search, Server } from "lucide-react";
import { useLocation } from "wouter";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";

const commands = [
  { href: "/", label: "Home", keywords: "overview dashboard health", icon: LayoutGrid },
  { href: "/apps", label: "Apps", keywords: "applications runtimes", icon: Boxes },
  { href: "/apps/new", label: "New app", keywords: "create application", icon: Plus },
  { href: "/data", label: "Data services", keywords: "mysql postgres redis database", icon: Database },
  { href: "/backups", label: "Backups", keywords: "artifacts restore schedule", icon: Archive },
  { href: "/ingress", label: "Ingress", keywords: "edge tunnel proxies routes", icon: Network },
  { href: "/activity", label: "Activity", keywords: "operations events", icon: Activity },
  { href: "/system", label: "System", keywords: "docker retained theme", icon: Server },
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
      <button type="button" className="search-btn" onClick={() => setOpen(true)} aria-label="Open command palette">
        <Search className="size-4" />
        <span>Search</span>
        <kbd>⌘K</kbd>
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
              placeholder="Go to…"
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
              <p className="p-4 text-center text-sm text-muted-foreground">Nothing found</p>
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
                    className={`flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm ${index === active ? "bg-secondary" : ""}`}
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
        </DialogContent>
      </Dialog>
    </>
  );
}
