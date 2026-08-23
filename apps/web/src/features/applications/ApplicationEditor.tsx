import { useState, type FormEvent } from "react";
import type { Application, ApplicationList, SaveApplicationInput } from "@bento/shared";

type ApplicationEditorProps = {
  application: Application | null;
  settings: ApplicationList;
  error: string | null;
  saving: boolean;
  onClose: () => void;
  onSave: (input: SaveApplicationInput) => Promise<Application>;
};

export function ApplicationEditor({
  application,
  settings,
  error,
  saving,
  onClose,
  onSave,
}: ApplicationEditorProps) {
  const primaryDatabase = application?.databases[0];
  const initialDatabase = primaryDatabase
    ? databaseSelection(primaryDatabase.engine, primaryDatabase.service)
    : databaseSelection(
        settings.defaults?.databaseEngine ?? "sqlite",
        settings.defaults?.databaseService,
      );
  const [database, setDatabase] = useState(initialDatabase);
  const [tls, setTls] = useState(application?.tls ?? "shared");
  const relationalDatabase = database.startsWith("mysql:") || database.startsWith("postgres:");
  const creating = application === null;

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const [databaseEngine, databaseService] = database.split(":") as [
      SaveApplicationInput["databaseEngine"],
      string | undefined,
    ];
    const databaseName = String(form.get("databaseName") ?? "").trim();
    const input: SaveApplicationInput = {
      slug: String(form.get("slug") ?? "").trim(),
      domain: String(form.get("domain") ?? "").trim(),
      aliases: String(form.get("aliases") ?? "")
        .split(",")
        .map((alias) => alias.trim())
        .filter(Boolean),
      documentRoot: String(form.get("documentRoot") ?? "").trim(),
      entrypointMode: String(form.get("entrypointMode")) as SaveApplicationInput["entrypointMode"],
      phpVersion: String(form.get("phpVersion")),
      fpmProfile: String(form.get("fpmProfile")),
      tls,
      tlsCertificatePath:
        tls === "external" ? String(form.get("tlsCertificatePath") ?? "").trim() : undefined,
      tlsKeyPath: tls === "external" ? String(form.get("tlsKeyPath") ?? "").trim() : undefined,
      accessLog: form.get("accessLog") === "on",
      databaseEngine,
      databaseService,
      createDatabase: relationalDatabase && form.get("createDatabase") === "on",
      databaseName:
        relationalDatabase && form.get("createDatabase") === "on"
          ? databaseName || undefined
          : undefined,
    };

    try {
      await onSave(input);
      onClose();
    } catch {
      // The TanStack mutation exposes the sanitized oRPC error in the dialog.
    }
  }

  return (
    <dialog className="modal" open onCancel={(event) => event.preventDefault()}>
      <div className="modal-box app-editor-box">
        <button
          type="button"
          className="btn btn-sm btn-circle btn-ghost modal-close"
          aria-label="Close application editor"
          disabled={saving}
          onClick={onClose}
        >
          ✕
        </button>
        <h2>{creating ? "Create application" : `Edit ${application.slug}`}</h2>
        <p className="form-help">
          Changes are saved to desired state, rendered, validated, and applied immediately.
        </p>
        {error && <div className="alert alert-error">{error}</div>}

        <form onSubmit={(event) => void submit(event)}>
          <fieldset disabled={saving}>
            <div className="form-grid">
              <label>
                <span className="label-text">Slug</span>
                <input
                  className="input input-bordered w-full"
                  name="slug"
                  required
                  maxLength={63}
                  pattern="[a-z0-9][a-z0-9-]*"
                  defaultValue={application?.slug ?? ""}
                  readOnly={!creating}
                />
              </label>
              <label>
                <span className="label-text">Primary domain</span>
                <input
                  className="input input-bordered w-full"
                  name="domain"
                  required
                  defaultValue={application?.domain ?? ""}
                  placeholder="app.example.com"
                />
              </label>
              <label className="form-span">
                <span className="label-text">
                  Aliases <small>comma-separated</small>
                </span>
                <input
                  className="input input-bordered w-full"
                  name="aliases"
                  defaultValue={application?.aliases.join(", ") ?? ""}
                  placeholder="www.example.com, alternate.example.com"
                />
              </label>
            </div>

            <div className="form-section">
              <h3>Runtime</h3>
              <div className="form-grid">
                <label>
                  <span className="label-text">PHP version</span>
                  <select
                    className="select select-bordered w-full"
                    name="phpVersion"
                    required
                    defaultValue={application?.phpVersion ?? settings.defaults?.phpVersion}
                  >
                    {settings.phpVersions.map((version) => (
                      <option key={version} value={version}>
                        PHP {version}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  <span className="label-text">FPM capacity</span>
                  <select
                    className="select select-bordered w-full"
                    name="fpmProfile"
                    required
                    defaultValue={application?.fpmProfile ?? settings.defaults?.fpmProfile}
                  >
                    {settings.fpmProfiles.map((profile) => (
                      <option key={profile} value={profile}>
                        {profile}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  <span className="label-text">Document root</span>
                  <input
                    className="input input-bordered w-full"
                    name="documentRoot"
                    required
                    defaultValue={application?.documentRoot ?? "public"}
                  />
                </label>
                <label>
                  <span className="label-text">Routing mode</span>
                  <select
                    className="select select-bordered w-full"
                    name="entrypointMode"
                    defaultValue={application?.entrypointMode ?? "front-controller"}
                  >
                    <option value="front-controller">Front controller</option>
                    <option value="legacy">Direct PHP files (legacy)</option>
                  </select>
                </label>
              </div>
            </div>

            {creating && (
              <div className="form-section">
                <h3>Initial database binding</h3>
                <label>
                  <span className="label-text">Engine or managed service</span>
                  <select
                    className="select select-bordered w-full"
                    value={database}
                    onChange={(event) => setDatabase(event.target.value)}
                  >
                    {settings.databaseServices.map((service) => (
                      <option
                        key={`${service.engine}:${service.service}`}
                        value={databaseSelection(service.engine, service.service)}
                      >
                        {service.engine === "mysql" ? "MySQL" : "PostgreSQL"} {service.version} (
                        {service.service})
                      </option>
                    ))}
                    <option value="sqlite">SQLite</option>
                    <option value="litestream">SQLite + Litestream</option>
                  </select>
                </label>
                {relationalDatabase && (
                  <div className="conditional-fields">
                    <label className="check-row">
                      <input
                        className="checkbox"
                        type="checkbox"
                        name="createDatabase"
                        defaultChecked={creating}
                      />
                      <span>
                        Create database
                        <small>
                          Existing bindings and durable databases are never removed here.
                        </small>
                      </span>
                    </label>
                    <label>
                      <span className="label-text">Database name</span>
                      <input
                        className="input input-bordered w-full"
                        name="databaseName"
                        defaultValue={creating ? "" : primaryDatabase?.names[0]}
                        placeholder="Defaults to the application slug"
                      />
                    </label>
                  </div>
                )}
              </div>
            )}

            <div className="form-section">
              <h3>TLS and logs</h3>
              <div className="form-grid">
                <label>
                  <span className="label-text">TLS mode</span>
                  <select
                    className="select select-bordered w-full"
                    value={tls}
                    onChange={(event) => setTls(event.target.value as Application["tls"])}
                  >
                    <option value="shared">Shared starter certificate</option>
                    <option value="self-ca">Stack private CA</option>
                    <option value="acme">ACME</option>
                    <option value="external">External certificate</option>
                  </select>
                </label>
                <label className="check-row">
                  <input
                    className="checkbox"
                    type="checkbox"
                    name="accessLog"
                    defaultChecked={application?.accessLog ?? false}
                  />
                  <span>Enable access logs</span>
                </label>
                {tls === "external" && (
                  <>
                    <label>
                      <span className="label-text">Certificate path</span>
                      <input
                        className="input input-bordered w-full"
                        name="tlsCertificatePath"
                        required
                        defaultValue={application?.tlsCertificatePath ?? ""}
                      />
                    </label>
                    <label>
                      <span className="label-text">Private key path</span>
                      <input
                        className="input input-bordered w-full"
                        name="tlsKeyPath"
                        required
                        defaultValue={application?.tlsKeyPath ?? ""}
                      />
                    </label>
                  </>
                )}
              </div>
            </div>
          </fieldset>

          <div className="modal-action">
            <button type="button" className="btn btn-ghost" disabled={saving} onClick={onClose}>
              Cancel
            </button>
            <button className="btn btn-primary" disabled={saving} type="submit">
              {saving && <span className="loading loading-spinner loading-xs" />}
              {creating ? "Create and apply" : "Save and apply"}
            </button>
          </div>
        </form>
      </div>
      <button className="modal-backdrop" aria-label="Close" disabled={saving} onClick={onClose} />
    </dialog>
  );
}

function databaseSelection(engine: string, service?: string): string {
  return service ? `${engine}:${service}` : engine;
}
