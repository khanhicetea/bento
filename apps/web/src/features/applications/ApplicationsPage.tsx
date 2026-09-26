import { useEffect, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import type { Application } from "@bento/shared";
import {
  ArrowUpRight,
  CircleCheck,
  Copy,
  Database,
  Gauge,
  Globe2,
  LockKeyhole,
  MoreHorizontal,
  Pencil,
  Power,
  Rocket,
  ScrollText,
  Search,
  Server,
  SquareTerminal,
  Trash2,
  X,
} from "lucide-react";
import { ApplicationDatabasesDialog } from "./ApplicationDatabasesDialog.tsx";
import { ApplicationEditor } from "./ApplicationEditor.tsx";
import { ApplicationPublicKeyDialog } from "./ApplicationPublicKeyDialog.tsx";
import { orpc } from "../../api/client.ts";
import { TerminalDialog } from "../../components/TerminalDialog.tsx";
import { RemoveApplicationDialog } from "./RemoveApplicationDialog.tsx";
import { useApplications } from "./useApplications.ts";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Alert } from "@/components/ui/alert";
import { Spinner } from "@/components/ui/spinner";

type StatusFilter = "all" | "running" | "disabled";

export function ApplicationsPage() {
  const {
    data,
    error,
    loading,
    changing,
    saving,
    addingDatabase,
    removing,
    reload,
    setEnabled,
    setRunning,
    saveApplication,
    addDatabase,
    removeApplication,
    resetErrors,
  } = useApplications();
  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [editorTarget, setEditorTarget] = useState<Application | "create" | null>(null);
  const [databaseTarget, setDatabaseTarget] = useState<Application | null>(null);
  const schedulerAccess = useQuery(orpc.jobs.schedulerAccess.queryOptions({ input: {} }));
  const [terminalTarget, setTerminalTarget] = useState<Application | null>(null);
  const [removeTarget, setRemoveTarget] = useState<Application | null>(null);
  const [keyTarget, setKeyTarget] = useState<Application | null>(null);
  const normalizedQuery = query.trim().toLowerCase();
  const allApplications = data?.applications ?? [];
  const applications = allApplications.filter((app) => {
    const matchesQuery = `${app.slug} ${app.domain} ${app.aliases.join(" ")}`.toLowerCase().includes(normalizedQuery);
    const matchesStatus = statusFilter === "all" || (statusFilter === "running" ? app.enabled : !app.enabled);
    return matchesQuery && matchesStatus;
  });
  const canCreate = Boolean(data?.initialized && data.phpVersions.length);

  function startCreating() {
    resetErrors();
    setEditorTarget("create");
  }

  return (
    <section className="mx-auto w-full max-w-[1800px] p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4" aria-live="polite">
      <div className="flex items-end justify-between gap-6 max-[760px]:items-stretch max-[760px]:flex-col">
        <div>
          <p className="mb-2 text-[0.68rem] font-semibold uppercase tracking-[0.16em] text-muted-foreground">
            Control plane / Applications
          </p>
          <h2 className="m-0 text-[clamp(1.7rem,3vw,2.35rem)] tracking-tight">Applications</h2>
          <p className="m-0 mt-2 max-w-[620px] text-sm text-muted-foreground">
            Deploy, configure, and monitor the applications running on this stack.
          </p>
        </div>
        <div className="flex items-center gap-2 max-[760px]:w-full">
          <Button className="max-[760px]:flex-1" variant="outline" disabled={loading} onClick={() => void reload()}>
            {loading && <Spinner />}
            Refresh
          </Button>
          <Button className="max-[760px]:flex-1" disabled={!canCreate} onClick={startCreating}>
            <span aria-hidden="true">+</span> New application
          </Button>
        </div>
      </div>

      {error &&
        editorTarget === null &&
        databaseTarget === null &&
        terminalTarget === null &&
        removeTarget === null &&
        keyTarget === null && (
          <Alert className="mt-6" variant="destructive">
            <span>{error}</span>
          </Alert>
        )}
      {loading && !data && (
        <div className="flex min-h-[45vh] items-center justify-center gap-3 text-muted-foreground">
          <Spinner className="size-8" /> Loading applications…
        </div>
      )}
      {data && !data.initialized && (
        <div className="mt-8 flex items-end justify-between gap-8 overflow-hidden rounded-2xl bg-gradient-to-br from-sidebar to-primary p-[clamp(1.4rem,4vw,2.7rem)] text-sidebar-foreground shadow-lg max-[760px]:block">
          <div>
            <p className="m-0 text-[0.68rem] font-bold tracking-[0.14em] text-sidebar-foreground/70">STACK NOT READY</p>
            <h2 className="my-2 text-[clamp(1.5rem,4vw,2.5rem)]">
              Initialize this stack before managing applications.
            </h2>
            <p className="m-0 max-w-[680px] opacity-70">
              {data.error ?? (
                <>
                  Run <code>bento init</code> for <code>{data.stackRoot}</code>, then refresh.
                </>
              )}
            </p>
          </div>
        </div>
      )}
      {data?.initialized && (
        <>
          <div className="mt-8 grid grid-cols-3 gap-3 max-[560px]:grid-cols-1">
            <Summary value={allApplications.length} label="Total applications" icon={<Server className="size-4" />} />
            <Summary
              value={allApplications.filter((app) => app.enabled).length}
              label="Running now"
              icon={<CircleCheck className="size-4" />}
              tone="success"
            />
            <Summary
              value={allApplications.reduce((sum, app) => sum + app.databases.length, 0)}
              label="Attached databases"
              icon={<Database className="size-4" />}
            />
          </div>

          <div className="mt-6 flex flex-wrap items-center justify-between gap-3">
            <div
              className="flex items-center gap-1 rounded-lg border border-border bg-muted/50 p-1"
              role="group"
              aria-label="Filter applications by status"
            >
              {(
                [
                  ["all", "All"],
                  ["running", "Running"],
                  ["disabled", "Disabled"],
                ] as const
              ).map(([value, label]) => (
                <button
                  key={value}
                  type="button"
                  className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${statusFilter === value ? "bg-card text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
                  aria-pressed={statusFilter === value}
                  onClick={() => setStatusFilter(value)}
                >
                  {label}
                  <span className="ml-1.5 opacity-60">
                    {value === "all"
                      ? allApplications.length
                      : value === "running"
                        ? allApplications.filter((app) => app.enabled).length
                        : allApplications.filter((app) => !app.enabled).length}
                  </span>
                </button>
              ))}
            </div>
            <div className="flex min-w-[min(100%,340px)] flex-1 items-center gap-2 rounded-xl border border-border bg-card px-3 py-1.5 shadow-sm focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/20 max-[760px]:min-w-full">
              <Search className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
              <Input
                className="h-8 min-w-0 flex-1 border-0 bg-transparent px-1 shadow-none focus-visible:ring-0"
                type="search"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Search by name, domain, or alias…"
                aria-label="Search applications"
              />
              {query && (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  className="shrink-0 rounded-full"
                  aria-label="Clear application search"
                  onClick={() => setQuery("")}
                >
                  <X />
                </Button>
              )}
            </div>
          </div>
          <div className="mb-4 mt-3 flex items-center justify-between gap-4 text-xs text-muted-foreground">
            <p className="m-0">
              Showing <strong className="font-semibold text-foreground">{applications.length}</strong> of{" "}
              {allApplications.length} applications
            </p>
            {statusFilter !== "all" && (
              <button
                type="button"
                className="underline underline-offset-2 hover:text-foreground"
                onClick={() => setStatusFilter("all")}
              >
                Show all
              </button>
            )}
          </div>
          <div className="grid grid-cols-2 items-stretch gap-5 max-[760px]:grid-cols-1">
            {applications.map((app) => (
              <ApplicationCard
                key={app.slug}
                app={app}
                busy={changing === app.slug}
                onToggle={() => setEnabled(app)}
                onStart={() => setRunning(app, "start")}
                onStop={() => setRunning(app, "stop")}
                onEdit={() => {
                  resetErrors();
                  setEditorTarget(app);
                }}
                onDatabases={() => {
                  resetErrors();
                  setDatabaseTarget(app);
                }}
                schedulerPath={schedulerAccess.data?.schedulers.find((item) => item.app === app.slug)?.path}
                schedulerUnavailableReason={schedulerAccess.data?.reason}
                onTerminal={() => setTerminalTarget(app)}
                onPublicKey={() => setKeyTarget(app)}
                onRemove={() => {
                  resetErrors();
                  setRemoveTarget(app);
                }}
              />
            ))}
            {!applications.length && (
              <div className="col-span-full rounded-2xl border border-dashed border-border bg-card px-6 py-16 text-center shadow-sm max-[760px]:col-span-1">
                <div className="mx-auto grid size-12 place-items-center rounded-2xl bg-muted text-muted-foreground">
                  <Search className="size-5" aria-hidden="true" />
                </div>
                <h3 className="mb-2 mt-4 text-lg font-semibold">
                  {query || statusFilter !== "all" ? "No matching applications" : "No applications yet"}
                </h3>
                <p className="mx-auto mb-5 max-w-md text-sm text-muted-foreground">
                  {query || statusFilter !== "all"
                    ? "Try a different search or reset the status filter."
                    : "Create an application to provision its runtime, domain, TLS, and database binding."}
                </p>
                {!query && statusFilter === "all" && (
                  <Button disabled={!canCreate} onClick={startCreating}>
                    <span aria-hidden="true">+</span> Create your first application
                  </Button>
                )}
              </div>
            )}
          </div>
        </>
      )}
      {data?.initialized && editorTarget !== null && (
        <ApplicationEditor
          key={editorTarget === "create" ? "create" : editorTarget.slug}
          application={editorTarget === "create" ? null : editorTarget}
          settings={data}
          error={error}
          saving={saving}
          onClose={() => setEditorTarget(null)}
          onSave={saveApplication}
        />
      )}
      {data?.initialized && databaseTarget && (
        <ApplicationDatabasesDialog
          key={databaseTarget.slug}
          application={databaseTarget}
          settings={data}
          error={error}
          adding={addingDatabase}
          onClose={() => setDatabaseTarget(null)}
          onAdd={addDatabase}
        />
      )}
      {terminalTarget && (
        <TerminalDialog
          key={terminalTarget.slug}
          target={{ app: terminalTarget.slug }}
          onClose={() => setTerminalTarget(null)}
        />
      )}
      {keyTarget && (
        <ApplicationPublicKeyDialog key={keyTarget.slug} application={keyTarget} onClose={() => setKeyTarget(null)} />
      )}
      {removeTarget && (
        <RemoveApplicationDialog
          key={removeTarget.slug}
          application={removeTarget}
          error={error}
          removing={removing}
          onClose={() => setRemoveTarget(null)}
          onRemove={removeApplication}
        />
      )}
    </section>
  );
}

function Summary({
  value,
  label,
  icon,
  tone = "default",
}: {
  value: number;
  label: string;
  icon: ReactNode;
  tone?: "default" | "success";
}) {
  return (
    <div className="flex items-center gap-3 rounded-xl border border-border bg-card px-4 py-3 text-card-foreground shadow-sm">
      <span
        className={`grid size-9 shrink-0 place-items-center rounded-lg ${tone === "success" ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400" : "bg-muted text-muted-foreground"}`}
      >
        {icon}
      </span>
      <span className="min-w-0">
        <strong className="block text-xl leading-none tracking-tight">{value}</strong>
        <span className="mt-1 block truncate text-xs text-muted-foreground">{label}</span>
      </span>
    </div>
  );
}

function ApplicationCard({
  app,
  busy,
  onToggle,
  onStart,
  onStop,
  onEdit,
  onDatabases,
  schedulerPath,
  schedulerUnavailableReason,
  onTerminal,
  onPublicKey,
  onRemove,
}: {
  app: Application;
  busy: boolean;
  onToggle: () => void;
  onStart: () => void;
  onStop: () => void;
  onEdit: () => void;
  onDatabases: () => void;
  schedulerPath?: string;
  schedulerUnavailableReason?: string;
  onTerminal: () => void;
  onPublicKey: () => void;
  onRemove: () => void;
}) {
  const visibleAliases = app.aliases.slice(0, 2);
  const extraAliases = app.aliases.length - visibleAliases.length;
  const visibleDatabases = app.databases.slice(0, 2);
  const extraDatabases = app.databases.length - visibleDatabases.length;
  const [moreOpen, setMoreOpen] = useState(false);
  const moreActionsRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!moreOpen) return;

    function closeOnOutsideClick(event: MouseEvent) {
      if (event.target instanceof Node && !moreActionsRef.current?.contains(event.target)) {
        setMoreOpen(false);
      }
    }

    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape") setMoreOpen(false);
    }

    document.addEventListener("mousedown", closeOnOutsideClick);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("mousedown", closeOnOutsideClick);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [moreOpen]);

  return (
    <article
      className={`group relative flex min-w-0 flex-col overflow-hidden rounded-2xl border bg-card text-card-foreground shadow-sm transition-all hover:-translate-y-0.5 hover:shadow-md ${app.enabled ? "border-emerald-500/25" : "border-border"}`}
      aria-busy={busy}
    >
      <div className={`h-1 w-full ${app.enabled ? "bg-emerald-500" : "bg-muted-foreground/30"}`} />
      <div className="flex flex-1 flex-col gap-5 p-5">
        <div className="flex items-start justify-between gap-4">
          <div className="flex min-w-0 items-center gap-3">
            <span
              className={`grid size-11 shrink-0 place-items-center rounded-xl text-lg font-bold ${app.enabled ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300" : "bg-muted text-muted-foreground"}`}
              aria-hidden="true"
            >
              {app.slug.slice(0, 1).toUpperCase()}
            </span>
            <div className="min-w-0">
              <h3 className="m-0 truncate text-base font-semibold tracking-tight">{app.slug}</h3>
              <a
                className="mt-1 flex min-w-0 max-w-full items-center gap-1 text-sm text-muted-foreground no-underline hover:text-primary hover:underline hover:underline-offset-2"
                href={`https://${app.domain}`}
                target="_blank"
                rel="noreferrer"
                title={`Open ${app.domain}`}
              >
                <Globe2 className="size-3.5 shrink-0" aria-hidden="true" />
                <span className="truncate">{app.domain}</span>
                <ArrowUpRight className="size-3.5 shrink-0" aria-hidden="true" />
                <span className="sr-only">(opens in a new tab)</span>
              </a>
            </div>
          </div>
          <span
            className={`inline-flex shrink-0 items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium ${app.enabled ? "border-emerald-500/20 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300" : "border-border bg-muted text-muted-foreground"}`}
          >
            <span
              className={`size-1.5 rounded-full ${app.enabled ? "bg-emerald-500" : "bg-muted-foreground/60"}`}
              aria-hidden="true"
            />
            {app.enabled ? "Running" : "Disabled"}
          </span>
        </div>

        <div className="grid grid-cols-2 divide-x divide-y divide-border overflow-hidden rounded-xl border border-border bg-muted/30">
          <Fact
            label="Runtime"
            value={
              app.kind === "php"
                ? `PHP ${app.phpVersion}`
                : `${formatLabel(app.processRuntime?.language ?? "process")} ${app.processRuntime?.version ?? ""}`
            }
          />
          <Fact
            label={app.kind === "php" ? "Capacity" : "Service"}
            value={app.kind === "php" ? (app.fpmProfile ?? "-") : (app.processRuntime?.service ?? "-")}
            icon={<Gauge className="size-3.5" />}
          />
          <Fact
            label={app.kind === "php" ? "Document root" : "Working directory"}
            value={app.kind === "php" ? (app.documentRoot ?? "-") : (app.processRuntime?.workdir ?? "-")}
          />
          <Fact label="TLS" value={formatLabel(app.tls)} icon={<LockKeyhole className="size-3.5" />} />
        </div>

        <div className="min-h-[3.25rem]">
          <p className="mb-2 flex items-center gap-1.5 text-[0.68rem] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
            Configuration
          </p>
          <div className="flex flex-wrap gap-1.5">
            {visibleDatabases.map((database, index) => (
              <Badge
                variant={index === 0 ? "default" : "outline"}
                className="gap-1.5 px-2.5 py-1"
                key={`${database.engine}:${database.service ?? database.file ?? index}`}
              >
                <Database className="size-3" aria-hidden="true" />
                {database.engine}
                {database.names.length > 0 ? ` · ${database.names.length}` : ""}
              </Badge>
            ))}
            {extraDatabases > 0 && <Badge variant="outline">+{extraDatabases} databases</Badge>}
            {app.databases.length === 0 && <Badge variant="outline">No database attached</Badge>}
            {app.deployEnabled && (
              <Badge
                className="gap-1.5 bg-emerald-600 px-2.5 py-1 text-white"
                title={
                  app.deploySummary
                    ? `${app.deploySummary.queuePolicy} queue · ${app.deploySummary.timeoutSec}s timeout · ${app.deploySummary.command}`
                    : undefined
                }
              >
                <Rocket className="size-3" aria-hidden="true" /> Deploys
              </Badge>
            )}
            {app.accessLog && (
              <Badge variant="outline" className="gap-1.5 px-2.5 py-1">
                <ScrollText className="size-3" aria-hidden="true" /> Access logs
              </Badge>
            )}
            {visibleAliases.map((alias) => (
              <Badge variant="outline" className="max-w-full px-2.5 py-1" key={alias}>
                <span className="max-w-[170px] truncate">{alias}</span>
              </Badge>
            ))}
            {extraAliases > 0 && <Badge variant="outline">+{extraAliases} aliases</Badge>}
          </div>
        </div>

        <div className="mt-auto border-t border-border pt-4">
          <div className="grid grid-cols-[repeat(3,minmax(0,1fr))_auto] gap-2 max-[520px]:grid-cols-2">
            <Button variant="outline" size="sm" className="w-full min-w-0 px-2" disabled={busy} onClick={onDatabases}>
              <Database className="size-3.5 shrink-0" aria-hidden="true" />
              <span className="truncate">Databases</span>
            </Button>
            {schedulerPath && !busy ? (
              <Button asChild variant="outline" size="sm" className="w-full min-w-0 px-2">
                <a href={schedulerPath} target="_blank" rel="noreferrer">
                  <ScrollText className="size-3.5 shrink-0" aria-hidden="true" />
                  <span className="truncate">Scheduler</span>
                  <span className="sr-only">(opens in a new tab)</span>
                </a>
              </Button>
            ) : (
              <Button
                variant="outline"
                size="sm"
                className="w-full min-w-0 px-2"
                disabled
                title={
                  app.kind === "process"
                    ? "Process app jobs are not supported yet"
                    : (schedulerUnavailableReason ?? "Scheduler unavailable for this application")
                }
              >
                <ScrollText className="size-3.5 shrink-0" aria-hidden="true" />
                <span className="truncate">Scheduler</span>
              </Button>
            )}
            <Button variant="outline" size="sm" className="w-full min-w-0 px-2" disabled={busy} onClick={onEdit}>
              <Pencil className="size-3.5 shrink-0" aria-hidden="true" />
              <span className="truncate">Edit</span>
            </Button>
            <div ref={moreActionsRef} className="relative">
              <Button
                type="button"
                variant="outline"
                size="icon-sm"
                disabled={busy}
                aria-label={`More actions for ${app.slug}`}
                aria-controls={`${app.slug}-more-actions`}
                aria-expanded={moreOpen}
                onClick={() => setMoreOpen((open) => !open)}
              >
                <MoreHorizontal className="size-4" aria-hidden="true" />
              </Button>
              {moreOpen && (
                <div
                  id={`${app.slug}-more-actions`}
                  className="absolute right-0 bottom-[calc(100%+0.5rem)] z-30 w-48 overflow-hidden rounded-xl border border-border bg-popover p-1 text-popover-foreground shadow-xl"
                  role="menu"
                  aria-label={`More actions for ${app.slug}`}
                >
                  <button
                    type="button"
                    role="menuitem"
                    className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm hover:bg-accent disabled:pointer-events-none disabled:opacity-50"
                    disabled={busy}
                    onClick={() => {
                      setMoreOpen(false);
                      onTerminal();
                    }}
                  >
                    <SquareTerminal className="size-3.5" aria-hidden="true" />
                    Open shell
                  </button>
                  <button
                    type="button"
                    role="menuitem"
                    className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm hover:bg-accent disabled:pointer-events-none disabled:opacity-50"
                    disabled={busy}
                    onClick={() => {
                      setMoreOpen(false);
                      onPublicKey();
                    }}
                  >
                    <Copy className="size-3.5" aria-hidden="true" />
                    Copy deploy key
                  </button>
                  {app.kind === "process" && (
                    <>
                      <button
                        type="button"
                        role="menuitem"
                        className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm hover:bg-accent disabled:pointer-events-none disabled:opacity-50"
                        disabled={busy}
                        onClick={() => {
                          setMoreOpen(false);
                          onStart();
                        }}
                      >
                        <Power className="size-3.5" aria-hidden="true" />
                        Start / rebuild privately
                      </button>
                      <button
                        type="button"
                        role="menuitem"
                        className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm hover:bg-accent disabled:pointer-events-none disabled:opacity-50"
                        disabled={busy}
                        onClick={() => {
                          setMoreOpen(false);
                          onStop();
                        }}
                      >
                        <Power className="size-3.5" aria-hidden="true" />
                        Stop private service
                      </button>
                    </>
                  )}
                  <button
                    type="button"
                    role="menuitem"
                    className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm hover:bg-accent disabled:pointer-events-none disabled:opacity-50"
                    disabled={busy}
                    onClick={() => {
                      setMoreOpen(false);
                      onToggle();
                    }}
                  >
                    {busy ? <Spinner /> : <Power className="size-3.5" aria-hidden="true" />}
                    {app.enabled ? "Disable" : "Enable"}
                  </button>
                  <div className="my-1 border-t border-border" />
                  <button
                    type="button"
                    role="menuitem"
                    className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm text-destructive hover:bg-destructive/10 disabled:pointer-events-none disabled:opacity-50"
                    disabled={busy}
                    onClick={() => {
                      setMoreOpen(false);
                      onRemove();
                    }}
                  >
                    <Trash2 className="size-3.5" aria-hidden="true" />
                    Delete application
                  </button>
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
    </article>
  );
}

function Fact({ label, value, icon }: { label: string; value: string; icon?: ReactNode }) {
  return (
    <div className="min-w-0 p-3 first:rounded-tl-xl [&:nth-child(2)]:rounded-tr-xl [&:nth-child(3)]:rounded-bl-xl [&:nth-child(4)]:rounded-br-xl">
      <span className="flex items-center gap-1.5 text-[0.68rem] text-muted-foreground">
        {icon}
        {label}
      </span>
      <strong className="mt-1 block truncate text-sm font-medium" title={value}>
        {value}
      </strong>
    </div>
  );
}

function formatLabel(value: string) {
  return value
    .split("-")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}
