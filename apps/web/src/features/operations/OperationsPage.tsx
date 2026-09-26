import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import {
  Activity,
  AlertCircle,
  CheckCircle2,
  CircleAlert,
  Database,
  Gauge,
  Layers3,
  RefreshCw,
  Server,
  ServerCog,
} from "lucide-react";
import {
  DomainError,
  DomainLoading,
  EmptyPanel,
  StackNotReady,
} from "../../components/DomainState.tsx";
import { orpc } from "../../api/client.ts";
import {
  OperationsControls,
  ServiceLogsButton,
  ServiceRestartButton,
} from "./OperationsControls.tsx";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { CloudflareTunnelCard } from "./CloudflareTunnelCard.tsx";

export function OperationsPage() {
  const query = useQuery(orpc.operations.overview.queryOptions({ input: {} }));
  const data = query.data;

  if (!data && query.isPending) {
    return (
      <section className="mx-auto w-full max-w-[1800px] p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
        <DomainLoading label="operations" />
      </section>
    );
  }
  if (!data && query.error) {
    return (
      <section className="mx-auto w-full max-w-[1800px] p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
        <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
      </section>
    );
  }
  if (!data) return null;

  const runningRoles = data.roles.filter((role) => role.state === "running").length;
  const health = getHealth(runningRoles, data.roles.length);

  return (
    <section
      className="mx-auto w-full max-w-[1800px] p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4"
      aria-live="polite"
    >
      <div className="flex items-end justify-between gap-6 max-[760px]:items-stretch max-[760px]:flex-col">
        <div>
          <p className="mb-2 text-[0.68rem] font-semibold uppercase tracking-[0.16em] text-muted-foreground">
            Control plane / Runtime
          </p>
          <h2 className="m-0 text-[clamp(1.7rem,3vw,2.35rem)] tracking-tight">Operations</h2>
          <p className="m-0 mt-2 max-w-[680px] text-sm text-muted-foreground">
            Monitor stack health, capacity, and the actions that keep your services running.
          </p>
        </div>
        <Button
          className="max-[760px]:w-full"
          variant="outline"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {query.isFetching ? <Spinner /> : <RefreshCw className="size-4" aria-hidden="true" />}
          {query.isFetching ? "Refreshing…" : "Refresh status"}
        </Button>
      </div>

      {query.error && (
        <Alert className="mt-6" variant="destructive">
          <CircleAlert aria-hidden="true" />
          <div>
            <strong className="font-medium">Status may be out of date.</strong>{" "}
            {messageOf(query.error)}
          </div>
        </Alert>
      )}

      {!data.initialized ? (
        <div className="mt-8">
          <StackNotReady stackRoot={data.stackRoot} error={data.error} />
        </div>
      ) : (
        <>
          <article className="mt-8 overflow-hidden rounded-2xl border border-border bg-card text-card-foreground shadow-sm">
            <div className="grid gap-8 bg-gradient-to-br from-muted/60 via-card to-card p-[clamp(1.25rem,3vw,2rem)] lg:grid-cols-[minmax(0,1.2fr)_minmax(18rem,0.8fr)]">
              <div>
                <Badge className={health.badgeClass}>
                  <span className={`size-1.5 rounded-full ${health.dotClass}`} aria-hidden="true" />
                  {health.label}
                </Badge>
                <h3 className="mb-0 mt-4 text-2xl font-semibold tracking-tight">
                  {data.stackName ?? "Bento stack"}
                </h3>
                <p className="mb-0 mt-2 max-w-[620px] text-sm text-muted-foreground">
                  {health.description}
                </p>
                <div className="mt-6 flex flex-wrap gap-2 text-xs text-muted-foreground">
                  <span className="inline-flex items-center gap-1.5 rounded-full bg-muted px-3 py-1.5">
                    <Server className="size-3.5" aria-hidden="true" />
                    {runningRoles} of {data.roles.length} roles running
                  </span>
                  <span className="inline-flex items-center gap-1.5 rounded-full bg-muted px-3 py-1.5">
                    <Activity className="size-3.5" aria-hidden="true" />
                    Checked {formatDate(query.dataUpdatedAt)}
                  </span>
                </div>
              </div>
              <div className="grid content-start gap-3 rounded-xl border border-border bg-background/70 p-4">
                <Detail label="Stack root" value={data.stackRoot} />
                <Detail
                  label="Last render"
                  value={
                    data.generation?.renderedAt
                      ? formatDate(data.generation.renderedAt)
                      : "Not rendered"
                  }
                />
                <Detail label="Asset version" value={data.generation?.assetVersion ?? "Unknown"} />
              </div>
            </div>
          </article>

          <div className="mt-5 grid grid-cols-3 gap-3 max-[1050px]:grid-cols-2 max-[560px]:grid-cols-1">
            <Summary
              value={runningRoles}
              label="Running roles"
              icon={<CheckCircle2 className="size-4" />}
              tone={
                runningRoles > 0 && runningRoles === data.roles.length
                  ? "success"
                  : runningRoles < data.roles.length
                    ? "warning"
                    : "default"
              }
            />
            <Summary
              value={data.roles.length}
              label="Expected roles"
              icon={<ServerCog className="size-4" />}
            />
            <Summary
              value={data.counts.applications}
              label="Applications"
              icon={<Layers3 className="size-4" />}
            />
          </div>

          {(data.warnings.length > 0 || data.notes.length > 0) && (
            <section className="mt-8 space-y-3" aria-labelledby="operations-notices">
              <div className="flex items-center gap-2">
                <CircleAlert
                  className="size-4 text-amber-600 dark:text-amber-400"
                  aria-hidden="true"
                />
                <h3 id="operations-notices" className="m-0 text-base font-semibold">
                  Runtime notices
                </h3>
              </div>
              {data.warnings.map((warning, index) => (
                <Alert
                  className="border-amber-500/40 bg-amber-500/10 text-amber-950 dark:text-amber-200"
                  key={`${warning}-${index}`}
                >
                  <AlertCircle aria-hidden="true" />
                  {warning}
                </Alert>
              ))}
              {data.notes.map((note, index) => (
                <Alert key={`${note}-${index}`}>
                  <Activity aria-hidden="true" />
                  {note}
                </Alert>
              ))}
            </section>
          )}

          <div className="mt-8">
            <OperationsControls stackName={data.stackName ?? "bento"} />
          </div>

          <CloudflareTunnelCard configured={data.cloudflareTunnel.configured} />

          <SectionHeading
            eyebrow="Service health"
            title="Service roles"
            description="The services that make up this stack and their latest observed state."
            count={data.roles.length}
            itemLabel="role"
            icon={<ServerCog className="size-4" />}
          />
          {data.roles.length ? (
            <div className="mt-4 grid grid-cols-3 items-stretch gap-5 max-[1100px]:grid-cols-2 max-[760px]:grid-cols-1">
              {data.roles.map((role) => (
                <RoleCard key={role.name} role={role} />
              ))}
            </div>
          ) : (
            <article className="mt-4 rounded-2xl border border-dashed border-border bg-card shadow-sm">
              <EmptyPanel>No service roles found.</EmptyPanel>
            </article>
          )}

          <SectionHeading
            eyebrow="Capacity"
            title="PHP runtime capacity"
            description="Compare configured pool capacity with the process cap for each PHP runtime."
            count={data.runtimes.length}
            itemLabel="runtime"
            icon={<Gauge className="size-4" />}
          />
          {data.runtimes.length ? (
            <div className="mt-4 grid grid-cols-3 items-stretch gap-5 max-[1100px]:grid-cols-2 max-[760px]:grid-cols-1">
              {data.runtimes.map((runtime) => (
                <RuntimeCard key={runtime.version} runtime={runtime} />
              ))}
            </div>
          ) : (
            <article className="mt-4 rounded-2xl border border-dashed border-border bg-card shadow-sm">
              <EmptyPanel>No PHP runtimes configured.</EmptyPanel>
            </article>
          )}
        </>
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
  tone?: "default" | "success" | "warning";
}) {
  return (
    <div className="flex items-center gap-3 rounded-xl border border-border bg-card px-4 py-3 text-card-foreground shadow-sm">
      <span
        className={`grid size-9 shrink-0 place-items-center rounded-lg ${tone === "success" ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400" : tone === "warning" ? "bg-amber-500/10 text-amber-700 dark:text-amber-300" : "bg-muted text-muted-foreground"}`}
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

function SectionHeading({
  eyebrow,
  title,
  description,
  count,
  itemLabel,
  icon,
}: {
  eyebrow: string;
  title: string;
  description: string;
  count: number;
  itemLabel: string;
  icon: ReactNode;
}) {
  return (
    <div className="mt-10 flex items-end justify-between gap-4 max-[760px]:items-start">
      <div className="flex min-w-0 items-start gap-3">
        <span className="mt-1 grid size-9 shrink-0 place-items-center rounded-xl bg-muted text-muted-foreground">
          {icon}
        </span>
        <div className="min-w-0">
          <p className="m-0 text-[0.68rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
            {eyebrow}
          </p>
          <h3 className="m-0 mt-1 text-lg font-semibold tracking-tight">{title}</h3>
          <p className="m-0 mt-1 text-sm text-muted-foreground">{description}</p>
        </div>
      </div>
      <Badge variant="secondary" className="shrink-0">
        {count} {count === 1 ? itemLabel : `${itemLabel}s`}
      </Badge>
    </div>
  );
}

function RoleCard({ role }: { role: OperationsRole }) {
  const status = roleStatus(role.state);
  return (
    <article className="flex min-w-0 flex-col rounded-2xl border border-border bg-card p-5 text-card-foreground shadow-sm">
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-3">
          <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-muted text-primary">
            {roleIcon(role.kind)}
          </span>
          <div className="min-w-0">
            <h4 className="m-0 truncate text-sm font-semibold" title={role.name}>
              {role.name}
            </h4>
            <span className="mt-1 block text-xs text-muted-foreground">
              {formatLabel(role.kind)}
            </span>
          </div>
        </div>
        <div className="flex flex-col items-end gap-1">
          <Badge className={status.badgeClass} variant={status.variant}>
            <span className={`size-1.5 rounded-full ${status.dotClass}`} aria-hidden="true" />
            {formatLabel(role.state)}
          </Badge>
          <span className="text-[0.68rem] text-muted-foreground">
            {role.state === "running"
              ? role.uptimeSeconds !== undefined
                ? `${formatUptime(role.uptimeSeconds)} uptime`
                : "Uptime unavailable"
              : "Not running"}
          </span>
        </div>
      </div>
      <p className="mt-4 min-h-10 flex-1 text-sm text-muted-foreground">
        {role.detail ?? "No additional status detail reported."}
      </p>
      <div className="mt-4 flex items-center justify-between gap-3 border-t border-border pt-4">
        <span className="text-xs text-muted-foreground">Service action</span>
        <div className="flex items-center gap-2">
          <ServiceLogsButton service={role.name} />
          <ServiceRestartButton service={role.name} />
        </div>
      </div>
    </article>
  );
}

type Runtime = {
  version: string;
  service: string;
  runner: string;
  processCap: number;
  appCount: number;
  poolMaxSum: number;
  overCap: boolean;
};

type OperationsRole = {
  name: string;
  kind:
    | "nginx"
    | "redis"
    | "php-fpm"
    | "php-runner"
    | "process-app"
    | "mysql"
    | "postgres"
    | "litestream"
    | "cloudflare-tunnel";
  state: "running" | "stopped" | "unknown" | "config-ready";
  uptimeSeconds?: number;
  detail?: string;
};

function RuntimeCard({ runtime }: { runtime: Runtime }) {
  const ratio =
    runtime.processCap > 0
      ? Math.min((runtime.poolMaxSum / runtime.processCap) * 100, 100)
      : runtime.poolMaxSum > 0
        ? 100
        : 0;
  const status = runtime.overCap
    ? {
        label: "Over cap",
        className: "bg-amber-500 text-amber-950",
        barClassName: "bg-amber-500",
      }
    : {
        label: "Within cap",
        className: "bg-emerald-600 text-white",
        barClassName: "bg-emerald-500",
      };

  return (
    <article className="rounded-2xl border border-border bg-card p-5 text-card-foreground shadow-sm">
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-center gap-3">
          <span className="grid size-10 place-items-center rounded-xl bg-muted text-primary">
            <Gauge className="size-4" aria-hidden="true" />
          </span>
          <div>
            <h4 className="m-0 text-sm font-semibold">PHP {runtime.version}</h4>
            <p className="m-0 mt-1 text-xs text-muted-foreground">
              {runtime.appCount} applications
            </p>
          </div>
        </div>
        <Badge className={status.className}>
          {runtime.overCap && <AlertCircle aria-hidden="true" />}
          {status.label}
        </Badge>
      </div>
      <div className="mt-6">
        <div className="flex items-center justify-between gap-3 text-xs">
          <span className="text-muted-foreground">Pool allocation</span>
          <strong>
            {runtime.poolMaxSum} / {runtime.processCap}
          </strong>
        </div>
        <div
          className="mt-2 h-2 overflow-hidden rounded-full bg-muted"
          role="progressbar"
          aria-label={`PHP ${runtime.version} pool allocation`}
          aria-valuemin={0}
          aria-valuemax={runtime.processCap > 0 ? runtime.processCap : undefined}
          aria-valuenow={
            runtime.processCap > 0
              ? Math.min(runtime.poolMaxSum, runtime.processCap)
              : runtime.poolMaxSum
          }
        >
          <div
            className={`h-full rounded-full transition-all ${status.barClassName}`}
            style={{ width: `${ratio}%` }}
          />
        </div>
      </div>
      <div className="mt-5 grid grid-cols-2 divide-x divide-border overflow-hidden rounded-xl border border-border bg-muted/30">
        <Fact label="FPM service" value={runtime.service} />
        <Fact label="Runner" value={runtime.runner} />
      </div>
    </article>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 p-3">
      <span className="block text-[0.68rem] text-muted-foreground">{label}</span>
      <strong className="mt-1 block truncate text-sm font-medium" title={value}>
        {value}
      </strong>
    </div>
  );
}

function Detail({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 border-b border-border py-2.5 text-[0.84rem] last:border-b-0">
      <span className="text-muted-foreground">{label}</span>
      <strong className="[overflow-wrap:anywhere] text-right">{value}</strong>
    </div>
  );
}

function getHealth(running: number, total: number) {
  if (total === 0) {
    return {
      label: "No roles reported",
      description: "The stack is initialized, but no service roles have been reported yet.",
      badgeClass: "bg-muted text-muted-foreground",
      dotClass: "bg-muted-foreground",
    };
  }
  if (running === total) {
    return {
      label: "All services healthy",
      description: "Every expected service role is currently reporting as running.",
      badgeClass: "bg-emerald-600 text-white",
      dotClass: "bg-emerald-200",
    };
  }
  return {
    label: running === 0 ? "Services need attention" : "Partial service health",
    description: `${total - running} of ${total} expected service roles are not reporting as running.`,
    badgeClass: "bg-amber-500 text-amber-950",
    dotClass: "bg-amber-900/70",
  };
}

function roleStatus(state: OperationsRole["state"]) {
  if (state === "running") {
    return {
      variant: "default" as const,
      badgeClass: "bg-emerald-600 text-white",
      dotClass: "bg-emerald-200",
    };
  }
  if (state === "stopped") {
    return {
      variant: "default" as const,
      badgeClass: "bg-amber-500 text-amber-950",
      dotClass: "bg-amber-900/70",
    };
  }
  return {
    variant: "outline" as const,
    badgeClass: "",
    dotClass: "bg-muted-foreground",
  };
}

function roleIcon(kind: OperationsRole["kind"]) {
  if (kind === "php-fpm" || kind === "php-runner")
    return <Gauge className="size-4" aria-hidden="true" />;
  if (kind === "mysql" || kind === "postgres" || kind === "litestream") {
    return <Database className="size-4" aria-hidden="true" />;
  }
  return <Server className="size-4" aria-hidden="true" />;
}

function formatLabel(value: string) {
  return value
    .split("-")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function formatUptime(seconds: number) {
  const days = Math.floor(seconds / 86_400);
  const hours = Math.floor((seconds % 86_400) / 3_600);
  const minutes = Math.floor((seconds % 3_600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}

function formatDate(value: string | number) {
  return new Date(value).toLocaleString();
}

function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
