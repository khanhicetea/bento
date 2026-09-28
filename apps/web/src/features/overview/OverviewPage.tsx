import { useQuery } from "@tanstack/react-query";
import { ArrowRight, Plus } from "lucide-react";
import { Link } from "wouter";
import { api, messageOf } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { Cell, DomainError, DomainLoading, PageHeader, StateBadge } from "../../components/DomainState.tsx";
import { describeOp, formatRelative } from "../../lib/format.ts";
import { isTerminal } from "../operations/OperationTracker.tsx";
import { Button } from "@/components/ui/button";

function greeting() {
  const hour = new Date().getHours();
  return hour < 12 ? "Good morning" : hour < 18 ? "Good afternoon" : "Good evening";
}

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

  if (system.isPending || apps.isPending) return <DomainLoading label="overview" />;
  if (system.error || apps.error)
    return (
      <DomainError
        message={messageOf(system.error ?? apps.error)}
        onRetry={() => {
          void system.refetch();
          void apps.refetch();
        }}
      />
    );

  const allApps = apps.data?.apps ?? [];
  const allOps = operations.data?.operations ?? [];
  const running = allApps.filter((app) => app.observed.state === "healthy" || app.observed.state === "starting").length;
  const activeOps = allOps.filter((op) => !isTerminal(op.state)).length;
  const recent = allOps.slice(0, 8);

  const serviceState = (
    query: { isPending: boolean; error: unknown },
    enabled: boolean | undefined,
    state: string | undefined,
  ): [string, string | undefined] =>
    query.isPending
      ? ["queued", "Checking"]
      : query.error
        ? ["failed", "Unavailable"]
        : enabled
          ? [state || "queued", state ? undefined : "On"]
          : ["absent", "Off"];

  const stackServices: Array<[string, string, [string, string | undefined]]> = [
    ["Docker", "/system", system.data?.dockerError ? ["failed", "Down"] : ["healthy", "Connected"]],
    ["Edge", "/ingress", serviceState(edge, edge.data?.settings.enabled, edge.data?.state)],
    ["Tunnel", "/ingress", serviceState(tunnel, tunnel.data?.enabled, tunnel.data?.state)],
    ["Backups", "/backups", serviceState(schedule, schedule.data?.enabled, schedule.data?.lastState)],
  ];

  return (
    <>
      <PageHeader
        title={greeting()}
        description={system.data?.stackName}
        actions={
          <Button asChild>
            <Link href="/apps/new">
              <Plus /> New app
            </Link>
          </Button>
        }
      />

      <div className="box box--3">
        <MetricLink href="/apps" value={running} total={allApps.length} label="Apps running" />
        <MetricLink
          href="/system"
          value={services.isPending ? "–" : (services.data?.services.length ?? 0)}
          label="Data services"
        />
        <MetricLink href="/activity" value={operations.isPending ? "–" : activeOps} label="Operations in progress" />
      </div>

      <div className="box box--main">
        <Cell
          title="Recent activity"
          action={
            <Link href="/activity">
              All <ArrowRight className="size-3.5" />
            </Link>
          }
        >
          {operations.isPending ? (
            <DomainLoading label="activity" />
          ) : operations.error ? (
            <DomainError message={messageOf(operations.error)} onRetry={() => void operations.refetch()} />
          ) : recent.length === 0 ? (
            <p className="note">Nothing yet</p>
          ) : (
            <div className="rows">
              {recent.map((op) => (
                <Link key={op.id} href={`/activity/${op.id}`} className="row">
                  <span className={`dot ${isTerminal(op.state) ? "" : "dot--wait"}`} />
                  <span className="row__main">
                    <strong>{describeOp(op)}</strong>
                  </span>
                  <span className="row__meta">{formatRelative(op.createdAt)}</span>
                </Link>
              ))}
            </div>
          )}
        </Cell>
        <Cell title="Stack">
          <div className="rows">
            {stackServices.map(([name, href, [state, label]]) => (
              <Link key={name} href={href} className="row">
                <span className="row__main">
                  <strong>{name}</strong>
                </span>
                <StateBadge state={state} label={label} />
              </Link>
            ))}
          </div>
        </Cell>
      </div>
    </>
  );
}

function MetricLink({
  href,
  value,
  total,
  label,
}: {
  href: string;
  value: number | string;
  total?: number;
  label: string;
}) {
  return (
    <Link href={href} className="cell cell--link">
      <div className="metric">
        <strong>
          {value}
          {total !== undefined && <small> / {total}</small>}
        </strong>
        <span>{label}</span>
      </div>
    </Link>
  );
}
