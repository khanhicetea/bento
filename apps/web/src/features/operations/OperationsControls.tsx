import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, CheckCircle2, FileText, Play, Rocket, RotateCw, Square, Stethoscope, Wrench } from "lucide-react";
import { Link } from "wouter";
import { useState } from "react";
import { orpc } from "../../api/client.ts";
import { ConfirmOperationDialog } from "./ConfirmOperationDialog.tsx";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Spinner } from "@/components/ui/spinner";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

export function OperationsControls({ stackName }: { stackName: string }) {
  const queryClient = useQueryClient();
  const applicationsQuery = useQuery(orpc.applications.list.queryOptions({ input: {} }));
  const applications = applicationsQuery.data?.applications ?? [];
  const [deployApp, setDeployApp] = useState("");
  const [pendingLifecycle, setPendingLifecycle] = useState<"stop" | "restart" | null>(null);
  const [confirmDeploy, setConfirmDeploy] = useState(false);
  const [doctorOpen, setDoctorOpen] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const refresh = () => queryClient.invalidateQueries({ queryKey: orpc.operations.overview.key() });
  const completed = (result: { message: string }) => {
    setNotice(result.message);
    void refresh();
  };
  const stack = useMutation(orpc.operations.stackAction.mutationOptions({ onSuccess: completed }));
  const apply = useMutation(orpc.operations.apply.mutationOptions({ onSuccess: completed }));
  const doctor = useMutation(orpc.operations.doctor.mutationOptions());
  const maintenance = useMutation(orpc.operations.maintenance.mutationOptions({ onSuccess: completed }));
  const deploy = useMutation(orpc.operations.drainDeploy.mutationOptions({ onSuccess: completed }));
  const busy = stack.isPending || apply.isPending || doctor.isPending || maintenance.isPending || deploy.isPending;
  const error = applicationsQuery.error ?? stack.error ?? apply.error ?? maintenance.error ?? deploy.error;

  function lifecycle(action: "start" | "stop" | "restart") {
    setNotice(null);
    if (action === "start") stack.mutate({ action });
    else setPendingLifecycle(action);
  }

  function confirmLifecycle() {
    if (!pendingLifecycle) return;
    stack.mutate({ action: pendingLifecycle, confirmation: stackName }, { onSuccess: () => setPendingLifecycle(null) });
  }

  function drain() {
    if (deployApp.trim()) setConfirmDeploy(true);
  }

  function confirmDrain() {
    const app = deployApp.trim();
    if (!app) return;
    deploy.mutate({ app, confirmation: app }, { onSuccess: () => setConfirmDeploy(false) });
  }

  function runDoctor() {
    setDoctorOpen(true);
    doctor.mutate({});
  }

  return (
    <article className="rounded-2xl border border-border bg-card p-5 text-card-foreground shadow-sm md:p-6">
      <div className="flex items-start gap-3">
        <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-muted text-primary">
          <Wrench className="size-4" aria-hidden="true" />
        </span>
        <div>
          <p className="m-0 text-[0.68rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
            Stack actions
          </p>
          <h3 className="m-0 mt-1 text-lg font-semibold tracking-tight">Operations controls</h3>
          <p className="m-0 mt-1 text-sm text-muted-foreground">
            Mutating actions run directly against this local stack. Destructive actions ask for confirmation.
          </p>
        </div>
      </div>

      {error && (
        <Alert className="mt-5" variant="destructive">
          {messageOf(error)}
        </Alert>
      )}
      {notice && (
        <Alert className="mt-5 border-emerald-600/40 bg-emerald-600/10 text-emerald-700 dark:text-emerald-400">
          <CheckCircle2 aria-hidden="true" />
          {notice}
        </Alert>
      )}

      <div className="mt-6 grid gap-6 lg:grid-cols-[minmax(0,0.95fr)_minmax(0,1.45fr)]">
        <div>
          <p className="m-0 text-sm font-semibold">Stack lifecycle</p>
          <p className="m-0 mt-1 text-xs text-muted-foreground">Start, restart, or stop every service in the stack.</p>
          <div className="mt-3 grid grid-cols-3 gap-2 max-[520px]:grid-cols-1">
            <Button
              className="bg-emerald-600 text-white hover:bg-emerald-600/90"
              disabled={busy}
              onClick={() => lifecycle("start")}
            >
              {stack.isPending ? <Spinner /> : <Play className="size-4" aria-hidden="true" />}
              {stack.isPending ? "Working…" : "Start"}
            </Button>
            <Button variant="outline" disabled={busy} onClick={() => lifecycle("restart")}>
              <RotateCw className="size-4" aria-hidden="true" />
              Restart
            </Button>
            <Button
              variant="outline"
              className="border-destructive text-destructive hover:bg-destructive/10 hover:text-destructive"
              disabled={busy}
              onClick={() => lifecycle("stop")}
            >
              <Square className="size-3.5 fill-current" aria-hidden="true" />
              Stop
            </Button>
          </div>
        </div>

        <div>
          <p className="m-0 text-sm font-semibold">Maintenance</p>
          <p className="m-0 mt-1 text-xs text-muted-foreground">
            Apply configuration, protect data, clean up old artifacts, or run diagnostics.
          </p>
          <div className="mt-3 grid grid-cols-2 gap-2 max-[520px]:grid-cols-1">
            <Button disabled={busy} onClick={() => apply.mutate({})}>
              {apply.isPending ? <Spinner /> : <Wrench className="size-4" aria-hidden="true" />}
              {apply.isPending ? "Applying…" : "Render & apply"}
            </Button>
            <Button variant="outline" asChild>
              <Link href="/backups">
                <Archive className="size-4" aria-hidden="true" />
                Backups
              </Link>
            </Button>
            <Button variant="outline" disabled={busy} onClick={() => maintenance.mutate({ retainDays: 14 })}>
              {maintenance.isPending ? <Spinner /> : <RotateCw className="size-4" aria-hidden="true" />}
              {maintenance.isPending ? "Running…" : "Run maintenance"}
            </Button>
            <Button variant="outline" disabled={busy} onClick={runDoctor}>
              {doctor.isPending ? <Spinner /> : <Stethoscope className="size-4" aria-hidden="true" />}
              {doctor.isPending ? "Running…" : "Doctor"}
            </Button>
          </div>
        </div>
      </div>

      <div className="mt-6 border-t border-border pt-5">
        <div className="flex items-start gap-3 max-[640px]:flex-col">
          <div className="min-w-0 flex-1">
            <label htmlFor="deploy-application" className="text-sm font-semibold">
              Run a queued deploy
            </label>
            <p className="m-0 mt-1 text-xs text-muted-foreground">
              Enter an application slug to drain its next queued deployment.
            </p>
          </div>
          <div className="flex w-full max-w-[28rem] gap-2 max-[640px]:max-w-none">
            <div className="min-w-0 flex-1">
              <label htmlFor="deploy-application" className="sr-only">
                Application
              </label>
              <NativeSelect
                id="deploy-application"
                className="h-10 w-full bg-background"
                aria-label="Application"
                value={deployApp}
                disabled={applicationsQuery.isPending || applications.length === 0}
                onChange={(event) => setDeployApp(event.target.value)}
              >
                <NativeSelectOption value="" disabled>
                  {applicationsQuery.isPending
                    ? "Loading applications…"
                    : applicationsQuery.error
                      ? "Unable to load applications"
                      : applications.length
                        ? "Select an application…"
                        : "No applications available"}
                </NativeSelectOption>
                {applications.map((application) => (
                  <NativeSelectOption key={application.slug} value={application.slug}>
                    {application.slug} · {application.domain}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
            <Button variant="outline" disabled={busy || !deployApp} onClick={drain}>
              <Rocket className="size-4" aria-hidden="true" />
              <span className="max-[480px]:hidden">Run deploy</span>
            </Button>
          </div>
        </div>
      </div>

      <Dialog open={doctorOpen} onOpenChange={setDoctorOpen}>
        <DialogContent className="w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] max-w-[1000px] gap-4 overflow-y-auto p-6 max-[640px]:p-4 sm:!max-w-[1000px]">
          <DialogHeader>
            <DialogTitle>Bento doctor</DialogTitle>
            <DialogDescription>
              Read-only checks for the host, stack configuration, services, and storage.
            </DialogDescription>
          </DialogHeader>
          {doctor.error && <Alert variant="destructive">{messageOf(doctor.error)}</Alert>}
          {doctor.isPending && (
            <div className="flex min-h-48 items-center justify-center gap-3 text-sm text-muted-foreground">
              <Spinner className="size-5" /> Running doctor checks…
            </div>
          )}
          {!doctor.isPending && doctor.data && (
            <div className="space-y-4">
              <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-border bg-muted/30 p-4">
                <div className="flex items-center gap-3">
                  <Badge className={doctor.data.ok ? "bg-emerald-600 text-white" : "bg-destructive text-white"}>
                    {doctor.data.ok ? "Healthy" : "Problems found"}
                  </Badge>
                  <span className="text-xs text-muted-foreground">Generated {formatDate(doctor.data.generatedAt)}</span>
                </div>
                <span className="text-xs text-muted-foreground [overflow-wrap:anywhere]">{doctor.data.stackRoot}</span>
              </div>
              <div className="grid grid-cols-3 gap-3 max-[560px]:grid-cols-1">
                <DoctorSummary
                  label="Passed"
                  value={doctor.data.summary.pass}
                  className="text-emerald-600 dark:text-emerald-400"
                />
                <DoctorSummary
                  label="Warnings"
                  value={doctor.data.summary.warn}
                  className="text-amber-600 dark:text-amber-400"
                />
                <DoctorSummary label="Failed" value={doctor.data.summary.fail} className="text-destructive" />
              </div>
              <div className="max-h-[50vh] space-y-2 overflow-y-auto pr-1" aria-label="Doctor checks">
                {[...doctor.data.checks]
                  .sort((a, b) => doctorStatusRank(a.status) - doctorStatusRank(b.status))
                  .map((check) => {
                    const status = doctorStatus(check.status);
                    return (
                      <div
                        key={`${check.category}/${check.id}`}
                        className="flex items-start gap-3 rounded-xl border border-border p-3"
                      >
                        <Badge className={status.className}>{status.label}</Badge>
                        <div className="min-w-0">
                          <p className="m-0 text-sm font-medium">
                            {check.category} / {check.id}
                          </p>
                          <p className="m-0 mt-1 text-xs text-muted-foreground [overflow-wrap:anywhere]">
                            {check.detail}
                          </p>
                        </div>
                      </div>
                    );
                  })}
              </div>
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" disabled={doctor.isPending} onClick={runDoctor}>
              {doctor.isPending ? <Spinner /> : <Stethoscope className="size-4" aria-hidden="true" />}
              Run again
            </Button>
            <Button variant="ghost" onClick={() => setDoctorOpen(false)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      {pendingLifecycle && (
        <ConfirmOperationDialog
          title={`${pendingLifecycle === "stop" ? "Stop" : "Restart"} stack`}
          description={`This will ${pendingLifecycle} every service in the stack.`}
          confirmation={stackName}
          busy={stack.isPending}
          onConfirm={confirmLifecycle}
          onClose={() => setPendingLifecycle(null)}
        />
      )}
      {confirmDeploy && (
        <ConfirmOperationDialog
          title="Run queued deploy"
          description={`Run the next queued deploy for ${deployApp.trim()}.`}
          confirmation={deployApp.trim()}
          busy={deploy.isPending}
          onConfirm={confirmDrain}
          onClose={() => setConfirmDeploy(false)}
        />
      )}
    </article>
  );
}

function DoctorSummary({ label, value, className }: { label: string; value: number; className: string }) {
  return (
    <div className="rounded-xl border border-border bg-muted/30 p-3">
      <strong className={`block text-xl leading-none ${className}`}>{value}</strong>
      <span className="mt-1 block text-xs text-muted-foreground">{label}</span>
    </div>
  );
}

function doctorStatus(status: "pass" | "warn" | "fail") {
  if (status === "pass") return { label: "PASS", className: "bg-emerald-600 text-white" };
  if (status === "warn") return { label: "WARN", className: "bg-amber-500 text-amber-950" };
  return { label: "FAIL", className: "bg-destructive text-white" };
}

function doctorStatusRank(status: "pass" | "warn" | "fail") {
  if (status === "fail") return 0;
  if (status === "warn") return 1;
  return 2;
}

export function ServiceLogsButton({ service }: { service: string }) {
  const [open, setOpen] = useState(false);
  const logs = useMutation(orpc.operations.logs.mutationOptions());

  function loadLogs() {
    setOpen(true);
    logs.mutate({ service, tail: 200 });
  }

  return (
    <>
      <Button variant="outline" size="sm" disabled={logs.isPending} onClick={loadLogs}>
        {logs.isPending ? <Spinner /> : <FileText className="size-3.5" aria-hidden="true" />}
        Logs
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] max-w-[1200px] gap-4 overflow-y-auto p-6 max-[640px]:p-4 sm:!max-w-[1200px]">
          <DialogHeader>
            <DialogTitle>{service} logs</DialogTitle>
            <DialogDescription>Showing the most recent 200 lines from this service.</DialogDescription>
          </DialogHeader>
          {logs.error && <Alert variant="destructive">{messageOf(logs.error)}</Alert>}
          {logs.isPending && (
            <div className="flex min-h-48 items-center justify-center gap-3 text-sm text-muted-foreground">
              <Spinner className="size-5" /> Loading logs…
            </div>
          )}
          {!logs.isPending && logs.data && (
            <div className="overflow-hidden rounded-xl border border-border bg-muted/30">
              <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-3 text-xs text-muted-foreground">
                <span>{logs.data.lines.length} lines returned</span>
                {logs.data.truncated && <span>Output truncated</span>}
              </div>
              <pre
                className="max-h-[60vh] min-h-48 overflow-auto p-4 text-xs whitespace-pre-wrap break-words text-muted-foreground"
                aria-label={`Recent logs for ${service}`}
              >
                {logs.data.lines.join("\n") || "No log lines returned."}
              </pre>
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" disabled={logs.isPending} onClick={loadLogs}>
              <RotateCw className="size-4" aria-hidden="true" />
              Refresh logs
            </Button>
            <Button variant="ghost" onClick={() => setOpen(false)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

export function ServiceRestartButton({ service }: { service: string }) {
  const queryClient = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const restart = useMutation(
    orpc.operations.restartService.mutationOptions({
      onSuccess() {
        void queryClient.invalidateQueries({ queryKey: orpc.operations.overview.key() });
      },
    }),
  );

  function run() {
    restart.mutate({ service, confirmation: service }, { onSuccess: () => setConfirming(false) });
  }

  return (
    <>
      <Button variant="outline" size="sm" disabled={restart.isPending} onClick={() => setConfirming(true)}>
        {restart.isPending ? <Spinner /> : <RotateCw className="size-3.5" aria-hidden="true" />}
        {restart.isPending ? "Restarting…" : "Restart"}
      </Button>
      {restart.error && <small className="text-destructive">{messageOf(restart.error)}</small>}
      {restart.data && <small className="text-emerald-600 dark:text-emerald-400">Restarted</small>}
      {confirming && (
        <ConfirmOperationDialog
          title={`Restart ${service}`}
          description="Only this Docker Compose service will be restarted."
          confirmation={service}
          busy={restart.isPending}
          onConfirm={run}
          onClose={() => setConfirming(false)}
        />
      )}
    </>
  );
}

function formatDate(value: string | number) {
  return new Date(value).toLocaleString();
}

function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
