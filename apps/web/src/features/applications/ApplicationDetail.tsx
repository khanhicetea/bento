import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Database, ExternalLink, HardDrive, Plus, Terminal as TerminalIcon } from "lucide-react";
import { Link, useLocation } from "wouter";
import { api, messageOf, type T } from "../../api/client.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import {
  Cell,
  CopyableCode,
  DomainError,
  DomainLoading,
  Field,
  KeyValues,
  PageHeader,
  StateBadge,
} from "../../components/DomainState.tsx";
import { TerminalPanel } from "../../components/TerminalDialog.tsx";
import { keys } from "../../api/keys.ts";
import { formatRelative } from "../../lib/format.ts";
import { useActiveOperations } from "../operations/useActiveOperations.ts";
import { ApplicationEditor } from "./ApplicationEditor.tsx";
import { DeployPanel } from "./DeployPanel.tsx";
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

type Tab = "overview" | "deploy" | "logs" | "terminal" | "data" | "scheduler" | "settings";
const tabs: Array<[Tab, string]> = [
  ["overview", "Overview"],
  ["deploy", "Deploy"],
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
  if (list.isPending || (summary && query.isPending)) return <DomainLoading label="app" />;
  if (list.error) return <DomainError message={messageOf(list.error)} onRetry={() => void list.refetch()} />;
  if (!summary) return <DomainError message={`App “${slug}” not found.`} />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  const app = query.data;
  if (!app) return <DomainLoading label="app" />;
  const base = `/apps/${encodeURIComponent(slug)}`;
  return (
    <>
      <AppHeader app={app} />
      <nav className="seg mb-5" aria-label="App sections">
        {tabs.map(([value, label]) => (
          <Link
            key={value}
            href={value === "overview" ? base : `${base}/${value}`}
            aria-current={tab === value ? "page" : undefined}
          >
            {label}
          </Link>
        ))}
      </nav>
      <OperationBanner app={app} />
      {tab === "overview" && <Overview app={app} />}
      {tab === "deploy" && <DeployPanel app={app} />}
      {tab === "logs" && <LogsPanel appId={app.id} />}
      {tab === "terminal" && (
        <TerminalPanel
          appId={app.id}
          mode={new URLSearchParams(window.location.search).get("mode") === "running" ? "running" : "tool"}
          onModeChange={(mode) => navigate(`${base}/terminal?mode=${mode}`)}
        />
      )}
      {tab === "data" && <DataBindings app={app} />}
      {tab === "scheduler" && <Scheduler app={app} />}
      {tab === "settings" && <Settings app={app} onRemoved={() => navigate("/apps")} />}
    </>
  );
}

function AppHeader({ app }: { app: T.App }) {
  const action = useAppAction();
  const active = useActiveOperations(app.id);
  const [confirm, setConfirm] = useState<AppAction | null>(null);
  const run = (verb: AppAction) => action.mutate({ id: app.id, action: verb }, { onSuccess: () => setConfirm(null) });
  const request = (verb: AppAction) =>
    verb === "stop" || verb === "restart" || verb === "unpublish" ? setConfirm(verb) : run(verb);
  const busy = active.active || action.isPending;
  const stopped = app.desiredRuntime === "stopped";
  return (
    <>
      <PageHeader
        back={{ href: "/apps", label: "Apps" }}
        title={
          <span className="flex flex-wrap items-center gap-3">
            {app.slug}
            <StateBadge state={app.observed.state} title={app.observed.message} />
            {!app.observed.generationCurrent && <StateBadge state="pending" label="Update pending" />}
          </span>
        }
        description={
          <span className="flex flex-wrap items-center gap-2">
            {app.toolchain} {app.version}
            {app.primaryDomain && (
              <>
                <span aria-hidden="true">·</span>
                <a
                  className="inline-flex items-center gap-1"
                  href={`//${app.primaryDomain}`}
                  target="_blank"
                  rel="noreferrer"
                >
                  {app.primaryDomain}
                  <ExternalLink className="size-3" />
                </a>
              </>
            )}
          </span>
        }
        actions={
          <>
            <Button
              disabled={busy}
              variant={stopped ? "default" : "outline"}
              onClick={() => request(stopped ? "start" : "stop")}
            >
              {stopped ? "Start" : "Stop"}
            </Button>
            <Button variant="outline" disabled={busy || stopped} onClick={() => request("restart")}>
              Restart
            </Button>
            {app.ingress === "managed" && (
              <Button
                variant={app.publication === "published" || stopped ? "outline" : "default"}
                disabled={busy || stopped}
                onClick={() => request(app.publication === "published" ? "unpublish" : "publish")}
              >
                {app.publication === "published" ? "Unpublish" : "Publish"}
              </Button>
            )}
            <Button asChild variant="ghost" size="icon" aria-label="Terminal">
              <Link href={`/apps/${encodeURIComponent(app.slug)}/terminal`}>
                <TerminalIcon />
              </Link>
            </Button>
          </>
        }
      />
      {action.error && (
        <Alert variant="destructive" className="mb-4">
          {messageOf(action.error)}
        </Alert>
      )}
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(open) => !open && setConfirm(null)}
        title={`${confirm ? confirm[0]?.toUpperCase() + confirm.slice(1) : "Change"} ${app.slug}?`}
        description={
          confirm === "unpublish"
            ? "Its public route will be removed."
            : confirm === "stop"
              ? "The app will stop serving."
              : "The running instance will be replaced."
        }
        confirmLabel={confirm ? confirm[0]?.toUpperCase() + confirm.slice(1) : "Confirm"}
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
    <div className="box mb-5">
      <div className="cell flex flex-wrap items-center gap-3 py-3!">
        <StateBadge state={op?.state ?? "queued"} label={op?.kind.replace("app.", "")} />
        <span className="note min-w-0 flex-1 truncate">{op?.events?.at(-1)?.message ?? op?.phase ?? "Queued"}</span>
        {op && (
          <Button asChild size="xs" variant="ghost">
            <Link href={`/activity/${op.id}`}>Details</Link>
          </Button>
        )}
      </div>
    </div>
  );
}

