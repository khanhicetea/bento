import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ExternalLink, Terminal as TerminalIcon } from "lucide-react";
import { Link, useLocation } from "wouter";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import { CopyableCode, DomainError, DomainLoading, Field, Page, StateBadge } from "../../components/DomainState.tsx";
import { TerminalPanel } from "../../components/TerminalDialog.tsx";
import { formatRelative } from "../../lib/format.ts";
import { useActiveOperations } from "../operations/useActiveOperations.ts";
import { ApplicationEditor } from "./ApplicationEditor.tsx";
import { LogsPanel } from "./LogsPanel.tsx";
import {
  useAppAction,
  useApplication,
  useApplicationList,
  useOperationMutation,
  type AppAction,
} from "./useApplications.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";

type Tab = "overview" | "logs" | "terminal" | "data" | "scheduler" | "settings";
const tabs: Array<[Tab, string]> = [
  ["overview", "Overview"],
  ["logs", "Logs"],
  ["terminal", "Terminal"],
  ["data", "Data"],
  ["scheduler", "Scheduler"],
  ["settings", "Settings"],
];

export function ApplicationPage({ slug, tab = "overview" }: { slug: string; tab?: Tab }) {
  const list = useApplicationList();
  const summary = list.data?.apps.find((app) => app.slug === slug);
  const query = useApplication(summary?.id ?? null);
  const [, navigate] = useLocation();
  if (list.isPending || (summary && query.isPending))
    return (
      <Page>
        <DomainLoading label="application" />
      </Page>
    );
  if (list.error)
    return (
      <Page>
        <DomainError message={messageOf(list.error)} onRetry={() => void list.refetch()} />
      </Page>
    );
  if (!summary)
    return (
      <Page>
        <DomainError message={`Application “${slug}” was not found.`} />
      </Page>
    );
  if (query.error)
    return (
      <Page>
        <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
      </Page>
    );
  const app = query.data;
  if (!app)
    return (
      <Page>
        <DomainLoading label="application" />
      </Page>
    );
  return (
    <Page wide>
      <AppHeader app={app} />
      <nav className="mb-6 flex gap-1 overflow-x-auto border-b" aria-label="Application sections">
        {tabs.map(([value, label]) => (
          <Link
            key={value}
            href={
              value === "overview" ? `/apps/${encodeURIComponent(slug)}` : `/apps/${encodeURIComponent(slug)}/${value}`
            }
            className={`border-b-2 px-3 py-2 text-sm font-medium whitespace-nowrap no-underline ${tab === value ? "border-primary text-foreground" : "border-transparent text-muted-foreground hover:text-foreground"}`}
            aria-current={tab === value ? "page" : undefined}
          >
            {label}
          </Link>
        ))}
      </nav>
      <OperationBanner app={app} />
      {tab === "overview" && <Overview app={app} />}
      {tab === "logs" && <LogsPanel appId={app.id} />}
      {tab === "terminal" && (
        <TerminalPanel
          appId={app.id}
          mode={new URLSearchParams(window.location.search).get("mode") === "running" ? "running" : "tool"}
          onModeChange={(mode) => navigate(`/apps/${encodeURIComponent(slug)}/terminal?mode=${mode}`)}
        />
      )}
      {tab === "data" && <DataBindings app={app} />}
      {tab === "scheduler" && <Scheduler app={app} />}
      {tab === "settings" && <Settings app={app} onRemoved={() => navigate("/apps")} />}
    </Page>
  );
}

