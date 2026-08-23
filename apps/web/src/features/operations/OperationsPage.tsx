import { useQuery } from "@tanstack/react-query";
import {
  DomainError,
  DomainLoading,
  EmptyPanel,
  StackNotReady,
} from "../../components/DomainState.tsx";
import { orpc } from "../../api/client.ts";
import { OperationsControls, ServiceRestartButton } from "./OperationsControls.tsx";
import { Button } from "@/components/ui/button";
import { Alert } from "@/components/ui/alert";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";

export function OperationsPage() {
  const query = useQuery(orpc.operations.overview.queryOptions({ input: {} }));
  const data = query.data;
  if (!data && query.isPending)
    return (
      <section className="w-full max-w-[1800px] mx-auto p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
        <DomainLoading label="operations" />
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
          <h2>Operations</h2>
          <p className="m-0 my-1 opacity-60">
            Live best-effort service observations, runtime capacity, and render state.
          </p>
        </div>
        <Button variant="outline" disabled={query.isFetching} onClick={() => void query.refetch()}>
          Refresh status
        </Button>
      </div>
      {!data.initialized ? (
        <StackNotReady stackRoot={data.stackRoot} error={data.error} />
      ) : (
        <>
          <div className="my-[1.2rem] grid grid-cols-4 gap-4 max-[1050px]:grid-cols-2 max-[760px]:grid-cols-1">
            <Metric
              label="Running roles"
              value={data.roles.filter((role) => role.state === "running").length}
            />
            <Metric label="Expected roles" value={data.roles.length} />
            <Metric label="Applications" value={data.counts.applications} />
            <Metric label="Background tasks" value={data.counts.cronJobs + data.counts.workers} />
          </div>
          {(data.warnings.length > 0 || data.notes.length > 0) && (
            <div className="mb-4 grid gap-2.5">
              {data.warnings.map((warning) => (
                <Alert className="border-amber-500/40 bg-amber-500/10" key={warning}>
                  {warning}
                </Alert>
              ))}
              {data.notes.map((note) => (
                <Alert key={note}>{note}</Alert>
              ))}
            </div>
          )}
          <div className="my-[1.2rem] grid grid-cols-4 gap-4 max-[1050px]:grid-cols-2 max-[760px]:grid-cols-1">
            <OperationsControls stackName={data.stackName ?? "bento"} />
            <article className="col-span-2 rounded-xl border border-border bg-card p-5 text-card-foreground [&_h2]:mt-0 [&_h2]:mb-3.5 [&_h3]:mt-0 [&_h3]:mb-2 max-[760px]:col-span-1">
              <h2>Stack</h2>
              <div className="grid">
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
            <article className="col-span-2 rounded-xl border border-border bg-card p-5 text-card-foreground [&_h2]:mt-0 [&_h2]:mb-3.5 [&_h3]:mt-0 [&_h3]:mb-2 max-[760px]:col-span-1">
              <h2>Inventory</h2>
              <div className="grid">
                <Detail label="Applications" value={String(data.counts.applications)} />
                <Detail label="Proxies" value={String(data.counts.proxies)} />
                <Detail label="Cron jobs" value={String(data.counts.cronJobs)} />
                <Detail label="Workers" value={String(data.counts.workers)} />
              </div>
            </article>
            <article className="col-span-full rounded-xl border border-border bg-card p-5 text-card-foreground [&_h2]:mt-0 [&_h2]:mb-3.5 [&_h3]:mt-0 [&_h3]:mb-2 max-[760px]:col-span-1">
              <h2>Service roles</h2>
              {data.roles.length ? (
                <div className="grid grid-cols-3 gap-3 max-[1050px]:grid-cols-2 max-[760px]:grid-cols-1">
                  {data.roles.map((role) => (
                    <div
                      className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-3 gap-y-1 rounded-xl border border-border bg-background p-3.5"
                      key={role.name}
                    >
                      <div>
                        <strong>{role.name}</strong>
                        <small className="block opacity-60">{role.kind}</small>
                      </div>
                      <Badge
                        variant={role.state === "unknown" ? "outline" : "default"}
                        className={
                          role.state === "running"
                            ? "bg-emerald-600 text-white"
                            : role.state === "unknown"
                              ? undefined
                              : "bg-amber-500 text-amber-950"
                        }
                      >
                        {role.state}
                      </Badge>
                      {role.detail && (
                        <p className="col-span-full m-0 mt-1 text-xs opacity-60">{role.detail}</p>
                      )}
                      <div className="col-span-full mt-1 flex items-center gap-2">
                        <ServiceRestartButton service={role.name} />
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <EmptyPanel>No service roles found.</EmptyPanel>
              )}
            </article>
            <article className="col-span-full rounded-xl border border-border bg-card p-5 text-card-foreground [&_h2]:mt-0 [&_h2]:mb-3.5 [&_h3]:mt-0 [&_h3]:mb-2 max-[760px]:col-span-1">
              <h2>PHP runtime capacity</h2>
              {data.runtimes.length ? (
                <div className="overflow-auto rounded-[0.8rem] border border-border">
                  <Table className="min-w-[650px] bg-card">
                    <TableHeader>
                      <TableRow>
                        <TableHead>PHP</TableHead>
                        <TableHead>FPM service</TableHead>
                        <TableHead>Runner</TableHead>
                        <TableHead>Applications</TableHead>
                        <TableHead>Pool capacity</TableHead>
                        <TableHead>Status</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {data.runtimes.map((runtime) => (
                        <TableRow key={runtime.version}>
                          <TableCell>
                            <strong>{runtime.version}</strong>
                          </TableCell>
                          <TableCell>{runtime.service}</TableCell>
                          <TableCell>{runtime.runner}</TableCell>
                          <TableCell>{runtime.appCount}</TableCell>
                          <TableCell>
                            {runtime.poolMaxSum} / {runtime.processCap}
                          </TableCell>
                          <TableCell>
                            <Badge
                              variant={runtime.overCap ? "destructive" : "default"}
                              className={runtime.overCap ? undefined : "bg-emerald-600 text-white"}
                            >
                              {runtime.overCap ? "Over cap" : "Within cap"}
                            </Badge>
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
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
    <div className="rounded-xl border border-border bg-card p-5 text-card-foreground">
      <div className="text-xs uppercase tracking-[0.06em] opacity-60">{label}</div>
      <div className="mt-1 text-3xl font-bold">{value}</div>
    </div>
  );
}
function Detail({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 border-b border-border py-2.5 text-[0.84rem] last:border-b-0">
      <span className="opacity-70">{label}</span>
      <strong className="[overflow-wrap:anywhere] text-right">{value}</strong>
    </div>
  );
}
function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
