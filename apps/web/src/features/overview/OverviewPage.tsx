import { useQuery } from "@tanstack/react-query";
import { Activity, ArrowRight, Package, Plus, Server, TriangleAlert } from "lucide-react";
import { Link } from "wouter";
import { api, messageOf } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { Cell, DomainError, DomainLoading, EmptyState, PageHeader, StateBadge } from "../../components/DomainState.tsx";
import { Mascot } from "../../components/Mascot.tsx";
import { describeOp, formatRelative } from "../../lib/format.ts";
import { isTerminal } from "../operations/OperationTracker.tsx";
import { Button } from "@/components/ui/button";

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
  const schedules = useQuery({
    queryKey: keys.backups.schedules,
    queryFn: ({ signal }) => api.backups.schedules(signal),
  });

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
  const enabledSchedules = (schedules.data?.schedules ?? []).filter((schedule) => schedule.enabled);
  // The state of the enabled schedule that ran most recently.
  const lastBackup = enabledSchedules
    .filter((schedule) => schedule.lastRun)
    .sort((a, b) => (b.lastRun ?? "").localeCompare(a.lastRun ?? ""))[0];

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
    ["Backups", "/backups", serviceState(schedules, enabledSchedules.length > 0, lastBackup?.lastState)],
  ];

  const attention = allApps.filter((app) => ["failed", "unhealthy", "blocked"].includes(app.observed.state));
  const failedOps = recent.filter((op) => op.state === "failed");
  const issues = attention.length + (attention.length === 0 ? failedOps.length : 0);

  return (
    <>
      <PageHeader
        title="Home"
        description={system.data?.stackName}
        actions={
          <Button asChild>
            <Link href="/apps/new">
              <Plus /> New app
            </Link>
          </Button>
        }
      />

      <section className="box box--home" aria-label="Status">
        <Cell
          kind={issues > 0 ? "ume" : "gohan"}
          className="home__attention"
          title="Attention"
          icon={<TriangleAlert />}
        >
          <div className="home__attention-head">
            <div className="metric">
              <strong>{issues}</strong>
              <span>{issues > 0 ? "Needs you" : "All clear"}</span>
            </div>
            <Mascot mood={issues > 0 ? "alert" : activeOps > 0 ? "busy" : "ok"} size={104} />
          </div>
          <div className="home__issues">
            {attention.slice(0, 3).map((app) => (
              <Link key={app.id} href={`/apps/${encodeURIComponent(app.slug)}`} className="home__issue">
                <span className="mono mono--bad" aria-hidden="true">
                  {app.slug.slice(0, 1).toUpperCase()}
                </span>
                <span className="row__main">
                  <strong>{app.slug}</strong>
                  <small>{app.observed.message || app.observed.state}</small>
                </span>
                <StateBadge state={app.observed.state} />
              </Link>
            ))}
            {attention.length === 0 &&
              failedOps.slice(0, 3).map((op) => (
                <Link key={op.id} href={`/activity/${op.id}`} className="home__issue">
                  <span className="row__main">
                    <strong>{describeOp(op)}</strong>
                    <small>{formatRelative(op.createdAt)}</small>
                  </span>
                  <StateBadge state={op.state} />
                </Link>
              ))}
          </div>
        </Cell>
        <MetricLink href="/apps" value={running} total={allApps.length} label="Apps running" />
        <MetricLink
          href="/activity"
          value={operations.isPending ? "–" : activeOps}
          label="In progress"
          caution={activeOps > 0}
        />
        <MetricLink
          href="/system"
          value={services.isPending ? "–" : (services.data?.services.length ?? 0)}
          label="Data services"
        />
        <Cell title="Stack" icon={<Server />} className="home__stack">
          <div className="home__services">
            {stackServices.map(([name, href, [state, label]]) => (
              <Link key={name} href={href} className="home__service">
                <span className="label">{name}</span>
                <StateBadge state={state} label={label} />
              </Link>
            ))}
          </div>
        </Cell>
      </section>

      <section className="box box--main" aria-label="Apps and activity">
        <Cell
          title="Apps"
          icon={<Package />}
          action={
            <Link href="/apps">
              All <ArrowRight className="size-3.5" />
            </Link>
          }
        >
          {allApps.length === 0 ? (
            <EmptyState
              title="No apps"
              action={
                <Button asChild>
                  <Link href="/apps/new">
                    <Plus /> New app
                  </Link>
                </Button>
              }
            />
          ) : (
            <div className="rows">
              {allApps.slice(0, 6).map((app) => (
                <Link key={app.id} href={`/apps/${encodeURIComponent(app.slug)}`} className="row">
                  <span className="mono" aria-hidden="true">
                    {app.slug.slice(0, 1).toUpperCase()}
                  </span>
                  <span className="row__main">
                    <strong>{app.slug}</strong>
                    <small>
                      {app.toolchain} {app.version}
                    </small>
                  </span>
                  <span className="row__meta max-sm:hidden">{app.hosts[0] ?? ""}</span>
                  <StateBadge state={app.observed.state} />
                </Link>
              ))}
            </div>
          )}
        </Cell>
        <Cell
          title="Activity"
          icon={<Activity />}
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
            <div className="rows rows--lined">
              {recent.map((op) => (
                <Link key={op.id} href={`/activity/${op.id}`} className="row">
                  <span
                    className={`dot ${isTerminal(op.state) ? (op.state === "failed" ? "dot--bad" : "") : "dot--wait"}`}
                    aria-hidden="true"
                  />
                  <span className="row__main">
                    <strong>{describeOp(op)}</strong>
                  </span>
                  <span className="row__meta">{formatRelative(op.createdAt)}</span>
                </Link>
              ))}
            </div>
          )}
        </Cell>
      </section>
    </>
  );
}

function MetricLink({
  href,
  value,
  total,
  label,
  caution = false,
}: {
  href: string;
  value: number | string;
  total?: number;
  label: string;
  caution?: boolean;
}) {
  return (
    <Link href={href} className={`cell cell--link ${caution ? "cell--caution" : ""}`}>
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
