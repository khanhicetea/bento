import { useState } from "react";
import type { Application } from "@bento/shared";
import { ApplicationDatabasesDialog } from "./ApplicationDatabasesDialog.tsx";
import { ApplicationEditor } from "./ApplicationEditor.tsx";
import { ApplicationJobsDialog } from "./ApplicationJobsDialog.tsx";
import { RemoveApplicationDialog } from "./RemoveApplicationDialog.tsx";
import { useApplications } from "./useApplications.ts";

export function ApplicationsPage() {
  const {
    data,
    error,
    loading,
    changing,
    saving,
    addingDatabase,
    removing,
    reload,
    setEnabled,
    saveApplication,
    addDatabase,
    removeApplication,
    resetErrors,
  } = useApplications();
  const [query, setQuery] = useState("");
  const [editorTarget, setEditorTarget] = useState<Application | "create" | null>(null);
  const [databaseTarget, setDatabaseTarget] = useState<Application | null>(null);
  const [jobsTarget, setJobsTarget] = useState<Application | null>(null);
  const [removeTarget, setRemoveTarget] = useState<Application | null>(null);
  const normalizedQuery = query.trim().toLowerCase();
  const applications = (data?.applications ?? []).filter((app) =>
    `${app.slug} ${app.domain} ${app.aliases.join(" ")}`.toLowerCase().includes(normalizedQuery),
  );

  return (
    <section className="content" aria-live="polite">
      <div className="section-head">
        <div>
          <h2>Your applications</h2>
          <p>The first domain feature uses dedicated, schema-validated oRPC procedures.</p>
        </div>
        <div className="section-actions">
          <button className="btn btn-outline" disabled={loading} onClick={() => void reload()}>
            Refresh
          </button>
          <button
            className="btn btn-primary"
            disabled={!data?.initialized || !data.phpVersions.length}
            onClick={() => {
              resetErrors();
              setEditorTarget("create");
            }}
          >
            + New application
          </button>
        </div>
      </div>

      {error &&
        editorTarget === null &&
        databaseTarget === null &&
        jobsTarget === null &&
        removeTarget === null && (
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
                onToggle={() => setEnabled(app)}
                onEdit={() => {
                  resetErrors();
                  setEditorTarget(app);
                }}
                onDatabases={() => {
                  resetErrors();
                  setDatabaseTarget(app);
                }}
                onJobs={() => {
                  resetErrors();
                  setJobsTarget(app);
                }}
                onRemove={() => {
                  resetErrors();
                  setRemoveTarget(app);
                }}
              />
            ))}
            {!applications.length && (
              <div className="empty-state">
                <div className="empty-icon">◫</div>
                <h3>{query ? "No matching applications" : "No applications yet"}</h3>
                <p>
                  {query
                    ? "Try another app name or domain."
                    : "Create an application here to provision its runtime, domains, TLS, and database binding."}
                </p>
              </div>
            )}
          </div>
        </>
      )}
      {data?.initialized && editorTarget !== null && (
        <ApplicationEditor
          key={editorTarget === "create" ? "create" : editorTarget.slug}
          application={editorTarget === "create" ? null : editorTarget}
          settings={data}
          error={error}
          saving={saving}
          onClose={() => setEditorTarget(null)}
          onSave={saveApplication}
        />
      )}
      {data?.initialized && databaseTarget && (
        <ApplicationDatabasesDialog
          key={databaseTarget.slug}
          application={databaseTarget}
          settings={data}
          error={error}
          adding={addingDatabase}
          onClose={() => setDatabaseTarget(null)}
          onAdd={addDatabase}
        />
      )}
      {jobsTarget && (
        <ApplicationJobsDialog
          key={jobsTarget.slug}
          application={jobsTarget}
          onClose={() => setJobsTarget(null)}
        />
      )}
      {removeTarget && (
        <RemoveApplicationDialog
          key={removeTarget.slug}
          application={removeTarget}
          error={error}
          removing={removing}
          onClose={() => setRemoveTarget(null)}
          onRemove={removeApplication}
        />
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
  onEdit,
  onDatabases,
  onJobs,
  onRemove,
}: {
  app: Application;
  busy: boolean;
  onToggle: () => void;
  onEdit: () => void;
  onDatabases: () => void;
  onJobs: () => void;
  onRemove: () => void;
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
        <Fact label="Document root" value={app.documentRoot} />
        <Fact label="TLS" value={app.tls} />
      </div>
      <div className="app-tags">
        {app.databases.map((database, index) => (
          <span
            className={`badge ${index === 0 ? "badge-primary" : "badge-outline"}`}
            key={`${database.engine}:${database.service ?? database.file ?? index}`}
          >
            {database.engine}
            {database.names.length > 0 ? ` · ${database.names.length}` : ""}
          </span>
        ))}
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
        <div className="app-management-actions">
          <button className="btn btn-sm btn-ghost" disabled={busy} onClick={onDatabases}>
            Databases
          </button>
          <button className="btn btn-sm btn-ghost" disabled={busy} onClick={onJobs}>
            Crons &amp; workers
          </button>
          <button className="btn btn-sm btn-ghost" disabled={busy} onClick={onEdit}>
            Edit
          </button>
          <button className="btn btn-sm btn-ghost app-remove" disabled={busy} onClick={onRemove}>
            Remove
          </button>
        </div>
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
