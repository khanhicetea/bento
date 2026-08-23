import { useState, type FormEvent } from "react";
import type { AddApplicationDatabaseInput, Application, ApplicationList } from "@bento/shared";

type ApplicationDatabasesDialogProps = {
  application: Application;
  settings: ApplicationList;
  error: string | null;
  adding: boolean;
  onClose: () => void;
  onAdd: (input: AddApplicationDatabaseInput) => Promise<Application>;
};

export function ApplicationDatabasesDialog({
  application,
  settings,
  error,
  adding,
  onClose,
  onAdd,
}: ApplicationDatabasesDialogProps) {
  const defaultSelection = settings.defaults
    ? `${settings.defaults.databaseEngine}:${settings.defaults.databaseService}`
    : "sqlite";
  const [selection, setSelection] = useState(defaultSelection);
  const relational = selection.startsWith("mysql:") || selection.startsWith("postgres:");

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const [engine, service] = selection.split(":") as [
      AddApplicationDatabaseInput["engine"],
      string | undefined,
    ];
    const databaseName = String(form.get("databaseName") ?? "").trim();
    try {
      await onAdd({
        slug: application.slug,
        engine,
        service,
        databaseName: databaseName || undefined,
      });
      onClose();
    } catch {
      // The TanStack mutation exposes the sanitized oRPC error in the dialog.
    }
  }

  return (
    <dialog className="modal" open onCancel={(event) => event.preventDefault()}>
      <div className="modal-box app-databases-box">
        <button
          type="button"
          className="btn btn-sm btn-circle btn-ghost modal-close"
          aria-label="Close database manager"
          disabled={adding}
          onClick={onClose}
        >
          ✕
        </button>
        <h2>{application.slug} databases</h2>
        <p className="form-help">
          An application can own multiple engine bindings and multiple names on each relational
          binding.
        </p>
        {error && <div className="alert alert-error">{error}</div>}

        <div className="database-binding-list">
          {application.databases.map((binding, index) => (
            <section
              className="database-binding"
              key={`${binding.engine}:${binding.service ?? binding.file ?? index}`}
            >
              <div>
                <strong>{databaseLabel(binding.engine)}</strong>
                <small>{binding.service ?? binding.file ?? "Local application database"}</small>
              </div>
              <div className="database-names">
                {binding.names.map((name) => (
                  <span className="badge badge-outline" key={name}>
                    {name}
                  </span>
                ))}
                {!binding.names.length && binding.file && (
                  <span className="badge badge-outline">SQLite file</span>
                )}
                {index === 0 && <span className="badge badge-primary">primary</span>}
              </div>
            </section>
          ))}
        </div>

        <form onSubmit={(event) => void submit(event)}>
          <fieldset disabled={adding}>
            <div className="form-section">
              <h3>Add a database</h3>
              <label>
                <span className="label-text">Engine or managed service</span>
                <select
                  className="select select-bordered w-full"
                  value={selection}
                  onChange={(event) => setSelection(event.target.value)}
                >
                  {settings.databaseServices.map((service) => (
                    <option
                      key={`${service.engine}:${service.service}`}
                      value={`${service.engine}:${service.service}`}
                    >
                      {databaseLabel(service.engine)} {service.version} ({service.service})
                    </option>
                  ))}
                  <option value="sqlite">SQLite file</option>
                  <option value="litestream">SQLite file with Litestream</option>
                </select>
              </label>
              {relational && (
                <label>
                  <span className="label-text">
                    Database name{" "}
                    <small>
                      {application.slug} or {application.slug}_*
                    </small>
                  </span>
                  <input
                    className="input input-bordered w-full"
                    name="databaseName"
                    required
                    pattern={`${application.slug}(_[A-Za-z0-9_]+)?`}
                    defaultValue={`${application.slug}_`}
                  />
                </label>
              )}
              <p className="form-help">
                Selecting an existing service adds a database name to that binding. Selecting a new
                engine or service creates another binding. SQLite selections create independent
                files.
              </p>
            </div>
          </fieldset>
          <div className="modal-action">
            <button type="button" className="btn btn-ghost" disabled={adding} onClick={onClose}>
              Close
            </button>
            <button className="btn btn-primary" type="submit" disabled={adding}>
              {adding && <span className="loading loading-spinner loading-xs" />}
              Add database
            </button>
          </div>
        </form>
        <p className="form-help">
          Database removal and permanent data deletion remain intentionally unavailable in the web
          control plane.
        </p>
      </div>
      <button className="modal-backdrop" aria-label="Close" disabled={adding} onClick={onClose} />
    </dialog>
  );
}

function databaseLabel(engine: string): string {
  if (engine === "mysql") return "MySQL";
  if (engine === "postgres") return "PostgreSQL";
  if (engine === "litestream") return "SQLite + Litestream";
  return "SQLite";
}
