import { useQuery } from "@tanstack/react-query";
import {
  DomainError,
  DomainLoading,
  EmptyPanel,
  StackNotReady,
} from "../../components/DomainState.tsx";
import { orpc } from "../../api/client.ts";

export function DataPage() {
  const query = useQuery(orpc.data.overview.queryOptions({ input: {} }));
  const data = query.data;
  if (!data && query.isPending)
    return (
      <section className="content">
        <DomainLoading label="data and runtimes" />
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
      <PageHead
        title="Data and runtimes"
        description="Managed database services, application bindings, and SQLite replication policy."
        refresh={() => void query.refetch()}
        refreshing={query.isFetching}
      />
      {!data.initialized ? (
        <StackNotReady stackRoot={data.stackRoot} error={data.error} />
      ) : (
        <>
          <div className="stats-grid">
            <Metric label="Managed services" value={data.services.length} />
            <Metric label="App bindings" value={data.bindings.length} />
            <Metric
              label="Relational"
              value={
                data.bindings.filter(
                  (item) => item.engine === "mysql" || item.engine === "postgres",
                ).length
              }
            />
            <Metric
              label="SQLite files"
              value={
                data.bindings.filter(
                  (item) => item.engine === "sqlite" || item.engine === "litestream",
                ).length
              }
            />
          </div>
          <div className="card-grid">
            <article className="panel full">
              <h2>Managed database services</h2>
              {data.services.length ? (
                <div className="table-wrap">
                  <table className="table">
                    <thead>
                      <tr>
                        <th>Service</th>
                        <th>Engine</th>
                        <th>Version</th>
                        <th>Applications</th>
                        <th>Volume</th>
                        <th>Image</th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.services.map((service) => (
                        <tr key={service.service}>
                          <td>
                            <strong>{service.service}</strong>
                          </td>
                          <td>{service.engine}</td>
                          <td>{service.version}</td>
                          <td>{service.appCount}</td>
                          <td>
                            <code>{service.volume}</code>
                          </td>
                          <td>
                            <code>{service.image}</code>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <EmptyPanel>No managed database services.</EmptyPanel>
              )}
            </article>
            <article className="panel full">
              <h2>Application bindings</h2>
              {data.bindings.length ? (
                <div className="table-wrap">
                  <table className="table">
                    <thead>
                      <tr>
                        <th>Application</th>
                        <th>Engine</th>
                        <th>Service</th>
                        <th>Resources</th>
                        <th>Recovery</th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.bindings.map((binding, index) => (
                        <tr key={`${binding.app}:${binding.engine}:${index}`}>
                          <td>
                            <strong>{binding.app}</strong>{" "}
                            {binding.primary && (
                              <span className="badge badge-primary">primary</span>
                            )}
                          </td>
                          <td>{binding.engine}</td>
                          <td>{binding.service}</td>
                          <td>{binding.resources.length ? binding.resources.join(", ") : "—"}</td>
                          <td>
                            {binding.engine === "litestream"
                              ? binding.backupVerifiedAt
                                ? `Verified ${formatDate(binding.backupVerifiedAt)}`
                                : "Not verified"
                              : "Logical backup"}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <EmptyPanel>No application database bindings.</EmptyPanel>
              )}
            </article>
            <article className="panel full">
              <h2>Litestream policy</h2>
              {data.sqliteBackup ? (
                <div className="detail-list">
                  <Detail
                    label="Status"
                    value={data.sqliteBackup.enabled ? "Enabled" : "Disabled"}
                  />
                  <Detail label="Destination" value={data.sqliteBackup.destination} />
                  <Detail label="Sync interval" value={data.sqliteBackup.syncInterval} />
                  <Detail
                    label="Snapshots"
                    value={`${data.sqliteBackup.snapshotInterval} · retain ${data.sqliteBackup.snapshotRetention}`}
                  />
                </div>
              ) : (
                <EmptyPanel>Litestream is not configured.</EmptyPanel>
              )}
            </article>
          </div>
        </>
      )}
    </section>
  );
}

function PageHead({
  title,
  description,
  refresh,
  refreshing,
}: {
  title: string;
  description: string;
  refresh: () => void;
  refreshing: boolean;
}) {
  return (
    <div className="section-head">
      <div>
        <h2>{title}</h2>
        <p>{description}</p>
      </div>
      <button className="btn btn-outline" disabled={refreshing} onClick={refresh}>
        Refresh
      </button>
    </div>
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
function formatDate(value: string) {
  return new Date(value).toLocaleString();
}
function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
