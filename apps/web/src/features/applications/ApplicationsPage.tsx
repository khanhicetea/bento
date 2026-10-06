import { useState } from "react";
import { Archive, Boxes, ExternalLink, Globe, Lock, Plus, Search } from "lucide-react";
import { Link } from "wouter";
import { messageOf, type T } from "../../api/client.ts";
import { DomainError, DomainLoading, EmptyState, PageHeader, StateBadge } from "../../components/DomainState.tsx";
import { EngineLogo } from "../../components/EngineLogo.tsx";
import { formatRelative } from "../../lib/format.ts";
import { useActiveOperations } from "../operations/useActiveOperations.ts";
import { RestoreFromBackupDialog } from "./CloneFromBackupDialog.tsx";
import { useApplicationList } from "./useApplications.ts";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const engineLabel: Record<T.Engine, string> = {
  mysql: "MySQL",
  postgres: "Postgres",
  sqlite: "SQLite",
  redis: "Redis",
};

// Databases per engine; a binding without listed databases still counts as one.
function databaseCounts(bindings: T.BindingSummary[]) {
  const counts: Partial<Record<T.Engine, number>> = {};
  for (const b of bindings) counts[b.engine] = (counts[b.engine] ?? 0) + Math.max(b.databases, 1);
  return counts;
}

const formatMemory = (mb: number) => (mb >= 1024 ? `${+(mb / 1024).toFixed(1)} GiB` : `${mb} MiB`);

type Filter = "all" | "running" | "stopped" | "attention";
const filters: Array<[Filter, string]> = [
  ["all", "All"],
  ["running", "Running"],
  ["stopped", "Stopped"],
  ["attention", "Attention"],
];

export function ApplicationsPage() {
  const list = useApplicationList();
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [restoring, setRestoring] = useState(false);
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
    <>
      <PageHeader
        title="Apps"
        actions={
          <>
            <Button variant="outline" onClick={() => setRestoring(true)}>
              <Archive /> From app backup
            </Button>
            <Button asChild>
              <Link href="/apps/new">
                <Plus /> New app
              </Link>
            </Button>
          </>
        }
      />
      <RestoreFromBackupDialog open={restoring} onClose={() => setRestoring(false)} />
      <div className="toolbar">
        <label className="toolbar__search">
          <Search aria-hidden="true" />
          <span className="sr-only">Search apps</span>
          <Input placeholder="Search" value={query} onChange={(event) => setQuery(event.target.value)} />
        </label>
        <div className="seg" role="group" aria-label="Filter apps">
          {filters.map(([value, label]) => (
            <button key={value} type="button" aria-pressed={filter === value} onClick={() => setFilter(value)}>
              {label}
            </button>
          ))}
        </div>
      </div>
      {list.isPending && <DomainLoading label="apps" />}
      {list.error && <DomainError message={messageOf(list.error)} onRetry={() => void list.refetch()} />}
      {list.data &&
        (apps.length === 0 ? (
          <div className="box">
            <div className="cell">
              <EmptyState
                icon={<Boxes />}
                title={list.data.apps.length === 0 ? "Your box is empty" : "No matches"}
                action={
                  list.data.apps.length === 0 ? (
                    <Button asChild>
                      <Link href="/apps/new">Create app</Link>
                    </Button>
                  ) : undefined
                }
              />
            </div>
          </div>
        ) : (
          <div className="box">
            <div className="tiles">
              {apps.map((app) => (
                <ApplicationTile key={app.id} app={app} />
              ))}
              <Link href="/apps/new" className="cell tile tile--add">
                <Plus aria-hidden="true" />
                New app
              </Link>
            </div>
          </div>
        ))}
    </>
  );
}

function ApplicationTile({ app }: { app: T.AppSummary }) {
  const active = useActiveOperations(app.id);
  const observedRunning = app.observed.state === "healthy" || app.observed.state === "starting";
  const drift = app.desiredRuntime !== (observedRunning ? "running" : "stopped");
  const attention = drift || ["blocked", "failed", "unhealthy"].includes(app.observed.state);
  const status = drift ? "drift" : app.observed.state;
  const href = `/apps/${encodeURIComponent(app.slug)}`;
  const exposure =
    app.ingress === "managed"
      ? app.publication === "published"
        ? "Public"
        : "Unpublished"
      : app.ingress === "none"
        ? "Private"
        : "External";
  return (
    <article className={`cell tile ${attention ? "cell--alert" : ""}`} aria-label={app.slug}>
      <div className="tile__top">
        <span className={`mono mono--lg ${attention ? "mono--bad" : ""}`} aria-hidden="true">
          {app.slug.slice(0, 1).toUpperCase()}
        </span>
        <div className="tile__name">
          <Link href={href} className="tile__link">
            {app.slug}
          </Link>
          <small>
            {app.primaryDomain ? (
              <a href={`//${app.primaryDomain}`} target="_blank" rel="noreferrer">
                {app.primaryDomain} <ExternalLink className="inline size-3" aria-hidden="true" />
              </a>
            ) : (
              "No domain"
            )}
          </small>
        </div>
        <StateBadge
          state={active.active ? "running" : status}
          title={app.observed.message}
          label={
            active.active
              ? (active.operations[0]?.kind.replace("app.", "") ?? "Working")
              : drift
                ? `Wants ${app.desiredRuntime}`
                : undefined
          }
        />
      </div>
      <dl className="facts">
        <div>
          <dt>Runtime</dt>
          <dd>
            {app.toolchain} {app.version}
          </dd>
        </div>
        <div>
          <dt>Ingress</dt>
          <dd className="inline-flex items-center gap-1">
            {app.ingress === "none" ? <Lock aria-hidden="true" /> : <Globe aria-hidden="true" />}
            {exposure}
          </dd>
        </div>
        <div>
          <dt>Up</dt>
          <dd title={app.observed.startedAt}>
            {observedRunning && app.observed.startedAt
              ? formatRelative(app.observed.startedAt).replace(/ ago$/, "")
              : "—"}
          </dd>
        </div>
        <div>
          <dt>Limits</dt>
          <dd>
            {app.resources.cpuMillis / 1000} CPU · {formatMemory(app.resources.memoryMb)}
          </dd>
        </div>
        <div className="facts__wide">
          <dt>Data</dt>
          <dd>
            {app.bindingSummary.length === 0 ? (
              <span className="text-muted-foreground">No bindings</span>
            ) : (
              <span className="chips">
                {Object.entries(databaseCounts(app.bindingSummary)).map(([engine, count]) => (
                  <span key={engine} className="chip">
                    <EngineLogo engine={engine} className="size-3.5" />
                    {engineLabel[engine as T.Engine]} ×{count}
                  </span>
                ))}
              </span>
            )}
          </dd>
        </div>
      </dl>
      {(!app.provisioned || !app.observed.generationCurrent) && (
        <div className="tags">
          {!app.provisioned && <span className="tag">Not provisioned</span>}
          {app.provisioned && !app.observed.generationCurrent && <span className="tag">Update pending</span>}
        </div>
      )}
      {app.observed.message && status !== "healthy" && <p className="note note--bad">{app.observed.message}</p>}
    </article>
  );
}