function AppHeader({ app }: { app: T.App }) {
  const action = useAppAction();
  const active = useActiveOperations(app.id);
  const [confirm, setConfirm] = useState<AppAction | null>(null);
  const run = (verb: AppAction) => action.mutate({ id: app.id, action: verb }, { onSuccess: () => setConfirm(null) });
  const request = (verb: AppAction) =>
    verb === "stop" || verb === "restart" || verb === "unpublish" ? setConfirm(verb) : run(verb);
  return (
    <>
      <header className="mb-4 flex flex-wrap items-start justify-between gap-4">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="m-0 text-2xl font-semibold">{app.slug}</h1>
            <StateBadge state={app.observed.state} title={app.observed.message} />
            {!app.observed.generationCurrent && <StateBadge state="pending" label="Generation pending" />}
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
            <span>
              {app.toolchain} {app.version}
            </span>
            {app.primaryDomain && (
              <a
                className="inline-flex items-center gap-1"
                href={`//${app.primaryDomain}`}
                target="_blank"
                rel="noreferrer"
              >
                {app.primaryDomain}
                <ExternalLink className="size-3" />
              </a>
            )}
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            disabled={active.active || action.isPending}
            onClick={() => request(app.desiredRuntime === "stopped" ? "start" : "stop")}
          >
            {app.desiredRuntime === "stopped" ? "Start" : "Stop"}
          </Button>
          <Button
            variant="outline"
            disabled={active.active || action.isPending || app.desiredRuntime === "stopped"}
            onClick={() => request("restart")}
          >
            Restart
          </Button>
          {app.ingress === "managed" && (
            <Button
              variant="outline"
              disabled={active.active || action.isPending || app.desiredRuntime === "stopped"}
              onClick={() => request(app.publication === "published" ? "unpublish" : "publish")}
            >
              {app.publication === "published" ? "Unpublish" : "Publish"}
            </Button>
          )}
          <Button asChild variant="outline">
            <Link href={`/apps/${encodeURIComponent(app.slug)}/terminal`}>
              <TerminalIcon />
              Terminal
            </Link>
          </Button>
        </div>
      </header>
      {action.error && (
        <Alert variant="destructive" className="mb-4">
          {messageOf(action.error)}
        </Alert>
      )}
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(open) => !open && setConfirm(null)}
        title={`${confirm ?? "Change"} ${app.slug}?`}
        description={
          confirm === "unpublish"
            ? "This removes the managed public route."
            : confirm === "stop"
              ? "This stops the running app."
              : "This replaces the running instance after a controlled stop."
        }
        confirmLabel={confirm ?? "Confirm"}
        pending={action.isPending}
        error={action.error}
        onConfirm={() => confirm && run(confirm)}
      />
    </>
  );
}

function OperationBanner({ app }: { app: T.App }) {
  const active = useActiveOperations(app.id);
  if (!active.active) return null;
  const op = active.operations[0];
  return (
    <Alert className="mb-4">
      <StateBadge state={op?.state ?? "queued"} /> <strong className="ml-2">{op?.kind}</strong>{" "}
      <span className="text-muted-foreground">— {op?.events?.at(-1)?.message ?? op?.phase ?? "Queued"}</span>
      {op && (
        <Button asChild className="ml-3" size="xs" variant="outline">
          <Link href={`/activity/${op.id}`}>View operation</Link>
        </Button>
      )}
    </Alert>
  );
}

function Card({ title, children, danger = false }: { title: string; children: ReactNode; danger?: boolean }) {
  return (
    <section className={`rounded-xl border bg-card p-5 ${danger ? "border-destructive/40" : ""}`}>
      <h2 className={`mt-0 text-base font-semibold ${danger ? "text-destructive" : ""}`}>{title}</h2>
      {children}
    </section>
  );
}
function KeyValue({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid gap-1 border-t py-2 text-sm first:border-0 sm:grid-cols-[9rem_1fr]">
      <span className="text-muted-foreground">{label}</span>
      <span className="min-w-0">{children}</span>
    </div>
  );
}

