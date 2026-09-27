import { useQuery } from "@tanstack/react-query";
import { ArrowRight, Check, ChevronRight, CircleAlert, LoaderCircle, Plus } from "lucide-react";
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
  const attention = allApps.filter(
    (app) =>
      app.observed.state === "failed" ||
      app.observed.state === "unhealthy" ||
      app.observed.state === "blocked" ||
      (app.desiredRuntime === "running" && app.observed.state === "stopped"),
  );
  const allOps = operations.data?.operations ?? [];
  const recentFailed = allOps.filter(
    (op) => op.state === "failed" && Date.now() - Date.parse(op.createdAt) < 86_400_000,
  );
  const initializing = (services.data?.services ?? []).filter(
    (service) => !service.initialized || service.state === "starting",
  );
  const issues = attention.length + recentFailed.length + initializing.length + (system.data?.dockerError ? 1 : 0);
  const checks = [operations, services, edge, tunnel, schedule];
  const checksUnavailable = checks.some((query) => query.isError);
  const checksPending = checks.some((query) => query.isPending);
  const running = allApps.filter((app) => app.observed.state === "healthy" || app.observed.state === "starting").length;
  const activeOps = allOps.filter((op) => !isTerminal(op.state)).length;
  const recent = allOps.slice(0, 6);
  const bad = issues > 0 || checksUnavailable;

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

      <div className="box box--4">
        <Cell className={`cell--span2 ${bad ? "cell--alert" : ""}`}>
          <div className="hero">
            <span className={`hero__seal ${bad ? "hero__seal--bad" : checksPending ? "hero__seal--wait" : ""}`}>
              {bad ? (
                <CircleAlert aria-hidden="true" />
              ) : checksPending ? (
                <LoaderCircle className="animate-spin" aria-hidden="true" />
              ) : (
                <Check aria-hidden="true" />
              )}
            </span>
            <div>
              <h2>
                {issues
                  ? `${issues} to check`
                  : checksUnavailable
                    ? "Some checks failed"
                    : checksPending
                      ? "Checking…"
                      : "All good"}
              </h2>
              <p>{issues ? "See below" : checksUnavailable ? "Refresh to retry" : "Nothing needs you"}</p>
            </div>
          </div>
        </Cell>
        <Cell>
          <div className="metric">
            <strong>
              {running}
              <small> / {allApps.length}</small>
            </strong>
            <span>Apps running</span>
          </div>
        </Cell>
        <Cell>
          <div className="metric">
            <strong>{services.isPending ? "–" : (services.data?.services.length ?? 0)}</strong>
            <span>Data services</span>
          </div>
        </Cell>
      </div>

      <div className="box box--main">
        <div className="col">
          {issues > 0 && (
            <Cell title="Needs attention" className="cell--alert">
              <div className="rows">
                {system.data?.dockerError && (
                  <AttentionRow href="/system" title="Docker unavailable" detail={system.data.dockerError} />
                )}
                {attention.map((app) => (
                  <AttentionRow
                    key={app.id}
                    href={`/apps/${encodeURIComponent(app.slug)}`}
                    title={app.slug}
                    detail={app.observed.message || `Wants ${app.desiredRuntime}, is ${app.observed.state}`}
                  />
                ))}
                {recentFailed.map((op) => (
                  <AttentionRow
                    key={op.id}
                    href={`/activity/${op.id}`}
                    title={describeOp(op)}
                    detail={op.errorMessage || "Failed"}
                  />
                ))}
                {initializing.map((service) => (
                  <AttentionRow key={service.name} href="/data" title={service.name} detail="Initializing" />
                ))}
              </div>
            </Cell>
          )}
          <Cell
            title="Apps"
            action={
              <Link href="/apps">
                All <ArrowRight className="size-3.5" />
              </Link>
            }
          >
            {allApps.length === 0 ? (
              <div className="empty">
                <strong>No apps yet</strong>
                <Button asChild size="sm">
                  <Link href="/apps/new">Create one</Link>
                </Button>
              </div>
            ) : (
              <div className="rows">
                {allApps.slice(0, 7).map((app) => {
                  const drift = app.desiredRuntime === "running" && app.observed.state === "stopped";
                  return (
                    <Link key={app.id} href={`/apps/${encodeURIComponent(app.slug)}`} className="row">
                      <span className="mono">{app.slug.slice(0, 1).toUpperCase()}</span>
                      <span className="row__main">
                        <strong>{app.slug}</strong>
                        <small>{app.primaryDomain || `${app.toolchain} ${app.version}`}</small>
                      </span>
                      <StateBadge state={drift ? "drift" : app.observed.state} />
                    </Link>
                  );
                })}
              </div>
            )}
          </Cell>
        </div>
        <div className="col">
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
          <Cell
            title="Activity"
            action={
              <Link href="/activity">
                {activeOps > 0 ? `${activeOps} active` : "All"} <ArrowRight className="size-3.5" />
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
                    <span
                      className={`dot ${op.state === "failed" ? "dot--bad" : isTerminal(op.state) ? "" : "dot--wait"}`}
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
        </div>
      </div>
    </>
  );
}

function AttentionRow({ href, title, detail }: { href: string; title: string; detail: string }) {
  return (
    <Link href={href} className="row">
      <span className="dot dot--bad" />
      <span className="row__main">
        <strong>{title}</strong>
        <small>{detail}</small>
      </span>
      <ChevronRight className="size-4" aria-hidden="true" />
    </Link>
  );
}
