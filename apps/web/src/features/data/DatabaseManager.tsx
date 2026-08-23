import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { DataOverview } from "@bento/shared";
import { orpc } from "../../api/client.ts";

type Service = DataOverview["services"][number];

export function DatabaseManager({ service, data }: { service: Service; data: DataOverview }) {
  const client = useQueryClient();
  const [expanded, setExpanded] = useState(false);
  const [artifact, setArtifact] = useState("");
  const [app, setApp] = useState("");
  const [target, setTarget] = useState("");
  const runtime = useQuery(
    orpc.data.runtime.queryOptions({
      input: { service: service.service, engine: service.engine },
    }),
  );
  const backup = useMutation(
    orpc.data.backup.mutationOptions({
      onSuccess: async () => {
        await client.invalidateQueries({ queryKey: orpc.data.overview.key() });
      },
    }),
  );
  const restore = useMutation(
    orpc.data.restore.mutationOptions({
      onSuccess: async () => {
        await client.invalidateQueries({ queryKey: orpc.data.overview.key() });
        await runtime.refetch();
      },
    }),
  );
  const bindings = data.bindings.filter(
    (item) => item.engine === service.engine && item.service === service.service,
  );
  const selectedBinding = bindings.find((item) => item.app === app);
  const backups = data.backups.filter((item) => item.name.startsWith(`${service.service}/`));
  const databases = [...(runtime.data?.databases ?? [])];
  for (const binding of bindings) {
    for (const name of binding.resources) {
      if (!databases.some((database) => database.name === name))
        databases.push({ name, bytes: -1 });
    }
  }
  databases.sort((left, right) => left.name.localeCompare(right.name));

  return (
    <article className="card database-service-card bg-base-100 shadow-sm">
      <div className="card-body">
        <div className="database-service-heading">
          <div>
            <div className="database-service-title">
              <h2 className="card-title">{service.service}</h2>
              <span className="badge badge-outline">{engineLabel(service.engine)}</span>
            </div>
            <p className="muted">
              Version {runtime.data?.serverVersion ?? service.version} · {service.appCount}{" "}
              application{service.appCount === 1 ? "" : "s"}
            </p>
          </div>
          <div className="database-service-actions">
            <button
              className="btn btn-sm btn-ghost"
              disabled={runtime.isFetching}
              onClick={() => void runtime.refetch()}
            >
              {runtime.isFetching && <span className="loading loading-spinner loading-xs" />}
              Refresh sizes
            </button>
            <button
              className={`btn btn-sm ${expanded ? "btn-ghost" : "btn-outline"}`}
              onClick={() => setExpanded((value) => !value)}
              aria-expanded={expanded}
            >
              {expanded ? "Hide details" : "Details & restore"}
            </button>
          </div>
        </div>

        {runtime.error && <div className="alert alert-error">{messageOf(runtime.error)}</div>}
        {runtime.data?.error && <div className="alert alert-warning">{runtime.data.error}</div>}

        <div className="table-wrap database-listing">
          <table className="table">
            <thead>
              <tr>
                <th>Database</th>
                <th>Application</th>
                <th className="text-right">Size</th>
                <th className="database-action-column">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {runtime.isPending && databases.length === 0 ? (
                <tr>
                  <td colSpan={4} className="database-table-state">
                    <span className="loading loading-spinner loading-sm" /> Loading databases and
                    sizes…
                  </td>
                </tr>
              ) : databases.length ? (
                databases.map((database) => {
                  const owner = bindings.find((binding) =>
                    binding.resources.includes(database.name),
                  );
                  const backingUp =
                    backup.isPending && backup.variables?.database === database.name;
                  return (
                    <tr key={database.name}>
                      <td>
                        <strong>{database.name}</strong>
                      </td>
                      <td>{owner?.app ?? <span className="muted">System</span>}</td>
                      <td className="text-right database-size">
                        {database.bytes < 0 ? "Unavailable" : formatBytes(database.bytes)}
                      </td>
                      <td className="database-action-column">
                        <button
                          className="btn btn-sm btn-primary"
                          disabled={!owner || backup.isPending}
                          title={
                            owner
                              ? `Back up ${database.name}`
                              : "Only application databases can be backed up"
                          }
                          onClick={() =>
                            owner &&
                            backup.mutate({
                              app: owner.app,
                              database: database.name,
                              engine: service.engine,
                            })
                          }
                        >
                          {backingUp && <span className="loading loading-spinner loading-xs" />}
                          {backingUp ? "Backing up" : "Backup"}
                        </button>
                      </td>
                    </tr>
                  );
                })
              ) : (
                <tr>
                  <td colSpan={4}>No databases reported by this service.</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        {backup.data && (
          <div className="alert alert-success">
            <span>Backup created: {backup.data.artifacts.map((item) => item.name).join(", ")}</span>
          </div>
        )}
        {backup.error && (
          <div className="alert alert-error">
            <span>{messageOf(backup.error)}</span>
          </div>
        )}

        {expanded && (
          <div className="database-manager-body">
            <section className="card bg-base-200">
              <div className="card-body">
                <h3 className="card-title">Active processes</h3>
                <div className="table-wrap">
                  <table className="table table-sm">
                    <thead>
                      <tr>
                        <th>User / database</th>
                        <th>State</th>
                        <th>Query</th>
                      </tr>
                    </thead>
                    <tbody>
                      {runtime.data?.processes.length ? (
                        runtime.data.processes.map((process) => (
                          <tr key={process.id}>
                            <td>
                              <strong>{process.user}</strong>
                              <small className="database-process-meta">
                                {process.database || `Process ${process.id}`}
                              </small>
                            </td>
                            <td>
                              <span className="badge badge-ghost badge-sm">{process.state}</span>
                            </td>
                            <td>
                              <code>{process.query}</code>
                            </td>
                          </tr>
                        ))
                      ) : (
                        <tr>
                          <td colSpan={3}>No other active processes.</td>
                        </tr>
                      )}
                    </tbody>
                  </table>
                </div>
              </div>
            </section>

            <section className="card border border-base-300">
              <div className="card-body">
                <div>
                  <h3 className="card-title">Restore a backup</h3>
                  <p className="form-help">
                    Restore an artifact to a new or existing application database.
                  </p>
                </div>
                <label className="form-control w-full">
                  <span className="label-text">Backup artifact</span>
                  <select
                    className="select select-bordered w-full"
                    value={artifact}
                    onChange={(event) => setArtifact(event.target.value)}
                  >
                    <option value="">Select an artifact</option>
                    {backups.map((item) => (
                      <option key={item.name} value={item.name}>
                        {item.name} · {formatBytes(item.bytes)}
                      </option>
                    ))}
                  </select>
                </label>
                <div className="database-restore-fields">
                  <label className="form-control w-full">
                    <span className="label-text">Application</span>
                    <select
                      className="select select-bordered w-full"
                      value={app}
                      onChange={(event) => {
                        setApp(event.target.value);
                        setTarget("");
                      }}
                    >
                      <option value="">Select an application</option>
                      {bindings.map((item) => (
                        <option key={item.app} value={item.app}>
                          {item.app}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="form-control w-full">
                    <span className="label-text">Target database</span>
                    <input
                      className="input input-bordered w-full"
                      value={target}
                      onChange={(event) => setTarget(event.target.value)}
                      placeholder={selectedBinding?.resources[0] ?? "database_name"}
                    />
                  </label>
                </div>
                <div className="alert alert-warning">
                  <span>
                    Restoring to an existing name overwrites that database. The target name is used
                    as confirmation.
                  </span>
                </div>
                <div className="card-actions justify-end">
                  <button
                    className="btn btn-warning"
                    disabled={!artifact || !app || !target || restore.isPending}
                    onClick={() =>
                      restore.mutate({
                        app,
                        engine: service.engine,
                        artifact,
                        targetDatabase: target,
                        confirmation: target,
                      })
                    }
                  >
                    {restore.isPending && <span className="loading loading-spinner loading-xs" />}
                    Restore backup
                  </button>
                </div>
                {restore.data && (
                  <div className="alert alert-success">
                    <span>{restore.data.message}</span>
                  </div>
                )}
                {restore.error && (
                  <div className="alert alert-error">
                    <span>{messageOf(restore.error)}</span>
                  </div>
                )}
              </div>
            </section>
          </div>
        )}
      </div>
    </article>
  );
}

function engineLabel(engine: Service["engine"]) {
  return engine === "mysql" ? "MySQL" : "PostgreSQL";
}
function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let value = bytes;
  let unit = -1;
  do {
    value /= 1024;
    unit++;
  } while (value >= 1024 && unit < units.length - 1);
  return `${value.toFixed(1)} ${units[unit]}`;
}
function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