function Overview({ app }: { app: T.App }) {
  return (
    <>
      <div className="box box--4">
        <Cell>
          <div className="metric">
            <strong>
              {app.resources.memoryMb}
              <small> MB</small>
            </strong>
            <span>Memory</span>
          </div>
        </Cell>
        <Cell>
          <div className="metric">
            <strong>{app.resources.cpuMillis / 1000}</strong>
            <span>CPU cores</span>
          </div>
        </Cell>
        <Cell>
          <div className="metric">
            <strong>{app.resources.pids}</strong>
            <span>Processes</span>
          </div>
        </Cell>
        <Cell>
          <div className="metric">
            <strong>{app.bindings.length}</strong>
            <span>Data bindings</span>
          </div>
        </Cell>
      </div>
      <div className="box box--2">
        <Cell title="Runtime">
          <KeyValues
            items={[
              ["Container", app.observed.containerId ? <CopyableCode value={app.observed.containerId} /> : "—"],
              ["Started", <span title={app.observed.startedAt}>{formatRelative(app.observed.startedAt)}</span>],
              ["Generation", `${app.configGeneration}${app.observed.generationCurrent ? "" : " · pending"}`],
              [
                "Command",
                app.runtime.http ? (
                  <code>{app.runtime.http.argv.join(" ")}</code>
                ) : (
                  `PHP ${app.runtime.php?.version} · ${app.runtime.php?.pool}`
                ),
              ],
            ]}
          />
        </Cell>
        <Cell title="Routing">
          <KeyValues
            items={[
              ["Ingress", `${app.ingressInfo.mode} · ${app.publication}`],
              ["Internal", <CopyableCode value={app.ingressInfo.internalUrl} />],
              [
                "Domains",
                app.domains.length ? (
                  <span className="chips">
                    {app.domains.map((domain) => (
                      <span key={domain.name} className="chip">
                        {domain.name}
                        {domain.primary && <b className="text-primary">•</b>}
                      </span>
                    ))}
                  </span>
                ) : (
                  "—"
                ),
              ],
              ["TLS", `${app.route.tls}${app.route.redirectHttps ? " · HTTPS redirect" : ""}`],
            ]}
          />
        </Cell>
        <Cell title="Redis" className={app.reconcile.failures > 0 ? "" : "cell--wide"}>
          <KeyValues
            items={[
              ["User", <code>{app.redisUser}</code>],
              ["Prefix", <code>{app.redisPrefix}*</code>],
            ]}
          />
        </Cell>
        {app.reconcile.failures > 0 && (
          <Cell title="Reconcile" className="cell--alert">
            <p className="mb-2 text-sm">
              {app.reconcile.failures} failure(s){app.reconcile.blocked && " · blocked until start or restart"}
            </p>
            <p className="note note--bad">{app.reconcile.lastError}</p>
          </Cell>
        )}
      </div>
    </>
  );
}

function DataBindings({ app }: { app: T.App }) {
  const [engine, setEngine] = useState("sqlite");
  const addBinding = useOperationMutation(() => {
    const [value, service] = engine.split(":");
    return api.apps.addBinding(app.id, { engine: value as T.Engine, service });
  });
  return (
    <div className="box box--2">
      {app.bindings.map((binding) => (
        <BindingCard key={binding.id} appId={app.id} binding={binding} />
      ))}
      <Cell title="Add binding" className={`cell--muted ${app.bindings.length % 2 === 0 ? "cell--wide" : ""}`}>
        <div className="flex flex-wrap items-end gap-2">
          <div className="min-w-48 flex-1">
            <NativeSelect
              className="w-full"
              value={engine}
              onChange={(event) => setEngine(event.target.value)}
              aria-label="Data service"
            >
              <option value="sqlite">SQLite file</option>
              <ServiceOptions />
            </NativeSelect>
          </div>
          <Button disabled={addBinding.isPending} onClick={() => addBinding.mutate(undefined)}>
            <Plus /> Add
          </Button>
        </div>
        <p className="note mt-3">Add-only. Data is kept if the app is removed.</p>
        {addBinding.error && <p className="note note--bad mt-2">{messageOf(addBinding.error)}</p>}
      </Cell>
    </div>
  );
}

