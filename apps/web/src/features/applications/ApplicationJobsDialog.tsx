import { useState, type FormEvent, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { AddCronJobInput, Application, JobsOverview } from "@bento/shared";
import { Clock, Plus, RefreshCw, Terminal, Trash2, Wrench } from "lucide-react";
import { orpc } from "../../api/client.ts";
import { JobLogsButton } from "../jobs/JobLogsButton.tsx";
import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Alert } from "@/components/ui/alert";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";

type ApplicationJobsDialogProps = {
  application: Application;
  onClose: () => void;
};

export function ApplicationJobsDialog({ application, onClose }: ApplicationJobsDialogProps) {
  const queryClient = useQueryClient();
  const overviewKey = orpc.jobs.overview.queryKey({ input: {} });
  const overview = useQuery(orpc.jobs.overview.queryOptions({ input: {} }));
  const [commandMode, setCommandMode] = useState<AddCronJobInput["commandMode"]>("argv");
  const updateOverview = (updated: JobsOverview) => queryClient.setQueryData(overviewKey, updated);
  const addCron = useMutation(orpc.jobs.addCron.mutationOptions({ onSuccess: updateOverview }));
  const removeCron = useMutation(
    orpc.jobs.removeCron.mutationOptions({ onSuccess: updateOverview }),
  );
  const addWorker = useMutation(orpc.jobs.addWorker.mutationOptions({ onSuccess: updateOverview }));
  const removeWorker = useMutation(
    orpc.jobs.removeWorker.mutationOptions({ onSuccess: updateOverview }),
  );
  const data = overview.data;
  const cronJobs = (data?.cronJobs ?? []).filter((job) => job.app === application.slug);
  const workers = (data?.workers ?? []).filter((worker) => worker.app === application.slug);
  const mutationError = addCron.error ?? removeCron.error ?? addWorker.error ?? removeWorker.error;
  const error = overview.error ?? mutationError ?? data?.error;
  const busy =
    addCron.isPending || removeCron.isPending || addWorker.isPending || removeWorker.isPending;

  async function submitCron(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const rawCommand = String(form.get("command") ?? "").trim();
    const timeout = String(form.get("timeoutSec") ?? "").trim();
    const command =
      commandMode === "shell"
        ? [rawCommand]
        : rawCommand
            .split("\n")
            .map((item) => item.trim())
            .filter(Boolean);
    try {
      await addCron.mutateAsync({
        app: application.slug,
        name: String(form.get("name") ?? "").trim(),
        schedule: String(form.get("schedule") ?? "").trim(),
        timezone: String(form.get("timezone") ?? "").trim(),
        command,
        commandMode,
        output: String(form.get("output") ?? "log") as AddCronJobInput["output"],
        ...(timeout ? { timeoutSec: Number(timeout) } : {}),
      });
      formElement.reset();
      setCommandMode("argv");
    } catch {
      // The mutation error is rendered in this dialog.
    }
  }

  async function submitWorker(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const command = String(form.get("command") ?? "")
      .split("\n")
      .map((item) => item.trim())
      .filter(Boolean);
    try {
      await addWorker.mutateAsync({
        app: application.slug,
        name: String(form.get("name") ?? "").trim(),
        command,
        autorestart: form.get("autorestart") === "on",
        stopsignal: String(form.get("stopsignal") ?? "TERM").trim(),
        stopwaitsecs: Number(form.get("stopwaitsecs") ?? 10),
      });
      formElement.reset();
    } catch {
      // The mutation error is rendered in this dialog.
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent
        className="w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] max-w-[1200px] gap-0 overflow-y-auto p-0 sm:!max-w-[1200px]"
        showCloseButton={!busy}
      >
        <div className="border-b border-border bg-muted/30 px-8 py-6 pr-16 max-[700px]:px-4 max-[700px]:py-5">
          <DialogHeader className="gap-3">
            <div className="flex items-center gap-3">
              <span className="grid size-11 shrink-0 place-items-center rounded-xl bg-primary/10 text-primary">
                <Terminal className="size-5" aria-hidden="true" />
              </span>
              <div className="min-w-0">
                <DialogTitle className="text-xl">Jobs and workers</DialogTitle>
                <DialogDescription className="mt-1">
                  Schedule commands and run long-lived processes for {application.slug}.
                </DialogDescription>
              </div>
            </div>
          </DialogHeader>
        </div>

        <div className="space-y-5 px-8 py-6 max-[700px]:space-y-4 max-[700px]:px-4 max-[700px]:py-5">
          {error && <Alert variant="destructive">{messageOf(error)}</Alert>}
          {overview.isPending && (
            <div className="flex min-h-[18rem] items-center justify-center gap-3 text-muted-foreground">
              <Spinner /> Loading jobs…
            </div>
          )}
          {data && !data.initialized && (
            <Alert variant="destructive">
              {data.error ?? "Initialize this stack before managing jobs and workers."}
            </Alert>
          )}

          {data?.initialized && (
            <div className="grid grid-cols-2 items-start gap-5 max-[900px]:grid-cols-1">
              <section className="min-w-0 rounded-2xl border border-border bg-card p-5 shadow-sm max-[700px]:p-4">
                <PanelHeading
                  icon={<Clock className="size-4" />}
                  title="Scheduled jobs"
                  description="Run a command on a cron schedule."
                  count={cronJobs.length}
                />
                <div className="mt-5 grid gap-3">
                  {cronJobs.map((job) => (
                    <article
                      className="rounded-xl border border-border bg-muted/30 p-4"
                      key={job.name}
                    >
                      <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0">
                          <strong className="block truncate text-sm font-semibold">
                            {job.name}
                          </strong>
                          <p className="m-0 mt-1 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                            <code className="rounded bg-card px-1.5 py-0.5 text-foreground">
                              {job.schedule}
                            </code>
                            <span aria-hidden="true">·</span>
                            {job.timezone}
                          </p>
                        </div>
                        <div className="flex shrink-0 items-center gap-2">
                          <JobLogsButton
                            app={application.slug}
                            name={job.name}
                            kind="cron"
                            disabled={busy}
                          />
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon-xs"
                            className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                            aria-label={`Remove cron ${job.name}`}
                            title={`Remove ${job.name}`}
                            disabled={busy}
                            onClick={() => {
                              if (
                                window.confirm(`Remove cron ${job.name} from ${application.slug}?`)
                              ) {
                                removeCron.mutate({ app: application.slug, name: job.name });
                              }
                            }}
                          >
                            <Trash2 />
                          </Button>
                        </div>
                      </div>
                      <code className="my-3 block max-h-28 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-border bg-card p-3 text-xs leading-relaxed">
                        {job.command}
                      </code>
                      <div className="flex flex-wrap gap-1.5">
                        <Badge variant="outline">{job.commandMode}</Badge>
                        <Badge variant="outline">output: {job.output}</Badge>
                        {job.timeoutSec && (
                          <Badge variant="outline">timeout: {job.timeoutSec}s</Badge>
                        )}
                      </div>
                    </article>
                  ))}
                  {!cronJobs.length && (
                    <EmptyPanel text="No scheduled jobs for this application." />
                  )}
                </div>
                <details className="group mt-5 border-t border-border pt-4 [&[open]>summary]:mb-4 [&>summary]:list-none [&>summary::-webkit-details-marker]:hidden">
                  <summary className={buttonVariants({ variant: "outline", size: "sm" })}>
                    <Plus className="size-3.5" aria-hidden="true" /> Add scheduled job
                  </summary>
                  <form
                    className="rounded-xl border border-border bg-muted/30 p-4"
                    onSubmit={(event) => void submitCron(event)}
                  >
                    <fieldset disabled={busy} className="grid gap-4">
                      <label>
                        <FieldLabel>Name</FieldLabel>
                        <Input className="w-full bg-card" name="name" required />
                      </label>
                      <div className="grid grid-cols-2 gap-4 max-[700px]:grid-cols-1">
                        <label>
                          <FieldLabel>Cron schedule</FieldLabel>
                          <Input
                            className="w-full bg-card"
                            name="schedule"
                            required
                            placeholder="*/5 * * * *"
                          />
                          <FieldHint>Use standard five-field cron syntax.</FieldHint>
                        </label>
                        <label>
                          <FieldLabel>Timezone</FieldLabel>
                          <Input
                            className="w-full bg-card"
                            name="timezone"
                            required
                            defaultValue="UTC"
                          />
                        </label>
                      </div>
                      <label>
                        <FieldLabel>
                          Command{" "}
                          <span className="font-normal text-muted-foreground">
                            ({commandMode === "argv" ? "one argument per line" : "shell"})
                          </span>
                        </FieldLabel>
                        <Textarea className="w-full bg-card" name="command" required />
                      </label>
                      <div className="grid grid-cols-2 gap-4 max-[700px]:grid-cols-1">
                        <label>
                          <FieldLabel>Command mode</FieldLabel>
                          <NativeSelect
                            className="w-full bg-card"
                            value={commandMode}
                            onChange={(event) =>
                              setCommandMode(event.target.value as AddCronJobInput["commandMode"])
                            }
                          >
                            <NativeSelectOption value="argv">Argument list</NativeSelectOption>
                            <NativeSelectOption value="shell">Explicit shell</NativeSelectOption>
                          </NativeSelect>
                        </label>
                        <label>
                          <FieldLabel>Output</FieldLabel>
                          <NativeSelect className="w-full bg-card" name="output" defaultValue="log">
                            <NativeSelectOption value="log">Log</NativeSelectOption>
                            <NativeSelectOption value="null">Discard</NativeSelectOption>
                            <NativeSelectOption value="inherit">Inherit</NativeSelectOption>
                          </NativeSelect>
                        </label>
                        <label>
                          <FieldLabel>
                            Timeout seconds{" "}
                            <span className="font-normal text-muted-foreground">(optional)</span>
                          </FieldLabel>
                          <Input
                            className="w-full bg-card"
                            name="timeoutSec"
                            type="number"
                            min="1"
                          />
                        </label>
                      </div>
                    </fieldset>
                    <Button className="mt-4" size="sm" disabled={busy} type="submit">
                      {addCron.isPending ? (
                        <Spinner />
                      ) : (
                        <Plus className="size-3.5" aria-hidden="true" />
                      )}
                      Add cron
                    </Button>
                  </form>
                </details>
              </section>

              <section className="min-w-0 rounded-2xl border border-border bg-card p-5 shadow-sm max-[700px]:p-4">
                <PanelHeading
                  icon={<Wrench className="size-4" />}
                  title="Workers"
                  description="Keep a long-running process alive."
                  count={workers.length}
                />
                <div className="mt-5 grid gap-3">
                  {workers.map((worker) => (
                    <article
                      className="rounded-xl border border-border bg-muted/30 p-4"
                      key={worker.name}
                    >
                      <div className="flex items-start justify-between gap-3">
                        <strong className="min-w-0 truncate text-sm font-semibold">
                          {worker.name}
                        </strong>
                        <div className="flex shrink-0 items-center gap-2">
                          <JobLogsButton
                            app={application.slug}
                            name={worker.name}
                            kind="worker"
                            disabled={busy}
                          />
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon-xs"
                            className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                            aria-label={`Remove worker ${worker.name}`}
                            title={`Remove ${worker.name}`}
                            disabled={busy}
                            onClick={() => {
                              if (
                                window.confirm(
                                  `Remove worker ${worker.name} from ${application.slug}?`,
                                )
                              ) {
                                removeWorker.mutate({ app: application.slug, name: worker.name });
                              }
                            }}
                          >
                            <Trash2 />
                          </Button>
                        </div>
                      </div>
                      <code className="my-3 block max-h-28 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-border bg-card p-3 text-xs leading-relaxed">
                        {worker.command}
                      </code>
                      <div className="flex flex-wrap gap-1.5">
                        <Badge variant="outline">argv</Badge>
                        <Badge variant="outline">
                          {worker.autorestart ? "auto restart" : "manual restart"}
                        </Badge>
                        <Badge variant="outline">
                          stop: {worker.stopsignal} / {worker.stopwaitsecs}s
                        </Badge>
                      </div>
                    </article>
                  ))}
                  {!workers.length && <EmptyPanel text="No workers for this application." />}
                </div>
                <details className="group mt-5 border-t border-border pt-4 [&[open]>summary]:mb-4 [&>summary]:list-none [&>summary::-webkit-details-marker]:hidden">
                  <summary className={buttonVariants({ variant: "outline", size: "sm" })}>
                    <Plus className="size-3.5" aria-hidden="true" /> Add worker
                  </summary>
                  <form
                    className="rounded-xl border border-border bg-muted/30 p-4"
                    onSubmit={(event) => void submitWorker(event)}
                  >
                    <fieldset disabled={busy} className="grid gap-4">
                      <label>
                        <FieldLabel>Name</FieldLabel>
                        <Input className="w-full bg-card" name="name" required />
                      </label>
                      <label>
                        <FieldLabel>
                          Command{" "}
                          <span className="font-normal text-muted-foreground">
                            (one argument per line)
                          </span>
                        </FieldLabel>
                        <Textarea className="w-full bg-card" name="command" required />
                      </label>
                      <div className="grid grid-cols-2 gap-4 max-[700px]:grid-cols-1">
                        <label>
                          <FieldLabel>Stop signal</FieldLabel>
                          <Input
                            className="w-full bg-card"
                            name="stopsignal"
                            defaultValue="TERM"
                            required
                          />
                        </label>
                        <label>
                          <FieldLabel>Stop wait seconds</FieldLabel>
                          <Input
                            className="w-full bg-card"
                            name="stopwaitsecs"
                            type="number"
                            min="1"
                            defaultValue="10"
                            required
                          />
                        </label>
                      </div>
                      <label className="flex cursor-pointer items-start gap-3 rounded-lg border border-border bg-card p-3">
                        <Checkbox name="autorestart" defaultChecked className="mt-0.5" />
                        <span className="text-sm">
                          <span className="font-medium">Restart automatically</span>
                          <small className="mt-1 block text-xs text-muted-foreground">
                            Restart the worker when its process exits.
                          </small>
                        </span>
                      </label>
                    </fieldset>
                    <Button className="mt-4" size="sm" disabled={busy} type="submit">
                      {addWorker.isPending ? (
                        <Spinner />
                      ) : (
                        <RefreshCw className="size-3.5" aria-hidden="true" />
                      )}
                      Add worker
                    </Button>
                  </form>
                </details>
              </section>
            </div>
          )}

          <DialogFooter className="border-t border-border pt-5">
            <Button type="button" variant="ghost" disabled={busy} onClick={onClose}>
              Close
            </Button>
          </DialogFooter>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function PanelHeading({
  icon,
  title,
  description,
  count,
}: {
  icon: ReactNode;
  title: string;
  description: string;
  count: number;
}) {
  return (
    <div className="flex items-start justify-between gap-3">
      <div className="flex min-w-0 items-start gap-3">
        <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-muted text-primary">
          {icon}
        </span>
        <div>
          <h3 className="m-0 text-base font-semibold">{title}</h3>
          <p className="m-0 mt-1 text-sm text-muted-foreground">{description}</p>
        </div>
      </div>
      <Badge variant="secondary">{count}</Badge>
    </div>
  );
}

function EmptyPanel({ text }: { text: string }) {
  return (
    <div className="rounded-xl border border-dashed border-border bg-muted/20 px-4 py-6 text-center text-sm text-muted-foreground">
      {text}
    </div>
  );
}

function FieldLabel({ children }: { children: ReactNode }) {
  return <span className="mb-1.5 block text-sm font-medium">{children}</span>;
}

function FieldHint({ children }: { children: ReactNode }) {
  return <small className="mt-1.5 block text-xs text-muted-foreground">{children}</small>;
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
