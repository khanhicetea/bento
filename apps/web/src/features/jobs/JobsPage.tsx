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
      <section className="w-full max-w-[1800px] mx-auto p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
        <DomainLoading label="jobs and workers" />
      </section>
    );
  if (!data && query.error)
    return (
      <section className="w-full max-w-[1800px] mx-auto p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
        <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
      </section>
    );
  if (!data) return null;
  return (
    <section className="w-full max-w-[1800px] mx-auto p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
      <div className="mb-4 flex items-end justify-between gap-4 max-[760px]:items-stretch max-[760px]:flex-col">
        <div>
          <h2>Jobs and workers</h2>
          <p className="m-0 my-1 opacity-60">
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
          <div className="my-[1.2rem] grid grid-cols-4 gap-4 max-[1050px]:grid-cols-2 max-[760px]:grid-cols-1">
            <Metric label="Cron jobs" value={data.cronJobs.length} />
            <Metric
              label="Enabled jobs"
              value={data.cronJobs.filter((item) => item.enabled).length}
            />
            <Metric label="Workers" value={data.workers.length} />
            <Metric label="Deploy hooks" value={data.deploys.length} />
          </div>
          <div className="my-[1.2rem] grid grid-cols-4 gap-4 max-[1050px]:grid-cols-2 max-[760px]:grid-cols-1">
            <article className="col-span-full rounded-xl border border-border bg-card p-5 text-card-foreground [&_h2]:mt-0 [&_h2]:mb-3.5 [&_h3]:mt-0 [&_h3]:mb-2 max-[760px]:col-span-1">
              <h2>Scheduled jobs</h2>
              {data.cronJobs.length ? (
                <div className="overflow-auto rounded-[0.8rem] border border-border">
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
            <article className="col-span-full rounded-xl border border-border bg-card p-5 text-card-foreground [&_h2]:mt-0 [&_h2]:mb-3.5 [&_h3]:mt-0 [&_h3]:mb-2 max-[760px]:col-span-1">
              <h2>Workers</h2>
              {data.workers.length ? (
                <div className="overflow-auto rounded-[0.8rem] border border-border">
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
            <article className="col-span-full rounded-xl border border-border bg-card p-5 text-card-foreground [&_h2]:mt-0 [&_h2]:mb-3.5 [&_h3]:mt-0 [&_h3]:mb-2 max-[760px]:col-span-1">
              <h2>Deploy orchestration</h2>
              {data.deploys.length ? (
                <div className="overflow-auto rounded-[0.8rem] border border-border">
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
    <div className="rounded-xl border border-border bg-card p-5 text-card-foreground">
      <div className="text-xs uppercase tracking-[0.06em] opacity-60">{label}</div>
      <div className="mt-1 text-3xl font-bold">{value}</div>
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
