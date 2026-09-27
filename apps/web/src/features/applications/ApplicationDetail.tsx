import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { DomainError, DomainLoading, Field, StateBadge } from "../../components/DomainState.tsx";
import { TerminalDialog } from "../../components/TerminalDialog.tsx";
import { ApplicationEditor } from "./ApplicationEditor.tsx";
import { LogsPanel } from "./LogsPanel.tsx";
import { useApplication, useOperationMutation } from "./useApplications.ts";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";

type Tab = "overview" | "data" | "logs" | "scheduler" | "danger";

export function ApplicationDetail({ id, onClose }: { id: string; onClose: () => void }) {
  const query = useApplication(id);
  const [tab, setTab] = useState<Tab>("overview");
  const [editing, setEditing] = useState(false);
  const [terminal, setTerminal] = useState<"tool" | "running" | null>(null);
  const app = query.data;
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[92vh] overflow-y-auto sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle>{app?.slug ?? "Application"}</DialogTitle>
          <DialogDescription>
            {app ? `${app.toolchain} ${app.version} · uid ${app.uid} · ${app.home}` : "Loading…"}
          </DialogDescription>
        </DialogHeader>
        {query.isPending && <DomainLoading label="application" />}
        {query.error && <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />}
        {app && (
          <>
            <div className="flex flex-wrap gap-1 border-b border-border pb-2">
              {(["overview", "data", "logs", "scheduler", "danger"] as const).map((t) => (
                <Button key={t} size="sm" variant={tab === t ? "default" : "ghost"} onClick={() => setTab(t)}>
                  {t[0]?.toUpperCase() + t.slice(1)}
                </Button>
              ))}
              <span className="ml-auto flex gap-1">
                <Button size="sm" variant="outline" onClick={() => setEditing(true)}>
                  Edit
                </Button>
                <Button size="sm" variant="outline" onClick={() => setTerminal("tool")}>
                  Tool shell
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={app.desiredRuntime !== "running"}
                  onClick={() => setTerminal("running")}
                >
                  Exec into instance
                </Button>
              </span>
            </div>
            {tab === "overview" && <Overview app={app} />}
            {tab === "data" && <DataBindings app={app} />}
            {tab === "logs" && <LogsPanel appId={app.id} />}
            {tab === "scheduler" && <Scheduler app={app} />}
            {tab === "danger" && <Danger app={app} onRemoved={onClose} />}
          </>
        )}
        {editing && app && <ApplicationEditor app={app} onClose={() => setEditing(false)} />}
        {terminal && app && (
          <TerminalDialog appId={app.id} title={app.slug} mode={terminal} onClose={() => setTerminal(null)} />
        )}
      </DialogContent>
    </Dialog>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[180px_1fr] gap-3 border-b border-border/60 py-2 text-sm max-[640px]:grid-cols-1">
      <span className="text-muted-foreground">{label}</span>
      <span className="min-w-0 break-words">{children}</span>
    </div>
  );
}

function Overview({ app }: { app: T.App }) {
  return (
    <div>
      <Row label="Desired runtime">{app.desiredRuntime}</Row>
      <Row label="Observed">
        <StateBadge state={app.observed.state} />{" "}
        {app.observed.generationCurrent ? "current generation" : "generation pending"}{" "}
        {app.observed.message && <span className="text-muted-foreground">— {app.observed.message}</span>}
      </Row>
      <Row label="Container">
        {app.observed.containerId || "—"} {app.observed.startedAt && `since ${app.observed.startedAt}`}
      </Row>
      <Row label="Ingress">
        <Badge variant="outline">{app.ingressInfo.mode}</Badge>{" "}
        {app.ingress === "managed" && <Badge variant="outline">{app.publication}</Badge>}
        <p className="m-0 mt-1 text-xs text-muted-foreground">{app.ingressInfo.note}</p>
      </Row>
      <Row label="Internal URL">
        <code>{app.ingressInfo.internalUrl}</code>
      </Row>
      <Row label="Domains">{app.domains.map((d) => `${d.name}${d.primary ? " (primary)" : ""}`).join(", ") || "—"}</Row>
      <Row label="TLS">
        {app.route.tls}
        {app.route.redirectHttps ? ", redirects to HTTPS" : ""}
      </Row>
      <Row label="Resources">
        {app.resources.memoryMb} MB · {app.resources.cpuMillis}m CPU · {app.resources.pids} processes
      </Row>
      <Row label="Runtime">
        {app.runtime.php &&
          `PHP ${app.runtime.php.version} · ${app.runtime.php.routing} · root ${app.runtime.php.documentRoot || "."} · pool ${app.runtime.php.pool}`}
        {app.runtime.http && <code>{JSON.stringify(app.runtime.http.argv)}</code>}
        {app.runtime.http && ` in ${app.runtime.http.workdir || "~"} on :${app.runtime.http.port}`}
      </Row>
      <Row label="Redis">
        user {app.redisUser}, keys under <code>{app.redisPrefix}*</code>
      </Row>
      {app.reconcile.failures > 0 && (
        <Row label="Reconciliation">
          {app.reconcile.failures} failure(s){app.reconcile.blocked ? " — blocked until an explicit start/restart" : ""}
          : {app.reconcile.lastError}
        </Row>
      )}
    </div>
  );
}

