import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import type { JobsOverview } from "@bento/shared";
import {
  CalendarClock,
  Clock3,
  RefreshCw,
  Search,
  Server,
  ServerCog,
  Terminal,
  Webhook,
  X,
} from "lucide-react";
import {
  DomainError,
  DomainLoading,
  EmptyPanel,
  StackNotReady,
} from "../../components/DomainState.tsx";
import { orpc } from "../../api/client.ts";
import { JobLogsButton } from "./JobLogsButton.tsx";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

type JobsView = "all" | "cron" | "workers" | "deploys";

export function JobsPage() {
  const query = useQuery(orpc.jobs.overview.queryOptions({ input: {} }));
  const [search, setSearch] = useState("");
  const [view, setView] = useState<JobsView>("all");
  const [applicationFilter, setApplicationFilter] = useState("all");
  const data = query.data;

  if (!data && query.isPending) {
    return (
      <section className="mx-auto w-full max-w-[1800px] p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
        <DomainLoading label="jobs and workers" />
      </section>
    );
  }
  if (!data && query.error) {
    return (
      <section className="mx-auto w-full max-w-[1800px] p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
        <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
      </section>
    );
  }
  if (!data) return null;

  const normalizedSearch = search.trim().toLowerCase();
  const applications = Array.from(
    new Set([
      ...data.cronJobs.map((job) => job.app),
      ...data.workers.map((worker) => worker.app),
      ...data.deploys.map((deploy) => deploy.app),
    ]),
  ).sort((a, b) => a.localeCompare(b));
  const matchesApplication = (app: string) =>
    applicationFilter === "all" || app === applicationFilter;
  const matches = (...values: string[]) =>
    !normalizedSearch || values.some((value) => value.toLowerCase().includes(normalizedSearch));
  const cronJobs = data.cronJobs.filter(
    (job) =>
      matchesApplication(job.app) &&
      matches(job.name, job.app, job.schedule, job.timezone, job.command),
  );
  const workers = data.workers.filter(
    (worker) =>
      matchesApplication(worker.app) &&
      matches(worker.name, worker.app, worker.command, worker.stopsignal),
  );
  const deploys = data.deploys.filter(
    (deploy) =>
      matchesApplication(deploy.app) && matches(deploy.app, deploy.queuePolicy, deploy.command),
  );
  const applicationCronJobs = data.cronJobs.filter((job) => matchesApplication(job.app));
  const applicationWorkers = data.workers.filter((worker) => matchesApplication(worker.app));
  const applicationDeploys = data.deploys.filter((deploy) => matchesApplication(deploy.app));
  const totalItems =
    applicationCronJobs.length + applicationWorkers.length + applicationDeploys.length;
  const showCron = view === "all" || view === "cron";
  const showWorkers = view === "all" || view === "workers";
  const showDeploys = view === "all" || view === "deploys";
  const visibleItems =
    (showCron ? cronJobs.length : 0) +
    (showWorkers ? workers.length : 0) +
    (showDeploys ? deploys.length : 0);
  const filters = [
    { value: "all", label: "All", count: totalItems },
    { value: "cron", label: "Scheduled", count: applicationCronJobs.length },
    { value: "workers", label: "Workers", count: applicationWorkers.length },
    { value: "deploys", label: "Deploy hooks", count: applicationDeploys.length },
  ] as const;

  return (
    <section
      className="mx-auto w-full max-w-[1800px] p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4"
      aria-live="polite"
    >
      <div className="flex items-end justify-between gap-6 max-[760px]:items-stretch max-[760px]:flex-col">
        <div>
          <p className="mb-2 text-[0.68rem] font-semibold uppercase tracking-[0.16em] text-muted-foreground">
            Control plane / Workloads
          </p>
          <h2 className="m-0 text-[clamp(1.7rem,3vw,2.35rem)] tracking-tight">
            Jobs &amp; workers
          </h2>
          <p className="m-0 mt-2 max-w-[680px] text-sm text-muted-foreground">
            Review schedules, long-running processes, and deployment hooks across your applications.
          </p>
        </div>
        <Button
          className="max-[760px]:w-full"
          variant="outline"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {query.isFetching ? <Spinner /> : <RefreshCw className="size-4" aria-hidden="true" />}
          Refresh inventory
        </Button>
      </div>

      {query.error && (
        <Alert className="mt-6" variant="destructive">
          <span>Unable to refresh the workload inventory: {messageOf(query.error)}</span>
        </Alert>
      )}

      {!data.initialized ? (
        <div className="mt-8">
          <StackNotReady stackRoot={data.stackRoot} error={data.error} />
        </div>
      ) : (
        <>
          <div className="mt-8 grid grid-cols-4 gap-3 max-[1050px]:grid-cols-2 max-[560px]:grid-cols-1">
            <Summary
              value={applicationCronJobs.length}
              label="Scheduled jobs"
              icon={<CalendarClock className="size-4" />}
            />
            <Summary
              value={applicationCronJobs.filter((job) => job.enabled).length}
              label="Enabled schedules"
              icon={<Clock3 className="size-4" />}
              tone="success"
            />
            <Summary
              value={applicationWorkers.length}
              label="Workers"
              icon={<ServerCog className="size-4" />}
            />
            <Summary
              value={applicationDeploys.length}
              label="Deploy hooks"
              icon={<Webhook className="size-4" />}
            />
          </div>

          <div className="mt-8 flex flex-wrap items-center justify-between gap-3">
            <div
              className="flex items-center gap-1 rounded-lg border border-border bg-muted/50 p-1"
              role="group"
              aria-label="Filter workloads by type"
            >
              {filters.map((filter) => (
                <button
                  key={filter.value}
                  type="button"
                  className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${view === filter.value ? "bg-card text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
                  aria-pressed={view === filter.value}
                  onClick={() => setView(filter.value)}
                >
                  {filter.label}
                  <span className="ml-1.5 opacity-60">{filter.count}</span>
                </button>
              ))}
            </div>
            <div className="flex h-[2.875rem] min-w-[min(100%,360px)] flex-1 items-center gap-2 rounded-xl border border-border bg-card px-3 py-1.5 shadow-sm focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/20 max-[760px]:min-w-full">
              <Search className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
              <Input
                className="h-8 min-w-0 flex-1 border-0 bg-transparent px-1 shadow-none focus-visible:ring-0"
                type="search"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder="Search jobs, workers, or applications…"
                aria-label="Search jobs and workers"
              />
              {search && (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  className="shrink-0 rounded-full"
                  aria-label="Clear workload search"
                  onClick={() => setSearch("")}
                >
                  <X />
                </Button>
              )}
            </div>
            <div className="min-w-[min(100%,220px)] max-[760px]:min-w-full">
              <label htmlFor="jobs-application-filter" className="sr-only">
                Filter by application
              </label>
              <NativeSelect
                id="jobs-application-filter"
                className="h-[2.875rem] w-full bg-card"
                value={applicationFilter}
                onChange={(event) => setApplicationFilter(event.target.value)}
              >
                <NativeSelectOption value="all">All applications</NativeSelectOption>
                {applications.map((application) => (
                  <NativeSelectOption key={application} value={application}>
                    {application}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
          </div>
          {visibleItems === 0 && normalizedSearch ? (
            <NoMatches onClear={() => setSearch("")} />
          ) : (
            <div>
              {showCron && (
                <WorkloadSection
                  id="scheduled-jobs"
                  eyebrow="Schedules"
                  title="Scheduled jobs"
                  description="Commands that run on a recurring schedule."
                  count={cronJobs.length}
                  itemLabel="job"
                  icon={<CalendarClock className="size-4" />}
                >
                  {cronJobs.length ? (
                    <ScheduledJobsTable jobs={cronJobs} />
                  ) : (
                    <CollectionEmpty>
                      {normalizedSearch
                        ? "No scheduled jobs match your search."
                        : "No scheduled jobs configured."}
                    </CollectionEmpty>
                  )}
                </WorkloadSection>
              )}

              {showWorkers && (
                <WorkloadSection
                  id="workers"
                  eyebrow="Processes"
                  title="Workers"
                  description="Long-running processes supervised for your applications."
                  count={workers.length}
                  itemLabel="worker"
                  icon={<ServerCog className="size-4" />}
                >
                  {workers.length ? (
                    workers.map((worker) => (
                      <WorkerCard key={`${worker.app}:${worker.name}`} worker={worker} />
                    ))
                  ) : (
                    <CollectionEmpty>
                      {normalizedSearch
                        ? "No workers match your search."
                        : "No workers configured."}
                    </CollectionEmpty>
                  )}
                </WorkloadSection>
              )}

              {showDeploys && (
                <WorkloadSection
                  id="deploy-hooks"
                  eyebrow="Delivery"
                  title="Deploy hooks"
                  description="Application deployment orchestration and queue policy."
                  count={deploys.length}
                  itemLabel="hook"
                  icon={<Webhook className="size-4" />}
                >
                  {deploys.length ? (
                    deploys.map((deploy) => <DeployCard key={deploy.app} deploy={deploy} />)
                  ) : (
                    <CollectionEmpty>
                      {normalizedSearch
                        ? "No deploy hooks match your search."
                        : "No deploy hooks enabled."}
                    </CollectionEmpty>
                  )}
                </WorkloadSection>
              )}
            </div>
          )}
        </>
      )}
    </section>
  );
}

function Summary({
  value,
  label,
  icon,
  tone = "default",
}: {
  value: number;
  label: string;
  icon: ReactNode;
  tone?: "default" | "success";
}) {
  return (
    <div className="flex items-center gap-3 rounded-xl border border-border bg-card px-4 py-3 text-card-foreground shadow-sm">
      <span
        className={`grid size-9 shrink-0 place-items-center rounded-lg ${tone === "success" ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400" : "bg-muted text-muted-foreground"}`}
      >
        {icon}
      </span>
      <span className="min-w-0">
        <strong className="block text-xl leading-none tracking-tight">{value}</strong>
        <span className="mt-1 block truncate text-xs text-muted-foreground">{label}</span>
      </span>
    </div>
  );
}

function WorkloadSection({
  id,
  eyebrow,
  title,
  description,
  count,
  itemLabel,
  icon,
  children,
}: {
  id: string;
  eyebrow: string;
  title: string;
  description: string;
  count: number;
  itemLabel: string;
  icon: ReactNode;
  children: ReactNode;
}) {
  return (
    <section aria-labelledby={id}>
      <div className="mt-10 flex items-end justify-between gap-4 max-[760px]:items-start">
        <div className="flex min-w-0 items-start gap-3">
          <span className="mt-1 grid size-9 shrink-0 place-items-center rounded-xl bg-muted text-muted-foreground">
            {icon}
          </span>
          <div className="min-w-0">
            <p className="m-0 text-[0.68rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
              {eyebrow}
            </p>
            <h3 id={id} className="m-0 mt-1 text-lg font-semibold tracking-tight">
              {title}
            </h3>
            <p className="m-0 mt-1 text-sm text-muted-foreground">{description}</p>
          </div>
        </div>
        <Badge variant="secondary" className="shrink-0">
          {count} {count === 1 ? itemLabel : `${itemLabel}s`}
        </Badge>
      </div>
      <div className="mt-4 grid grid-cols-2 items-stretch gap-5 max-[900px]:grid-cols-1">
        {children}
      </div>
    </section>
  );
}

function ScheduledJobsTable({ jobs }: { jobs: JobItem<"cronJobs">[] }) {
  return (
    <div className="col-span-full overflow-hidden rounded-2xl border border-border bg-card text-card-foreground shadow-sm">
      <Table className="min-w-[800px] bg-card">
        <TableHeader>
          <TableRow>
            <TableHead>Job</TableHead>
            <TableHead>Schedule</TableHead>
            <TableHead>Command</TableHead>
            <TableHead>Status</TableHead>
            <TableHead className="text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        {groupByApplication(jobs).map(([application, applicationJobs]) => (
          <TableBody key={application}>
            <TableRow className="bg-muted/40 hover:bg-muted/40">
              <TableCell colSpan={5} className="px-4 py-3">
                <div className="flex items-center gap-2">
                  <span className="grid size-7 shrink-0 place-items-center rounded-lg bg-muted text-primary">
                    <Server className="size-3.5" aria-hidden="true" />
                  </span>
                  <strong className="text-sm">{application}</strong>
                  <Badge variant="secondary">
                    {applicationJobs.length} {applicationJobs.length === 1 ? "job" : "jobs"}
                  </Badge>
                </div>
              </TableCell>
            </TableRow>
            {applicationJobs.map((job) => (
              <TableRow key={`${job.app}:${job.name}`}>
                <TableCell>
                  <div className="min-w-[18rem]">
                    <strong className="block text-sm">{job.name}</strong>
                    <div className="mt-1.5 flex flex-wrap gap-1.5">
                      {job.timeoutSec !== undefined && (
                        <Badge variant="outline">Timeout {job.timeoutSec}s</Badge>
                      )}
                      <Badge variant="outline">
                        {job.commandMode === "argv" ? "Argument list" : "Shell"}
                      </Badge>
                      <Badge variant="outline">{formatLabel(job.output)}</Badge>
                    </div>
                  </div>
                </TableCell>
                <TableCell>
                  <div className="min-w-[8rem]">
                    <span className="mb-1 block text-xs text-muted-foreground">{job.timezone}</span>
                    <code className="rounded-md bg-muted px-2 py-1 text-xs">{job.schedule}</code>
                  </div>
                </TableCell>
                <TableCell>
                  <code className="block max-w-[28rem] truncate text-xs" title={job.command}>
                    {job.command}
                  </code>
                </TableCell>
                <TableCell>
                  <State enabled={job.enabled} />
                </TableCell>
                <TableCell className="text-right">
                  <JobLogsButton app={job.app} name={job.name} kind="cron" />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        ))}
      </Table>
    </div>
  );
}

function groupByApplication(jobs: JobItem<"cronJobs">[]) {
  const groups = new Map<string, JobItem<"cronJobs">[]>();
  for (const job of jobs) {
    const applicationJobs = groups.get(job.app) ?? [];
    applicationJobs.push(job);
    groups.set(job.app, applicationJobs);
  }
  return Array.from(groups.entries());
}

function WorkerCard({ worker }: { worker: JobItem<"workers"> }) {
  return (
    <article className="flex min-w-0 flex-col rounded-2xl border border-border bg-card p-5 text-card-foreground shadow-sm">
      <ItemHeader
        icon={<ServerCog className="size-4" />}
        title={worker.name}
        app={worker.app}
        enabled={worker.enabled}
      />
      <CommandBlock command={worker.command} />
      <div className="mt-4 flex flex-wrap gap-1.5">
        <Badge variant="outline" className="gap-1.5">
          <RefreshCw className="size-3" aria-hidden="true" />
          {worker.autorestart ? "Automatic restart" : "Manual restart"}
        </Badge>
        <Badge variant="outline">
          Stop {worker.stopsignal} · {worker.stopwaitsecs}s
        </Badge>
      </div>
      <div className="mt-4 flex justify-end border-t border-border pt-4">
        <JobLogsButton app={worker.app} name={worker.name} kind="worker" />
      </div>
    </article>
  );
}

function DeployCard({ deploy }: { deploy: JobItem<"deploys"> }) {
  return (
    <article className="flex min-w-0 flex-col rounded-2xl border border-border bg-card p-5 text-card-foreground shadow-sm">
      <ItemHeader
        icon={<Webhook className="size-4" />}
        title={deploy.app}
        app="Application"
        enabled={deploy.enabled}
      />
      <div className="mt-4 grid grid-cols-2 divide-x divide-border overflow-hidden rounded-xl border border-border bg-muted/30">
        <Fact label="Queue policy" value={formatLabel(deploy.queuePolicy)} />
        <Fact label="Timeout" value={`${deploy.timeoutSec}s`} />
      </div>
      <div className="mt-4">
        <p className="m-0 flex items-center gap-1.5 text-[0.68rem] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
          <Terminal className="size-3.5" aria-hidden="true" /> Hook command
        </p>
        <code className="mt-2 block overflow-auto whitespace-pre-wrap break-words rounded-xl border border-border bg-muted/30 p-3 text-xs leading-relaxed">
          {deploy.command}
        </code>
      </div>
    </article>
  );
}

function ItemHeader({
  icon,
  title,
  app,
  enabled,
}: {
  icon: ReactNode;
  title: string;
  app: string;
  enabled: boolean;
}) {
  return (
    <div className="flex items-start justify-between gap-3">
      <div className="flex min-w-0 items-center gap-3">
        <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-muted text-primary">
          {icon}
        </span>
        <div className="min-w-0">
          <strong className="block truncate text-sm font-semibold" title={title}>
            {title}
          </strong>
          <span className="mt-1 flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
            <Server className="size-3.5 shrink-0" aria-hidden="true" />
            <span className="truncate">{app}</span>
          </span>
        </div>
      </div>
      <State enabled={enabled} />
    </div>
  );
}

function CommandBlock({ command }: { command: string }) {
  return (
    <div className="mt-4">
      <p className="m-0 flex items-center gap-1.5 text-[0.68rem] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
        <Terminal className="size-3.5" aria-hidden="true" /> Command
      </p>
      <code className="mt-2 block max-h-28 overflow-auto whitespace-pre-wrap break-words rounded-xl border border-border bg-muted/30 p-3 text-xs leading-relaxed">
        {command}
      </code>
    </div>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 p-3">
      <span className="block text-[0.68rem] text-muted-foreground">{label}</span>
      <strong className="mt-1 block truncate text-sm font-medium" title={value}>
        {value}
      </strong>
    </div>
  );
}

function CollectionEmpty({ children }: { children: string }) {
  return (
    <div className="col-span-full rounded-2xl border border-dashed border-border bg-card shadow-sm">
      <EmptyPanel>{children}</EmptyPanel>
    </div>
  );
}

function NoMatches({ onClear }: { onClear: () => void }) {
  return (
    <div className="mt-6 rounded-2xl border border-dashed border-border bg-card px-6 py-16 text-center shadow-sm">
      <div className="mx-auto grid size-12 place-items-center rounded-2xl bg-muted text-muted-foreground">
        <Search className="size-5" aria-hidden="true" />
      </div>
      <h3 className="mb-2 mt-4 text-lg font-semibold">No matching workloads</h3>
      <p className="mx-auto mb-5 max-w-md text-sm text-muted-foreground">
        Try a different search term or clear the search to see all configured jobs and workers.
      </p>
      <Button variant="outline" onClick={onClear}>
        Clear search
      </Button>
    </div>
  );
}

function State({ enabled }: { enabled: boolean }) {
  return (
    <Badge className={enabled ? "bg-emerald-600 text-white" : "bg-amber-500 text-amber-950"}>
      <span
        className={`size-1.5 rounded-full ${enabled ? "bg-emerald-200" : "bg-amber-800/60"}`}
        aria-hidden="true"
      />
      {enabled ? "Enabled" : "Disabled"}
    </Badge>
  );
}

function formatLabel(value: string) {
  return value
    .split("-")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}

type JobItem<Key extends "cronJobs" | "workers" | "deploys"> = JobsOverview[Key][number];