function BindingCard({ appId, binding }: { appId: string; binding: T.Binding }) {
  const [name, setName] = useState("");
  const add = useOperationMutation(() => api.apps.addDatabase(appId, binding.id, name));
  const sqlite = binding.engine === "sqlite";
  return (
    <section className="cell grid content-start gap-4" aria-label={`${binding.engine} binding`}>
      <div className="tile__top">
        <span className="mono">{sqlite ? <HardDrive className="size-4" /> : <Database className="size-4" />}</span>
        <div className="tile__name">
          <strong>{sqlite ? "SQLite" : binding.engine === "postgres" ? "PostgreSQL" : "MySQL"}</strong>
          <small>{binding.service || "Private file"}</small>
        </div>
      </div>
      {sqlite ? (
        <KeyValues items={[["Path", binding.sqlitePath ? <CopyableCode value={binding.sqlitePath} /> : "—"]]} />
      ) : (
        <>
          <KeyValues items={[["User", <code>{binding.username || "—"}</code>]]} />
          <div className="chips">
            {binding.databases.length ? (
              binding.databases.map((database) => (
                <span key={database} className="chip">
                  <Database className="size-3" aria-hidden="true" />
                  {database}
                </span>
              ))
            ) : (
              <span className="note">No databases</span>
            )}
          </div>
          <div className="flex gap-2">
            <Input
              placeholder="New database"
              aria-label="New database name"
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
            <Button
              variant="outline"
              disabled={!name.trim() || add.isPending}
              onClick={() => add.mutate(undefined, { onSuccess: () => setName("") })}
            >
              <Plus /> Add
            </Button>
          </div>
        </>
      )}
      {add.error && <p className="note note--bad">{messageOf(add.error)}</p>}
    </section>
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
            {service.engine} {service.version} · {service.name}
          </option>
        ))}
    </>
  );
}

function Scheduler({ app }: { app: T.App }) {
  if (app.desiredRuntime !== "running")
    return (
      <div className="box">
        <div className="cell empty">
          <strong>App is stopped</strong>
          <p>Start it to manage jobs.</p>
        </div>
      </div>
    );
  return (
    <div className="box">
      <div className="cell cell--flush">
        <div className="flex justify-end border-b px-3 py-2">
          <a className="note inline-flex items-center gap-1" href={app.schedulerPath} target="_blank" rel="noreferrer">
            Open <ExternalLink className="size-3" />
          </a>
        </div>
        <iframe
          title={`${app.slug} scheduler`}
          src={app.schedulerPath}
          className="block h-[calc(100vh-16rem)] min-h-96 w-full"
        />
      </div>
    </div>
  );
}

function Settings({ app, onRemoved }: { app: T.App; onRemoved: () => void }) {
  const [mode, setMode] = useState("check");
  const [removeOpen, setRemoveOpen] = useState(false);
  const permissions = useOperationMutation(() => api.apps.permissions(app.id, mode));
  const remove = useOperationMutation((confirm: string) => api.apps.remove(app.id, confirm));
  return (
    <>
      <ApplicationEditor app={app} />
      <div className="box box--2">
        <Cell title="Permissions">
          <div className="flex flex-wrap items-end gap-2">
            <div className="min-w-40 flex-1">
              <Field label="Mode">
                <NativeSelect className="w-full" value={mode} onChange={(event) => setMode(event.target.value)}>
                  <option value="check">Check</option>
                  <option value="dry-run">Dry run</option>
                  <option value="shallow">Shallow repair</option>
                  <option value="recursive">Recursive repair</option>
                </NativeSelect>
              </Field>
            </div>
            <Button variant="outline" disabled={permissions.isPending} onClick={() => permissions.mutate(undefined)}>
              Run
            </Button>
          </div>
          {permissions.error && <p className="note note--bad mt-2">{messageOf(permissions.error)}</p>}
        </Cell>
        <Cell title="Remove" className="cell--alert">
          <p className="note mb-3">
            Removes containers and routes, retires uid {app.uid}. Home and databases are kept until pruned.
          </p>
          <Button variant="destructive" onClick={() => setRemoveOpen(true)}>
            Remove {app.slug}
          </Button>
        </Cell>
      </div>
      <ConfirmDialog
        open={removeOpen}
        onOpenChange={setRemoveOpen}
        title={`Remove ${app.slug}?`}
        description="Runtime resources are removed. Durable data is retained for explicit pruning."
        phrase={`delete ${app.slug}`}
        destructive
        confirmLabel="Remove app"
        pending={remove.isPending}
        error={remove.error}
        onConfirm={(typed) => remove.mutate(typed, { onSuccess: onRemoved })}
      />
    </>
  );
}