function DataBindings({ app }: { app: T.App }) {
  const [engine, setEngine] = useState("sqlite");
  const [dbName, setDbName] = useState("");
  const addBinding = useOperationMutation(() => {
    const [e, service] = engine.split(":");
    return api.apps.addBinding(app.id, { engine: e as T.Engine, service });
  });
  const addDatabase = useOperationMutation(({ binding }: { binding: string }) =>
    api.apps.addDatabase(app.id, binding, dbName),
  );
  return (
    <div className="grid gap-4">
      <p className="m-0 text-xs text-muted-foreground">
        Bindings are add-only. Credentials are exposed to the app as environment variables (DB_*, BENTO_DB_n_*) and are
        never shown here.
      </p>
      {app.bindings.map((b) => (
        <div key={b.id} className="rounded-lg border border-border p-3 text-sm">
          <div className="flex items-center gap-2">
            <Badge>{b.engine}</Badge> {b.service && <span>{b.service}</span>} <code className="text-xs">{b.id}</code>
          </div>
          {b.engine === "sqlite" ? (
            <div className="mt-1 text-xs">
              <code>{b.sqlitePath}</code>
            </div>
          ) : (
            <>
              <div className="mt-1 text-xs">
                user <code>{b.username}</code> · databases {b.databases.join(", ")}
              </div>
              <div className="mt-2 flex gap-2">
                <Input
                  className="h-8 max-w-48"
                  placeholder="suffix"
                  value={dbName}
                  onChange={(e) => setDbName(e.target.value)}
                />
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!dbName || addDatabase.isPending}
                  onClick={() => addDatabase.mutate({ binding: b.id })}
                >
                  Add database
                </Button>
              </div>
            </>
          )}
        </div>
      ))}
      <div className="flex items-end gap-2">
        <Field label="Add binding">
          <NativeSelect value={engine} onChange={(e) => setEngine(e.target.value)}>
            <option value="sqlite">SQLite</option>
            <ServiceOptions />
          </NativeSelect>
        </Field>
        <Button variant="outline" disabled={addBinding.isPending} onClick={() => addBinding.mutate(undefined)}>
          Add
        </Button>
      </div>
      {(addBinding.error || addDatabase.error) && (
        <Alert variant="destructive">{messageOf(addBinding.error ?? addDatabase.error)}</Alert>
      )}
    </div>
  );
}

function ServiceOptions() {
  const q = useQuery({ queryKey: keys.services, queryFn: ({ signal }) => api.services.list(signal) });
  return (
    <>
      {(q.data?.services ?? [])
        .filter((s) => s.engine === "mysql" || s.engine === "postgres")
        .map((s) => (
          <option key={s.name} value={`${s.engine}:${s.name}`}>
            {s.engine} {s.version} ({s.name})
          </option>
        ))}
    </>
  );
}

function Scheduler({ app }: { app: T.App }) {
  if (app.desiredRuntime !== "running") {
    return <Alert>The scheduler runs inside the app instance. Start the app to manage its jobs and workers.</Alert>;
  }
  return (
    <div className="grid gap-2">
      <p className="m-0 text-xs text-muted-foreground">
        Jobs and workers live in the app's own minicrond registry, reached through Bento's authenticated per-app relay.
        This same-origin view is for trusted scheduler content only.{" "}
        <a href={app.schedulerPath} target="_blank" rel="noreferrer">
          Open in a new tab
        </a>
      </p>
      <iframe
        title={`${app.slug} scheduler`}
        src={app.schedulerPath}
        className="h-[60vh] w-full rounded-lg border border-border"
      />
    </div>
  );
}

function Danger({ app, onRemoved }: { app: T.App; onRemoved: () => void }) {
  const [confirm, setConfirm] = useState("");
  const [mode, setMode] = useState("check");
  const remove = useOperationMutation(() => api.apps.remove(app.id, confirm));
  const permissions = useOperationMutation(() => api.apps.permissions(app.id, mode));
  const phrase = `delete ${app.slug}`;
  return (
    <div className="grid gap-6">
      <div className="grid gap-2">
        <h4 className="m-0">Permissions</h4>
        <p className="m-0 text-xs text-muted-foreground">Recursive repair never follows symbolic links.</p>
        <div className="flex gap-2">
          <NativeSelect value={mode} onChange={(e) => setMode(e.target.value)}>
            <option value="check">Check</option>
            <option value="dry-run">Dry run</option>
            <option value="shallow">Shallow repair</option>
            <option value="recursive">Recursive repair</option>
          </NativeSelect>
          <Button variant="outline" onClick={() => permissions.mutate(undefined)} disabled={permissions.isPending}>
            Run
          </Button>
        </div>
      </div>
      <div className="grid gap-2 rounded-lg border border-destructive/40 p-4">
        <h4 className="m-0 text-destructive">Remove application</h4>
        <p className="m-0 text-xs">
          Removes the route and all containers and retires uid {app.uid} (never reused). The home, SQLite files, and
          databases are retained until a separate prune.
        </p>
        <Field label={`Type "${phrase}" to confirm`}>
          <Input value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </Field>
        <Button
          variant="destructive"
          disabled={confirm !== phrase || remove.isPending}
          onClick={() => remove.mutate(undefined, { onSuccess: onRemoved })}
        >
          Remove {app.slug}
        </Button>
        {remove.error && <Alert variant="destructive">{messageOf(remove.error)}</Alert>}
      </div>
    </div>
  );
}
