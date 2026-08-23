import { useQuery } from "@tanstack/react-query";
import {
  DomainError,
  DomainLoading,
  EmptyPanel,
  StackNotReady,
} from "../../components/DomainState.tsx";
import { orpc } from "../../api/client.ts";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";

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
        <Button variant="outline" disabled={query.isFetching} onClick={() => void query.refetch()}>
          Refresh
        </Button>
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
                  <Table className="min-w-[650px] bg-card">
                    <TableHeader>
                      <TableRow>
                        <TableHead>Job</TableHead>
                        <TableHead>Application</TableHead>
                        <TableHead>Schedule</TableHead>
                        <TableHead>Timezone</TableHead>
                        <TableHead>Command</TableHead>
                        <TableHead>Status</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {data.cronJobs.map((job) => (
                        <TableRow key={`${job.app}:${job.name}`}>
                          <TableCell>
                            <strong>{job.name}</strong>
                          </TableCell>
                          <TableCell>{job.app}</TableCell>
                          <TableCell>
                            <code>{job.schedule}</code>
                          </TableCell>
                          <TableCell>{job.timezone}</TableCell>
                          <TableCell>{job.command}</TableCell>
                          <TableCell>
                            <State enabled={job.enabled} />
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              ) : (
                <EmptyPanel>No scheduled jobs configured.</EmptyPanel>
              )}
            </article>
            <article className="panel full">
              <h2>Workers</h2>
              {data.workers.length ? (
                <div className="table-wrap">
                  <Table className="min-w-[650px] bg-card">
                    <TableHeader>
                      <TableRow>
                        <TableHead>Worker</TableHead>
                        <TableHead>Application</TableHead>
                        <TableHead>Command</TableHead>
                        <TableHead>Restart</TableHead>
                        <TableHead>Stop policy</TableHead>
                        <TableHead>Status</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {data.workers.map((worker) => (
                        <TableRow key={`${worker.app}:${worker.name}`}>
                          <TableCell>
                            <strong>{worker.name}</strong>
                          </TableCell>
                          <TableCell>{worker.app}</TableCell>
                          <TableCell>{worker.command}</TableCell>
                          <TableCell>{worker.autorestart ? "Automatic" : "Manual"}</TableCell>
                          <TableCell>
                            {worker.stopsignal} / {worker.stopwaitsecs}s
                          </TableCell>
                          <TableCell>
                            <State enabled={worker.enabled} />
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              ) : (
                <EmptyPanel>No workers configured.</EmptyPanel>
              )}
            </article>
            <article className="panel full">
              <h2>Deploy orchestration</h2>
              {data.deploys.length ? (
                <div className="table-wrap">
                  <Table className="min-w-[650px] bg-card">
                    <TableHeader>
                      <TableRow>
                        <TableHead>Application</TableHead>
                        <TableHead>Queue</TableHead>
                        <TableHead>Timeout</TableHead>
                        <TableHead>Hook</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {data.deploys.map((deploy) => (
                        <TableRow key={deploy.app}>
                          <TableCell>
                            <strong>{deploy.app}</strong>
                          </TableCell>
                          <TableCell>{deploy.queuePolicy}</TableCell>
                          <TableCell>{deploy.timeoutSec}s</TableCell>
                          <TableCell>{deploy.command}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
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
    <Badge className={enabled ? "bg-emerald-600 text-white" : "bg-amber-500 text-amber-950"}>
      {enabled ? "Enabled" : "Disabled"}
    </Badge>
  );
}
function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
