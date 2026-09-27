import { useQuery } from "@tanstack/react-query";
import { ArrowRight, Check, CircleAlert, Database, History, LoaderCircle, Network, Server } from "lucide-react";
import { Link } from "wouter";
import { api, messageOf } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { DomainError, DomainLoading, Page, StateBadge } from "../../components/DomainState.tsx";
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
        <DomainError
          message={messageOf(system.error ?? apps.error)}
          onRetry={() => {
            void system.refetch();
            void apps.refetch();
          }}
        />
      </Page>
    );

  const allApps = apps.data?.apps ?? [];
  const attention = allApps.filter(
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
  const issues = attention.length + recentFailed.length + initializing.length + (system.data?.dockerError ? 1 : 0);
  const checksUnavailable = [operations, services, edge, tunnel, schedule].some((query) => query.isError);
  const checksPending = [operations, services, edge, tunnel, schedule].some((query) => query.isPending);
  const running = allApps.filter((app) => app.observed.state === "healthy" || app.observed.state === "starting").length;
  const recent = (operations.data?.operations ?? []).slice(0, 5);

  return (
    <Page wide>
      <div className="overview-intro">
        <div>
          <h1>Good to see you.</h1>
          <p>
            Here’s what’s happening on <strong>{system.data?.stackName}</strong>.
          </p>
        </div>
        <Link href="/apps/new" className="overview-create">
          New application <ArrowRight className="size-4" aria-hidden="true" />
        </Link>
      </div>
      <section
        className={`overview-condition ${issues || checksUnavailable ? "overview-condition--attention" : ""}`}
        aria-label="Stack condition"
      >
        <div className="overview-condition__icon">
          {issues || checksUnavailable ? (
            <CircleAlert aria-hidden="true" />
          ) : checksPending ? (
            <LoaderCircle aria-hidden="true" />
          ) : (
            <Check aria-hidden="true" />
          )}
        </div>
        <div className="overview-condition__body">
          <h2>
            {issues
              ? `${issues} ${issues === 1 ? "thing needs" : "things need"} attention`
              : checksUnavailable
                ? "Some checks are unavailable"
                : checksPending
                  ? "Checking your stack"
                  : "Everything is on track"}
          </h2>
          <p>
            {issues
              ? checksUnavailable
                ? "Review the items below; some other checks could not be completed."
                : "Review the items below to see what needs your help."
              : checksUnavailable
                ? "We can’t confirm everything is healthy. Try refreshing the unavailable sections."
                : checksPending
                  ? "We’re still checking services and recent activity."
                  : "No drift, recent failures, or initializing services found."}
          </p>
        </div>
        <div className="overview-condition__counts">
          <strong>
            {running}
            <span> / {allApps.length}</span>
          </strong>
          <small>apps running</small>
        </div>
      </section>
      <div className="overview-grid">
        <div className="overview-grid__primary">
          <section className="overview-section overview-section--attention" aria-labelledby="attention-title">
            <div className="overview-section__heading">
              <div>
                <h2 id="attention-title">Needs attention</h2>
                <p>Only the things you may need to act on.</p>
              </div>
              <span className="overview-section__total">
                {issues === 0 && (checksUnavailable || checksPending) ? "—" : issues}
              </span>
            </div>
            {issues === 0 ? (
              <div className="overview-clear">
                {checksUnavailable ? (
                  <CircleAlert className="size-5" aria-hidden="true" />
                ) : checksPending ? (
                  <LoaderCircle className="size-5" aria-hidden="true" />
                ) : (
                  <Check className="size-5" aria-hidden="true" />
                )}
                <span>
                  {checksUnavailable
                    ? "Some checks could not be loaded. Refresh to try again."
                    : checksPending
                      ? "Checking services and activity…"
                      : "All clear. Nothing to resolve right now."}
                </span>
              </div>
            ) : (
              <div className="overview-list">
                {system.data?.dockerError && (
                  <Link href="/system" className="overview-list__item">
                    <span className="overview-list__marker overview-list__marker--danger" />
                    <span>
                      <strong>Docker unavailable</strong>
                      <small>{system.data.dockerError}</small>
                    </span>
                    <ArrowRight className="size-4" aria-hidden="true" />
                  </Link>
                )}
                {attention.map((app) => (
                  <Link key={app.id} href={`/apps/${encodeURIComponent(app.slug)}`} className="overview-list__item">
                    <span className="overview-list__marker overview-list__marker--danger" />
                    <span>
                      <strong>{app.slug}</strong>
                      <small>
                        {app.observed.message || `Wanted ${app.desiredRuntime}, observed ${app.observed.state}`}
                      </small>
                    </span>
                    <ArrowRight className="size-4" aria-hidden="true" />
                  </Link>
                ))}
                {recentFailed.map((op) => (
                  <Link key={op.id} href={`/activity/${op.id}`} className="overview-list__item">
                    <span className="overview-list__marker overview-list__marker--danger" />
                    <span>
                      <strong>{describeOp(op)}</strong>
                      <small>{op.errorMessage || "Operation failed"}</small>
                    </span>
                    <ArrowRight className="size-4" aria-hidden="true" />
                  </Link>
                ))}
                {initializing.map((service) => (
                  <Link key={service.name} href="/data" className="overview-list__item">
                    <span className="overview-list__marker overview-list__marker--pending" />
                    <span>
                      <strong>{service.name}</strong>
                      <small>Service initializing</small>
                    </span>
                    <ArrowRight className="size-4" aria-hidden="true" />
                  </Link>
                ))}
              </div>
            )}
          </section>
          <section className="overview-section overview-section--apps" aria-labelledby="apps-title">
            <div className="overview-section__heading">
              <div>
                <h2 id="apps-title">Applications</h2>
                <p>Current state of your apps.</p>
              </div>
              <Link href="/apps" className="overview-section__link">
                View all <ArrowRight className="size-4" aria-hidden="true" />
              </Link>
            </div>
            {allApps.length === 0 ? (
              <div className="overview-clear">
                No applications yet. <Link href="/apps/new">Create your first application</Link>
              </div>
            ) : (
              <div className="overview-list">
                {allApps.slice(0, 6).map((app) => (
                  <Link
                    key={app.id}
                    href={`/apps/${encodeURIComponent(app.slug)}`}
                    className="overview-list__item overview-list__item--app"
                  >
                    <span className="overview-app__initial" aria-hidden="true">
                      {app.slug.slice(0, 1).toUpperCase()}
                    </span>
                    <span>
                      <strong>{app.slug}</strong>
                      <small>{app.primaryDomain || `${app.toolchain} ${app.version}`}</small>
                    </span>
                    <StateBadge
                      state={
                        app.desiredRuntime === "running" && app.observed.state === "stopped"
                          ? "drift"
                          : app.observed.state
                      }
                    />
                    <ArrowRight className="size-4 overview-list__arrow" aria-hidden="true" />
                  </Link>
                ))}
              </div>
            )}
          </section>
        </div>
        <div className="overview-grid__secondary">
          <section className="overview-section overview-section--services" aria-labelledby="services-title">
            <div className="overview-section__heading">
              <div>
                <h2 id="services-title">Stack services</h2>
                <p>Connections and scheduled work.</p>
              </div>
            </div>
            <div className="overview-service-list">
              <Link href="/system">
                <Server className="size-4" aria-hidden="true" />
                <span>Docker</span>
                <StateBadge
                  state={system.data?.dockerError ? "failed" : "healthy"}
                  label={system.data?.dockerError ? "Unavailable" : "Connected"}
                />
              </Link>
              <Link href="/ingress">
                <Network className="size-4" aria-hidden="true" />
                <span>Managed edge</span>
                <StateBadge
                  state={
                    edge.error
                      ? "failed"
                      : edge.isPending
                        ? "queued"
                        : edge.data?.settings.enabled
                          ? edge.data.state
                          : "absent"
                  }
                  label={
                    edge.isPending
                      ? "Checking"
                      : edge.error
                        ? "Unavailable"
                        : edge.data?.settings.enabled
                          ? undefined
                          : "Disabled"
                  }
                />
              </Link>
              <Link href="/ingress">
                <Network className="size-4" aria-hidden="true" />
                <span>Tunnel</span>
                <StateBadge
                  state={
                    tunnel.error
                      ? "failed"
                      : tunnel.isPending
                        ? "queued"
                        : tunnel.data?.enabled
                          ? tunnel.data.state
                          : "absent"
                  }
                  label={
                    tunnel.isPending
                      ? "Checking"
                      : tunnel.error
                        ? "Unavailable"
                        : tunnel.data?.enabled
                          ? undefined
                          : "Disabled"
                  }
                />
              </Link>
              <Link href="/backups">
                <History className="size-4" aria-hidden="true" />
                <span>Backup schedule</span>
                <StateBadge
                  state={
                    schedule.error
                      ? "failed"
                      : schedule.isPending
                        ? "queued"
                        : schedule.data?.enabled
                          ? schedule.data.lastState || "queued"
                          : "absent"
                  }
                  label={
                    schedule.isPending
                      ? "Checking"
                      : schedule.error
                        ? "Unavailable"
                        : schedule.data?.enabled
                          ? schedule.data.lastState || "Enabled"
                          : "Disabled"
                  }
                />
              </Link>
              <Link href="/data">
                <Database className="size-4" aria-hidden="true" />
                <span>Data services</span>
                <strong>
                  {services.isPending ? "…" : services.error ? "Unavailable" : (services.data?.services.length ?? 0)}
                </strong>
              </Link>
            </div>
          </section>
          <section className="overview-section overview-section--activity" aria-labelledby="activity-title">
            <div className="overview-section__heading">
              <div>
                <h2 id="activity-title">Recent activity</h2>
                <p>What changed on this stack.</p>
              </div>
              <Link href="/activity" className="overview-section__link">
                View all <ArrowRight className="size-4" aria-hidden="true" />
              </Link>
            </div>
            {operations.isPending ? (
              <DomainLoading label="activity" />
            ) : operations.error ? (
              <DomainError message={messageOf(operations.error)} onRetry={() => void operations.refetch()} />
            ) : recent.length === 0 ? (
              <div className="overview-clear">No activity yet.</div>
            ) : (
              <div className="overview-activity-list">
                {recent.map((op) => (
                  <Link key={op.id} href={`/activity/${op.id}`}>
                    <span
                      className={`overview-activity-list__dot ${op.state === "failed" ? "overview-activity-list__dot--error" : ""}`}
                    />
                    <span>
                      <strong>{describeOp(op)}</strong>
                      <small>{formatRelative(op.createdAt)}</small>
                    </span>
                    <StateBadge state={op.state} />
                  </Link>
                ))}
              </div>
            )}
          </section>
        </div>
      </div>
    </Page>
  );
}
