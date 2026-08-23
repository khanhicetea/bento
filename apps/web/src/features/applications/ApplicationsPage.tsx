import { useMemo, useState } from "react";
import type { Application } from "@bento/shared";
import { useApplications } from "./useApplications.ts";

export function ApplicationsPage() {
  const { data, error, loading, changing, reload, setEnabled } = useApplications();
  const [query, setQuery] = useState("");
  const applications = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    return (data?.applications ?? []).filter((app) =>
      `${app.slug} ${app.domain} ${app.aliases.join(" ")}`.toLowerCase().includes(normalized),
    );
  }, [data, query]);

  return (
    <section className="content" aria-live="polite">
      <div className="section-head">
        <div>
          <h2>Your applications</h2>
          <p>The first domain feature uses dedicated, schema-validated oRPC procedures.</p>
        </div>
        <button className="btn btn-outline" disabled={loading} onClick={() => void reload()}>
          Refresh
        </button>
      </div>

      {error && (
        <div className="alert alert-error">
          <span>{error}</span>
        </div>
      )}
      {loading && !data && (
        <div className="loading-state">
          <span className="loading loading-spinner loading-lg" /> Loading applications…
        </div>
      )}
      {data && !data.initialized && (
        <div className="hero">
          <div>
            <p className="eyebrow">STACK NOT READY</p>
            <h2>Initialize this stack before managing applications.</h2>
            <p>
              {data.error ?? (
                <>
                  Run <code>bento init</code> for <code>{data.stackRoot}</code>, then refresh.
                </>
              )}
            </p>
          </div>
        </div>
      )}
      {data?.initialized && (
        <>
          <div className="app-summary">
            <Summary value={data.applications.length} label="Total apps" />
            <Summary
              value={data.applications.filter((app) => app.enabled).length}
              label="Running"
            />
            <Summary
              value={data.applications.reduce((sum, app) => sum + app.databases.length, 0)}
              label="Databases"
            />
            <label className="app-search">
              <span aria-hidden="true">⌕</span>
              <input
                className="input"
                type="search"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Search apps or domains…"
                aria-label="Search applications"
              />
            </label>
          </div>
          <div className="app-grid">
            {applications.map((app) => (
              <ApplicationCard
                key={app.slug}
                app={app}
                busy={changing === app.slug}
                onToggle={() => void setEnabled(app)}
              />
            ))}
            {!applications.length && (
              <div className="empty-state">
                <div className="empty-icon">◫</div>
                <h3>{query ? "No matching applications" : "No applications yet"}</h3>
                <p>
                  {query
                    ? "Try another app name or domain."
                    : "Create one with the CLI; create forms will move here in the next application increment."}
                </p>
              </div>
            )}
          </div>
        </>
      )}
    </section>
  );
}

function Summary({ value, label }: { value: number; label: string }) {
  return (
    <div>
      <strong>{value}</strong>
      <span>{label}</span>
    </div>
  );
}

function ApplicationCard({
  app,
  busy,
  onToggle,
}: {
  app: Application;
  busy: boolean;
  onToggle: () => void;
}) {
  return (
    <article className="app-card">
      <div className="app-card-head">
        <div className="app-identity">
          <span className="app-avatar">{app.slug.slice(0, 1).toUpperCase()}</span>
          <div>
            <h3>{app.slug}</h3>
            <a href={`https://${app.domain}`} target="_blank" rel="noreferrer">
              {app.domain} ↗
            </a>
          </div>
        </div>
        <span className={`badge ${app.enabled ? "badge-success" : "badge-warning"}`}>
          {app.enabled ? "Running" : "Disabled"}
        </span>
      </div>
      <div className="app-facts">
        <Fact label="Runtime" value={`PHP ${app.phpVersion}`} />
        <Fact label="Capacity" value={app.fpmProfile} />
        <Fact label="TLS" value={app.tls} />
        <Fact label="Data" value={app.databases.map((db) => db.engine).join(", ") || "None"} />
      </div>
      <div className="app-tags">
        {app.deployEnabled && <span className="badge badge-success">Deploys</span>}
        {app.accessLog && <span className="badge badge-outline">Access logs</span>}
        {app.aliases.slice(0, 2).map((alias) => (
          <span className="badge badge-outline" key={alias}>
            {alias}
          </span>
        ))}
      </div>
      <div className="app-actions">
        <button className="btn btn-sm btn-primary" disabled={busy} onClick={onToggle}>
          {busy && <span className="loading loading-spinner loading-xs" />}
          {app.enabled ? "Disable" : "Enable"}
        </button>
      </div>
    </article>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <span>
      <small>{label}</small>
      <strong>{value}</strong>
    </span>
  );
}
