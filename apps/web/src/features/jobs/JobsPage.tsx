import { useQuery } from "@tanstack/react-query";
import {
  DomainError,
  DomainLoading,
  EmptyPanel,
  StackNotReady,
} from "../../components/DomainState.tsx";
import { orpc } from "../../api/client.ts";

export function JobsPage() {
  const query = useQuery(orpc.jobs.overview.queryOptions({ input: {} }));
  const data = query.data;
  if (!data && query.isPending)
    return (
      <section className="content">
        <DomainLoading label="jobs and workers" />
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
          <h2>Jobs and workers</h2>
          <p>
            Desired scheduler, worker, and deployment orchestration state. Command arguments are
            hidden.
          </p>
        </div>
        <button
          className="btn btn-outline"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          Refresh
        </button>
      </div>
      {!data.initialized ? (
        <StackNotReady stackRoot={data.stackRoot} error={data.error} />
      ) : (
        <>
          <div className="stats-grid">
            <Metric label="Cron jobs" value={data.cronJobs.length} />
            <Metric
              label="Enabled jobs"
              value={data.cronJobs.filter((item) => item.enabled).length}
            />
            <Metric label="Workers" value={data.workers.length} />
            <Metric label="Deploy hooks" value={data.deploys.length} />
          </div>
          <div className="card-grid">
            <article className="panel full">
              <h2>Scheduled jobs</h2>
              {data.cronJobs.length ? (
                <div className="table-wrap">
                  <table className="table">
                    <thead>
                      <tr>
                        <th>Job</th>
                        <th>Application</th>
                        <th>Schedule</th>
                        <th>Timezone</th>
                        <th>Command</th>
                        <th>Status</th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.cronJobs.map((job) => (
                        <tr key={`${job.app}:${job.name}`}>
                          <td>
                            <strong>{job.name}</strong>
                          </td>
                          <td>{job.app}</td>
                          <td>
                            <code>{job.schedule}</code>
                          </td>
                          <td>{job.timezone}</td>
                          <td>{job.command}</td>
                          <td>
                            <State enabled={job.enabled} />
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <EmptyPanel>No scheduled jobs configured.</EmptyPanel>
              )}
            </article>
            <article className="panel full">
              <h2>Workers</h2>
              {data.workers.length ? (
                <div className="table-wrap">
                  <table className="table">
                    <thead>
                      <tr>
                        <th>Worker</th>
                        <th>Application</th>
                        <th>Command</th>
                        <th>Restart</th>
                        <th>Stop policy</th>
                        <th>Status</th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.workers.map((worker) => (
                        <tr key={`${worker.app}:${worker.name}`}>
                          <td>
                            <strong>{worker.name}</strong>
                          </td>
                          <td>{worker.app}</td>
                          <td>{worker.command}</td>
                          <td>{worker.autorestart ? "Automatic" : "Manual"}</td>
                          <td>
                            {worker.stopsignal} / {worker.stopwaitsecs}s
                          </td>
                          <td>
                            <State enabled={worker.enabled} />
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <EmptyPanel>No workers configured.</EmptyPanel>
              )}
            </article>
            <article className="panel full">
              <h2>Deploy orchestration</h2>
              {data.deploys.length ? (
                <div className="table-wrap">
                  <table className="table">
                    <thead>
                      <tr>
                        <th>Application</th>
                        <th>Queue</th>
                        <th>Timeout</th>
                        <th>Hook</th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.deploys.map((deploy) => (
                        <tr key={deploy.app}>
                          <td>
                            <strong>{deploy.app}</strong>
                          </td>
                          <td>{deploy.queuePolicy}</td>
                          <td>{deploy.timeoutSec}s</td>
                          <td>{deploy.command}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <EmptyPanel>No deploy hooks enabled.</EmptyPanel>
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
function State({ enabled }: { enabled: boolean }) {
  return (
    <span className={`badge ${enabled ? "badge-success" : "badge-warning"}`}>
      {enabled ? "Enabled" : "Disabled"}
    </span>
  );
}
function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