function Overview({ app }: { app: T.App }) {
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Card title="Runtime">
        <KeyValue label="Container">
          {app.observed.containerId ? <CopyableCode value={app.observed.containerId} /> : "—"}
        </KeyValue>
        <KeyValue label="Started">
          <span title={app.observed.startedAt}>{formatRelative(app.observed.startedAt)}</span>
        </KeyValue>
        <KeyValue label="Generation">
          {app.configGeneration} · {app.observed.generationCurrent ? "current" : "pending"}
        </KeyValue>
        <KeyValue label="Command">
          {app.runtime.http ? (
            <code>{app.runtime.http.argv.join(" ")}</code>
          ) : (
            `PHP ${app.runtime.php?.version} · ${app.runtime.php?.pool}`
          )}
        </KeyValue>
      </Card>
      <Card title="Routing">
        <KeyValue label="Ingress">
          {app.ingressInfo.mode} · {app.publication}
        </KeyValue>
        <KeyValue label="Internal URL">
          <CopyableCode value={app.ingressInfo.internalUrl} />
        </KeyValue>
        <KeyValue label="Domains">
          {app.domains.length
            ? app.domains.map((domain) => (
                <div key={domain.name}>
                  <code>{domain.name}</code>
                  {domain.primary && " · primary"}
                </div>
              ))
            : "None"}
        </KeyValue>
        <KeyValue label="TLS">
          {app.route.tls}
          {app.route.redirectHttps && " · redirect HTTPS"}
        </KeyValue>
      </Card>
      <Card title="Resources">
        <KeyValue label="Memory">{app.resources.memoryMb} MB</KeyValue>
        <KeyValue label="CPU">{app.resources.cpuMillis} millicores</KeyValue>
        <KeyValue label="Processes">{app.resources.pids}</KeyValue>
      </Card>
      <Card title="Redis">
        <KeyValue label="User">
          <code>{app.redisUser}</code>
        </KeyValue>
        <KeyValue label="Key prefix">
          <code>{app.redisPrefix}*</code>
        </KeyValue>
      </Card>
      {app.reconcile.failures > 0 && (
        <Card title="Reconciliation" danger>
          <p>
            {app.reconcile.failures} failure(s){app.reconcile.blocked && " · blocked until explicit start or restart"}
          </p>
          <Alert variant="destructive">{app.reconcile.lastError}</Alert>
        </Card>
      )}
    </div>
  );
}

