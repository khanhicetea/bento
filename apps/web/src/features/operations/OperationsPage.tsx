import { useQuery } from "@tanstack/react-query";
import {
  DomainError,
  DomainLoading,
  EmptyPanel,
  StackNotReady,
} from "../../components/DomainState.tsx";
import { orpc } from "../../api/client.ts";
import { OperationsControls, ServiceRestartButton } from "./OperationsControls.tsx";

export function OperationsPage() {
  const query = useQuery(orpc.operations.overview.queryOptions({ input: {} }));
  const data = query.data;
  if (!data && query.isPending)
    return (
      <section className="content">
        <DomainLoading label="operations" />
      </section>
    );
  if (!data && query.error)
    return (
      <section className="content">
        <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
      </section>
    );
  if (!data) return null;
  return (
    <section className="content">
      <div className="section-head">
        <div>
          <h2>Operations</h2>
          <p>Live best-effort service observations, runtime capacity, and render state.</p>
        </div>
        <button
          className="btn btn-outline"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          Refresh status
        </button>
      </div>
      {!data.initialized ? (
        <StackNotReady stackRoot={data.stackRoot} error={data.error} />
      ) : (
        <>
          <div className="stats-grid">
            <Metric
              label="Running roles"
              value={data.roles.filter((role) => role.state === "running").length}
            />
            <Metric label="Expected roles" value={data.roles.length} />
            <Metric label="Applications" value={data.counts.applications} />
            <Metric label="Background tasks" value={data.counts.cronJobs + data.counts.workers} />
          </div>
          {(data.warnings.length > 0 || data.notes.length > 0) && (
            <div className="operations-messages">
              {data.warnings.map((warning) => (
                <div className="alert alert-warning" key={warning}>
                  {warning}
                </div>
              ))}
              {data.notes.map((note) => (
                <div className="alert" key={note}>
                  {note}
                </div>
              ))}
            </div>
          )}
          <div className="card-grid">
            <OperationsControls stackName={data.stackName ?? "bento"} />
            <article className="panel">
              <h2>Stack</h2>
              <div className="detail-list">
                <Detail label="Project" value={data.stackName ?? "Unknown"} />
                <Detail label="Stack root" value={data.stackRoot} />
                <Detail
                  label="Last render"
                  value={
                    data.generation?.renderedAt
                      ? new Date(data.generation.renderedAt).toLocaleString()
                      : "Not rendered"
                  }
                />
                <Detail label="Asset version" value={data.generation?.assetVersion ?? "Unknown"} />
              </div>
            </article>
            <article className="panel">
              <h2>Inventory</h2>
              <div className="detail-list">
                <Detail label="Applications" value={String(data.counts.applications)} />
                <Detail label="Proxies" value={String(data.counts.proxies)} />
                <Detail label="Cron jobs" value={String(data.counts.cronJobs)} />
                <Detail label="Workers" value={String(data.counts.workers)} />
              </div>
            </article>
            <article className="panel full">
              <h2>Service roles</h2>
              {data.roles.length ? (
                <div className="role-grid">
                  {data.roles.map((role) => (
                    <div className="role-card" key={role.name}>
                      <div>
                        <strong>{role.name}</strong>
                        <small>{role.kind}</small>
                      </div>
                      <span
                        className={`badge ${role.state === "running" ? "badge-success" : role.state === "unknown" ? "badge-outline" : "badge-warning"}`}
                      >
                        {role.state}
                      </span>
                      {role.detail && <p>{role.detail}</p>}
                      <div className="role-actions">
                        <ServiceRestartButton service={role.name} />
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <EmptyPanel>No service roles found.</EmptyPanel>
              )}
            </article>
            <article className="panel full">
              <h2>PHP runtime capacity</h2>
              {data.runtimes.length ? (
                <div className="table-wrap">
                  <table className="table">
                    <thead>
                      <tr>
                        <th>PHP</th>
                        <th>FPM service</th>
                        <th>Runner</th>
                        <th>Applications</th>
                        <th>Pool capacity</th>
                        <th>Status</th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.runtimes.map((runtime) => (
                        <tr key={runtime.version}>
                          <td>
                            <strong>{runtime.version}</strong>
                          </td>
                          <td>{runtime.service}</td>
                          <td>{runtime.runner}</td>
                          <td>{runtime.appCount}</td>
                          <td>
                            {runtime.poolMaxSum} / {runtime.processCap}
                          </td>
                          <td>
                            <span
                              className={`badge ${runtime.overCap ? "badge-error" : "badge-success"}`}
                            >
                              {runtime.overCap ? "Over cap" : "Within cap"}
                            </span>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <EmptyPanel>No PHP runtimes configured.</EmptyPanel>
              )}
            </article>
          </div>
        </>
      )}
    </section>
  );
}
function Metric({ label, value }: { label: string; value: number }) {
  return (
    <div className="metric">
      <div className="metric-label">{label}</div>
      <div className="metric-value">{value}</div>
    </div>
  );
}
function Detail({ label, value }: { label: string; value: string }) {
  return (
    <div className="detail-row">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}
function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
