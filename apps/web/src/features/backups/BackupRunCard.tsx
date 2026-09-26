import { useQuery } from "@tanstack/react-query";
import { CalendarClock, RefreshCw } from "lucide-react";
import { orpc } from "../../api/client.ts";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";

/** Host-scheduled backup evidence; separate from on-demand web runs. */
export function BackupRunCard() {
  const query = useQuery(orpc.operations.backupRunStatus.queryOptions({ input: {}, refetchInterval: 10_000 }));
  const run = query.data?.lastRun;
  const operation = query.data?.lastOperation;

  return (
    <section
      className="flex flex-col rounded-2xl border border-border bg-card p-5 shadow-sm sm:p-6"
      aria-labelledby="backup-run-title"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-start gap-3">
          <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-muted text-primary">
            <CalendarClock className="size-5" aria-hidden="true" />
          </span>
          <div>
            <p className="m-0 text-[0.68rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
              Automation
            </p>
            <h3 id="backup-run-title" className="m-0 mt-1 text-lg font-semibold tracking-tight">
              Scheduled backup
            </h3>
            <p className="m-0 mt-1 text-sm text-muted-foreground">
              Last recorded host-scheduled attempt. Separate from on-demand runs.
            </p>
          </div>
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Refresh scheduled backup status"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {query.isFetching ? <Spinner /> : <RefreshCw className="size-4" aria-hidden="true" />}
        </Button>
      </div>
      {query.error && (
        <Alert className="mt-4" variant="destructive">
          Scheduled status may be out of date: {query.error.message}
        </Alert>
      )}
      {query.isPending && <p className="mt-5 text-sm text-muted-foreground">Loading scheduled status…</p>}
      {!query.isPending && !run && !query.error && (
        <div className="mt-5 rounded-xl border border-dashed border-border bg-muted/20 p-4 text-sm text-muted-foreground">
          No scheduled backup attempt recorded yet. This does not indicate whether a schedule is configured.
        </div>
      )}
      {run && (
        <div className="mt-auto pt-5" aria-live="polite">
          <div className="flex flex-wrap items-center gap-2">
            <Badge className={statusClass(run.status)}>{statusLabel(run.status)}</Badge>
            <span className="text-xs text-muted-foreground">Started {formatDate(run.startedAt)}</span>
          </div>
          {run.operationId && (
            <code className="mt-2 block break-all text-xs text-muted-foreground">{run.operationId}</code>
          )}
          <div className="mt-4 grid grid-cols-2 gap-2 rounded-xl border border-border bg-muted/30 p-3">
            <div>
              <strong className="block text-lg leading-none">{run.artifactCount}</strong>
              <span className="mt-1 block text-xs text-muted-foreground">Local artifacts</span>
            </div>
            <div>
              <strong className="block text-lg leading-none">{formatBytes(run.artifactBytes)}</strong>
              <span className="mt-1 block text-xs text-muted-foreground">Produced in this run</span>
            </div>
          </div>
          {run.finishedAt && (
            <p className="mb-0 mt-3 text-xs text-muted-foreground">Finished {formatDate(run.finishedAt)}</p>
          )}
          {operation && operation.steps.length > 0 && (
            <div className="mt-3 flex flex-wrap gap-2 text-xs" aria-label="Scheduled backup steps">
              {operation.steps.map((step) => (
                <div key={step.name} className="rounded-lg border border-border px-3 py-2">
                  <span className="font-medium">{step.name === "backup" ? "Local backup" : "Remote upload"}</span>
                  <span className="ml-2 text-muted-foreground">{statusLabel(step.status)}</span>
                  {step.error && <p className="m-0 mt-1 break-words text-destructive">{step.error}</p>}
                </div>
              ))}
            </div>
          )}
          {run.error && (
            <Alert className="mt-3" variant="destructive">
              {run.error}
            </Alert>
          )}
          <p className="m-0 mt-3 text-xs text-muted-foreground">
            Upload success only confirms transport, not remote retention or a working restore.
          </p>
        </div>
      )}
    </section>
  );
}

function statusClass(status: "running" | "succeeded" | "failed" | "interrupted") {
  if (status === "succeeded") return "bg-emerald-600 text-white";
  if (status === "running") return "bg-amber-500 text-amber-950";
  return "bg-destructive text-white";
}

function statusLabel(status: string) {
  return status.charAt(0).toUpperCase() + status.slice(1);
}

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

function formatDate(value: string) {
  return new Date(value).toLocaleString();
}