function DataBindings({ app }: { app: T.App }) {
  const [engine, setEngine] = useState("sqlite");
  const addBinding = useOperationMutation(() => {
    const [value, service] = engine.split(":");
    return api.apps.addBinding(app.id, { engine: value as T.Engine, service });
  });
  return (
    <div className="grid gap-4">
      <Alert>Bindings are add-only. Credentials are delivered privately to the app and are never displayed here.</Alert>
      {app.bindings.map((binding) => (
        <BindingCard key={binding.id} appId={app.id} binding={binding} />
      ))}
      <Card title="Add binding">
        <div className="flex flex-wrap items-end gap-2">
          <Field label="Data service">
            <NativeSelect value={engine} onChange={(event) => setEngine(event.target.value)}>
              <option value="sqlite">SQLite</option>
              <ServiceOptions />
            </NativeSelect>
          </Field>
          <Button variant="outline" disabled={addBinding.isPending} onClick={() => addBinding.mutate(undefined)}>
            Add binding
          </Button>
        </div>
        {addBinding.error && (
          <Alert variant="destructive" className="mt-3">
            {messageOf(addBinding.error)}
          </Alert>
        )}
      </Card>
    </div>
  );
}
function BindingCard({ appId, binding }: { appId: string; binding: T.Binding }) {
  const [name, setName] = useState("");
  const add = useOperationMutation(() => api.apps.addDatabase(appId, binding.id, name));
  return (
    <Card title={`${binding.engine}${binding.service ? ` · ${binding.service}` : ""}`}>
      <p className="text-xs text-muted-foreground">
        <code>{binding.id}</code>
      </p>
      {binding.engine === "sqlite" ? (
        <CopyableCode value={binding.sqlitePath ?? ""} />
      ) : (
        <>
          <p className="text-sm">
            User <code>{binding.username}</code> · databases {binding.databases.join(", ") || "none"}
          </p>
          <div className="flex max-w-md gap-2">
            <Input placeholder="Database suffix" value={name} onChange={(event) => setName(event.target.value)} />
            <Button
              variant="outline"
              disabled={!name || add.isPending}
              onClick={() => add.mutate(undefined, { onSuccess: () => setName("") })}
            >
              Add database
            </Button>
          </div>
          {add.error && (
            <Alert variant="destructive" className="mt-3">
              {messageOf(add.error)}
            </Alert>
          )}
        </>
      )}
    </Card>
  );
}
function ServiceOptions() {
  const query = useQuery({ queryKey: keys.services, queryFn: ({ signal }) => api.services.list(signal) });
  return (
    <>
      {(query.data?.services ?? [])
        .filter((service) => service.engine === "mysql" || service.engine === "postgres")
        .map((service) => (
          <option key={service.name} value={`${service.engine}:${service.name}`}>
            {service.engine} {service.version} ({service.name})
          </option>
        ))}
    </>
  );
}
function Scheduler({ app }: { app: T.App }) {
  if (app.desiredRuntime !== "running") return <Alert>Start the app to manage scheduler jobs and workers.</Alert>;
  return (
    <div className="grid gap-3">
      <Alert>
        This trusted, same-origin scheduler view runs inside the app.{" "}
        <a href={app.schedulerPath} target="_blank" rel="noreferrer">
          Open in a new tab
        </a>
        .
      </Alert>
      <iframe
        title={`${app.slug} scheduler`}
        src={app.schedulerPath}
        className="h-[calc(100vh-14rem)] min-h-96 w-full rounded-lg border"
      />
    </div>
  );
}
function Settings({ app, onRemoved }: { app: T.App; onRemoved: () => void }) {
  const [mode, setMode] = useState("check");
  const [removeOpen, setRemoveOpen] = useState(false);
  const permissions = useOperationMutation(() => api.apps.permissions(app.id, mode));
  const remove = useOperationMutation((confirm: string) => api.apps.remove(app.id, confirm));
  return (
    <div className="grid gap-4">
      <ApplicationEditor app={app} embedded onClose={() => undefined} />
      <Card title="Permissions">
        <div className="flex flex-wrap items-end gap-2">
          <Field label="Mode">
            <NativeSelect value={mode} onChange={(event) => setMode(event.target.value)}>
              <option value="check">Check</option>
              <option value="dry-run">Dry run</option>
              <option value="shallow">Shallow repair</option>
              <option value="recursive">Recursive repair</option>
            </NativeSelect>
          </Field>
          <Button variant="outline" disabled={permissions.isPending} onClick={() => permissions.mutate(undefined)}>
            Run permissions task
          </Button>
        </div>
        {permissions.error && (
          <Alert variant="destructive" className="mt-3">
            {messageOf(permissions.error)}
          </Alert>
        )}
      </Card>
      <Card title="Remove application" danger>
        <p className="text-sm">
          Removes routes and containers and retires uid {app.uid}. Home, SQLite files, and relational databases remain
          until separately pruned.
        </p>
        <Button variant="destructive" onClick={() => setRemoveOpen(true)}>
          Remove {app.slug}
        </Button>
      </Card>
      <ConfirmDialog
        open={removeOpen}
        onOpenChange={setRemoveOpen}
        title={`Remove ${app.slug}?`}
        description="This removes runtime resources but retains durable data for explicit pruning."
        phrase={`delete ${app.slug}`}
        destructive
        confirmLabel="Remove application"
        pending={remove.isPending}
        error={remove.error}
        onConfirm={(typed) => remove.mutate(typed, { onSuccess: onRemoved })}
      />
    </div>
  );
}

// Kept for compatibility with any stale imports; app detail is now URL-routed.
export function ApplicationDetail({ id, onClose }: { id: string; onClose: () => void }) {
  const app = useApplication(id);
  if (!app.data) return <DomainLoading label="application" />;
  return (
    <div>
      <Button variant="outline" onClick={onClose}>
        Close
      </Button>
      <Overview app={app.data} />
    </div>
  );
}
