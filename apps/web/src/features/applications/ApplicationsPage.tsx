import { useState } from "react";
import { AppWindow, ArrowUpRight, ExternalLink, Search } from "lucide-react";
import { Link } from "wouter";
import { messageOf, type T } from "../../api/client.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import { DomainError, DomainLoading, EmptyState, Page, PageHeader, StateBadge } from "../../components/DomainState.tsx";
import { useActiveOperations } from "../operations/useActiveOperations.ts";
import { useAppAction, useApplicationList, type AppAction } from "./useApplications.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

type Filter = "all" | "running" | "stopped" | "attention";
const filters: Array<[Filter, string]> = [
  ["all", "All apps"],
  ["running", "Running"],
  ["stopped", "Stopped"],
  ["attention", "Needs attention"],
];

export function ApplicationsPage() {
  const list = useApplicationList();
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const needle = query.trim().toLowerCase();
  const apps = (list.data?.apps ?? []).filter((app) => {
    const matchesText = `${app.slug} ${app.primaryDomain}`.toLowerCase().includes(needle);
    const observedRunning = app.observed.state === "healthy" || app.observed.state === "starting";
    const drift = app.desiredRuntime !== (observedRunning ? "running" : "stopped");
    const needsAttention = drift || ["blocked", "failed", "unhealthy"].includes(app.observed.state);
    return (
      matchesText &&
      (filter === "all" ||
        (filter === "running" && observedRunning && !needsAttention) ||
        (filter === "stopped" && app.desiredRuntime === "stopped" && !needsAttention) ||
        (filter === "attention" && needsAttention))
    );
  });
  return (
    <Page wide>
      <PageHeader
        title="Applications"
        description="A place for every app. See what’s running and what needs you."
        actions={
          <Button asChild>
            <Link href="/apps/new">New application</Link>
          </Button>
        }
      />
      <div className="bento-filterbar">
        <label className="bento-filterbar__search">
          <Search className="size-4" aria-hidden="true" />
          <span className="sr-only">Search applications</span>
          <Input placeholder="Find an app or domain" value={query} onChange={(event) => setQuery(event.target.value)} />
        </label>
        <div className="bento-filterbar__tabs" role="group" aria-label="Filter applications">
          {filters.map(([value, label]) => (
            <button
              key={value}
              type="button"
              className={filter === value ? "is-selected" : ""}
              aria-pressed={filter === value}
              onClick={() => setFilter(value)}
            >
              {label}
            </button>
          ))}
        </div>
        {list.data && (
          <span className="bento-filterbar__count">
            {apps.length} {apps.length === 1 ? "app" : "apps"}
          </span>
        )}
      </div>
      {list.isPending && <DomainLoading label="applications" />}
      {list.error && <DomainError message={messageOf(list.error)} onRetry={() => void list.refetch()} />}
      {list.data &&
        (apps.length === 0 ? (
          <div className="bento-empty">
            <EmptyState
              icon={<AppWindow className="size-8" />}
              title={list.data.apps.length === 0 ? "Your tray is ready" : "No matching applications"}
              body={
                list.data.apps.length === 0
                  ? "Create your first app to give it a place here."
                  : "Try another search or choose a different filter."
              }
              action={
                list.data.apps.length === 0 ? (
                  <Button asChild>
                    <Link href="/apps/new">Create application</Link>
                  </Button>
                ) : undefined
              }
            />
          </div>
        ) : (
          <div className="bento-app-grid">
            {apps.map((app) => (
              <ApplicationTile key={app.id} app={app} />
            ))}
          </div>
        ))}
    </Page>
  );
}

