import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, AppWindow, Database, History } from "lucide-react";
import { Link } from "wouter";
import { api, messageOf } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import {
  DomainError,
  DomainLoading,
  EmptyState,
  Page,
  PageHeader,
  Panel,
  StateBadge,
} from "../../components/DomainState.tsx";
import { describeOp, formatRelative } from "../../lib/format.ts";

export function OverviewPage() {
  const system = useQuery({ queryKey: keys.system, queryFn: ({ signal }) => api.system.status(signal) });
  const apps = useQuery({ queryKey: keys.apps.list(), queryFn: ({ signal }) => api.apps.list(signal) });
  const services = useQuery({ queryKey: keys.services, queryFn: ({ signal }) => api.services.list(signal) });
  const operations = useQuery({
    queryKey: keys.operations.list(),
    queryFn: ({ signal }) => api.operations.list(undefined, signal),
    refetchInterval: 5_000,
  });
  const edge = useQuery({ queryKey: keys.edge, queryFn: ({ signal }) => api.edge.get(signal) });
  const tunnel = useQuery({ queryKey: keys.tunnel, queryFn: ({ signal }) => api.tunnel.get(signal) });
  const schedule = useQuery({ queryKey: keys.backups.schedule, queryFn: ({ signal }) => api.backups.schedule(signal) });
  if (system.isPending || apps.isPending)
    return (
      <Page>
        <DomainLoading label="overview" />
      </Page>
    );
  if (system.error || apps.error)
    return (
      <Page>
        <DomainError message={messageOf(system.error ?? apps.error)} />
      </Page>
    );
  const attention = (apps.data?.apps ?? []).filter(
    (app) =>
      app.observed.state === "failed" ||
      app.observed.state === "unhealthy" ||
      app.observed.state === "blocked" ||
      (app.desiredRuntime === "running" && app.observed.state === "stopped"),
  );
  const recentFailed = (operations.data?.operations ?? []).filter(
    (op) => op.state === "failed" && Date.now() - Date.parse(op.createdAt) < 86_400_000,
  );
  const initializing = (services.data?.services ?? []).filter(
    (service) => !service.initialized || service.state === "starting",
  );
  return (
    <Page>
      <PageHeader title="Overview" description={`Stack ${system.data?.stackName ?? ""} operator summary.`} />
      <div className="mb-6 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Stat
          label="Docker"
          value={system.data?.dockerError ? "Unavailable" : system.data?.dockerVersion || "Available"}
          state={system.data?.dockerError ? "failed" : "healthy"}
        />
        <Stat
          label="Managed edge"
          value={edge.data?.settings.enabled ? edge.data.state : "Disabled"}
          state={edge.data?.settings.enabled ? edge.data.state : "absent"}
        />
        <Stat
          label="Tunnel"
          value={tunnel.data?.enabled ? tunnel.data.state : "Disabled"}
          state={tunnel.data?.enabled ? tunnel.data.state : "absent"}
        />
        <Stat
          label="Backup schedule"
          value={schedule.data?.enabled ? schedule.data.lastState || "Enabled" : "Disabled"}
          state={schedule.data?.enabled ? schedule.data.lastState || "queued" : "absent"}
        />
      </div>
      <div className="grid gap-6 lg:grid-cols-2">
        <Panel title="Needs attention" actions={<AlertTriangle className="size-4 text-warning" />}>
          {attention.length + recentFailed.length + initializing.length === 0 ? (
            <EmptyState
              title="Nothing needs attention"
              body="No drift, recent failures, or initializing services were found."
            />
          ) : (
            <div className="grid gap-2">
              {attention.map((app) => (
                <Link
                  key={app.id}
                  href={`/apps/${encodeURIComponent(app.slug)}`}
                  className="rounded-md border p-3 text-sm no-underline"
                >
                  <strong>{app.slug}</strong> · {app.observed.message || app.observed.state}
                </Link>
              ))}
              {recentFailed.map((op) => (
                <Link key={op.id} href={`/activity/${op.id}`} className="rounded-md border p-3 text-sm no-underline">
                  <strong>{describeOp(op)}</strong> · {op.errorMessage || "failed"}
                </Link>
              ))}
              {initializing.map((service) => (
                <Link key={service.name} href="/data" className="rounded-md border p-3 text-sm no-underline">
                  <strong>{service.name}</strong> · initializing
                </Link>
              ))}
            </div>
          )}
        </Panel>
        <Panel title="Recent activity" actions={<History className="size-4" />}>
          {(operations.data?.operations ?? []).slice(0, 10).length === 0 ? (
            <EmptyState title="No activity yet" />
          ) : (
            <div className="grid">
              {(operations.data?.operations ?? []).slice(0, 10).map((op) => (
                <Link
                  key={op.id}
                  href={`/activity/${op.id}`}
                  className="flex items-center gap-2 border-t py-2 text-sm no-underline first:border-0"
                >
                  <StateBadge state={op.state} />
                  <span className="min-w-0 flex-1 truncate">{describeOp(op)}</span>
                  <span className="text-xs text-muted-foreground" title={op.createdAt}>
                    {formatRelative(op.createdAt)}
                  </span>
                </Link>
              ))}
            </div>
          )}
        </Panel>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <Link href="/apps" className="flex items-center gap-3 rounded-xl border bg-card p-4 text-inherit no-underline">
          <AppWindow />
          <div>
            <strong>
              {system.data?.runningApps ?? 0} / {system.data?.apps ?? 0}
            </strong>
            <div className="text-xs text-muted-foreground">applications desired running</div>
          </div>
        </Link>
        <Link href="/data" className="flex items-center gap-3 rounded-xl border bg-card p-4 text-inherit no-underline">
          <Database />
          <div>
            <strong>{services.data?.services.length ?? 0}</strong>
            <div className="text-xs text-muted-foreground">data services</div>
          </div>
        </Link>
      </div>
    </Page>
  );
}
function Stat({ label, value, state }: { label: string; value: string; state: string }) {
  return (
    <div className="rounded-xl border bg-card p-4">
      <div className="mb-2 text-xs text-muted-foreground">{label}</div>
      <div className="flex items-center justify-between gap-2 font-medium">
        <span className="truncate">{value}</span>
        <StateBadge state={state} />
      </div>
    </div>
  );
}