function ApplicationTile({ app }: { app: T.AppSummary }) {
  const action = useAppAction();
  const active = useActiveOperations(app.id);
  const [confirm, setConfirm] = useState<AppAction | null>(null);
  const observedRunning = app.observed.state === "healthy" || app.observed.state === "starting";
  const drift = app.desiredRuntime !== (observedRunning ? "running" : "stopped");
  const status = drift ? "drift" : app.observed.state;
  const run = (verb: AppAction) => action.mutate({ id: app.id, action: verb }, { onSuccess: () => setConfirm(null) });
  const request = (verb: AppAction) =>
    verb === "stop" || verb === "restart" || verb === "unpublish" ? setConfirm(verb) : run(verb);
  const disabled = active.active || action.isPending;
  return (
    <article
      className={`bento-app-tile ${drift || ["blocked", "failed", "unhealthy"].includes(app.observed.state) ? "bento-app-tile--attention" : ""}`}
      aria-label={`${app.slug} application`}
    >
      <div className="bento-app-tile__top">
        <span className="bento-app-tile__mark" aria-hidden="true">
          {app.slug.slice(0, 1).toUpperCase()}
        </span>
        <StateBadge
          state={status}
          title={app.observed.message}
          label={drift ? `Drift · wants ${app.desiredRuntime}` : undefined}
        />
      </div>
      <div className="bento-app-tile__identity">
        <Link href={`/apps/${encodeURIComponent(app.slug)}`} className="bento-app-tile__name">
          {app.slug}
          <ArrowUpRight className="size-5" aria-hidden="true" />
        </Link>
        {app.primaryDomain ? (
          <a href={`//${app.primaryDomain}`} target="_blank" rel="noreferrer" className="bento-app-tile__domain">
            {app.primaryDomain}
            <ExternalLink className="size-3" aria-hidden="true" />
          </a>
        ) : (
          <span className="bento-app-tile__domain">Private app</span>
        )}
      </div>
      <dl className="bento-app-tile__facts">
        <div>
          <dt>Runtime</dt>
          <dd>
            {app.toolchain} {app.version}
          </dd>
        </div>
        <div>
          <dt>Access</dt>
          <dd>
            {app.ingress === "managed"
              ? app.publication === "published"
                ? "Published · managed"
                : "Unpublished · managed"
              : app.ingress === "none"
                ? "Private"
                : "External"}
          </dd>
        </div>
      </dl>
      {active.active && (
        <div className="bento-app-tile__progress">
          <StateBadge state="running" label={active.operations[0]?.kind.replace("app.", "") ?? "Working"} />
          <Link href={`/activity/${active.operations[0]?.id ?? ""}`}>View progress</Link>
        </div>
      )}
      {app.observed.message && status !== "healthy" && (
        <p className="bento-app-tile__message">{app.observed.message}</p>
      )}
      <div className="bento-app-tile__actions">
        <Button
          size="sm"
          disabled={disabled}
          onClick={() => request(app.desiredRuntime === "stopped" ? "start" : "restart")}
        >
          {app.desiredRuntime === "stopped" ? "Start" : "Restart"}
        </Button>
        {app.desiredRuntime === "running" && (
          <Button size="sm" variant="outline" disabled={disabled} onClick={() => request("stop")}>
            Stop
          </Button>
        )}
        {app.ingress === "managed" && app.desiredRuntime === "running" && (
          <Button
            size="sm"
            variant="outline"
            disabled={disabled}
            onClick={() => request(app.publication === "published" ? "unpublish" : "publish")}
          >
            {app.publication === "published" ? "Unpublish" : "Publish"}
          </Button>
        )}
      </div>
      {action.error && (
        <Alert variant="destructive" className="mt-3">
          {messageOf(action.error)}
        </Alert>
      )}
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(open) => !open && setConfirm(null)}
        title={`${confirm ? confirm[0]?.toUpperCase() + confirm.slice(1) : "Change"} ${app.slug}?`}
        description={
          confirm === "stop"
            ? "This stops the running application."
            : confirm === "unpublish"
              ? "This removes its managed public route."
              : "This replaces the running instance after a controlled stop."
        }
        confirmLabel={confirm ? confirm[0]?.toUpperCase() + confirm.slice(1) : "Confirm"}
        pending={action.isPending}
        error={action.error}
        onConfirm={() => confirm && run(confirm)}
      />
    </article>
  );
}
