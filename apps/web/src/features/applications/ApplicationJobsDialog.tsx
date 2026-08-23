import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { AddCronJobInput, Application, JobsOverview } from "@bento/shared";
import { orpc } from "../../api/client.ts";
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
        className="app-jobs-box max-h-[calc(100vh-2rem)] overflow-y-auto"
        showCloseButton={!busy}
      >
        <DialogHeader>
          <DialogTitle>{application.slug} crons and workers</DialogTitle>
          <DialogDescription>
            Commands run under this application identity in its PHP runner. Full commands are shown
            below with secret-like values redacted.
          </DialogDescription>
        </DialogHeader>
        {error && <Alert variant="destructive">{messageOf(error)}</Alert>}
        {overview.isPending && (
          <div className="loading-state">
            <Spinner /> Loading jobs…
          </div>
        )}

        {data?.initialized && (
          <div className="app-job-management-grid">
            <section>
              <div className="app-job-heading">
                <h3>Scheduled jobs</h3>
                <Badge variant="outline">{cronJobs.length}</Badge>
              </div>
              <div className="app-job-list">
                {cronJobs.map((job) => (
                  <article className="database-binding" key={job.name}>
                    <div className="app-job-details">
                      <strong>{job.name}</strong>
                      <small>
                        <code>{job.schedule}</code> · {job.timezone}
                      </small>
                      <code className="app-job-command">{job.command}</code>
                      <div className="app-job-meta">
                        <Badge variant="outline">{job.commandMode}</Badge>
                        <Badge variant="outline">output: {job.output}</Badge>
                        {job.timeoutSec && (
                          <Badge variant="outline">timeout: {job.timeoutSec}s</Badge>
                        )}
                      </div>
                    </div>
                    <Button
                      type="button"
                      variant="ghost"
                      size="xs"
                      className="app-remove"
                      disabled={busy}
                      onClick={() => {
                        if (window.confirm(`Remove cron ${job.name} from ${application.slug}?`)) {
                          removeCron.mutate({ app: application.slug, name: job.name });
                        }
                      }}
                    >
                      Remove
                    </Button>
                  </article>
                ))}
                {!cronJobs.length && <p className="muted">No scheduled jobs.</p>}
              </div>
              <details className="app-job-form">
                <summary className={buttonVariants({ variant: "outline", size: "sm" })}>
                  + Add scheduled job
                </summary>
                <form onSubmit={(event) => void submitCron(event)}>
                  <fieldset disabled={busy}>
                    <label>
                      <span className="label-text">Name</span>
                      <Input className="w-full" name="name" required />
                    </label>
                    <div className="form-grid">
                      <label>
                        <span className="label-text">Cron schedule</span>
                        <Input
                          className="w-full"
                          name="schedule"
                          required
                          placeholder="*/5 * * * *"
                        />
                      </label>
                      <label>
                        <span className="label-text">Timezone</span>
                        <Input className="w-full" name="timezone" required defaultValue="UTC" />
                      </label>
                    </div>
                    <label>
                      <span className="label-text">
                        Command{" "}
                        <small>{commandMode === "argv" ? "one argument per line" : "shell"}</small>
                      </span>
                      <Textarea className="w-full" name="command" required />
                    </label>
                    <div className="form-grid">
                      <label>
                        <span className="label-text">Command mode</span>
                        <NativeSelect
                          className="w-full"
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
                        <span className="label-text">Output</span>
                        <NativeSelect className="w-full" name="output" defaultValue="log">
                          <NativeSelectOption value="log">Log</NativeSelectOption>
                          <NativeSelectOption value="null">Discard</NativeSelectOption>
                          <NativeSelectOption value="inherit">Inherit</NativeSelectOption>
                        </NativeSelect>
                      </label>
                      <label>
                        <span className="label-text">Timeout seconds (optional)</span>
                        <Input className="w-full" name="timeoutSec" type="number" min="1" />
                      </label>
                    </div>
                  </fieldset>
                  <Button size="sm" disabled={busy} type="submit">
                    Add cron
                  </Button>
                </form>
              </details>
            </section>

            <section>
              <div className="app-job-heading">
                <h3>Workers</h3>
                <Badge variant="outline">{workers.length}</Badge>
              </div>
              <div className="app-job-list">
                {workers.map((worker) => (
                  <article className="database-binding" key={worker.name}>
                    <div className="app-job-details">
                      <strong>{worker.name}</strong>
                      <code className="app-job-command">{worker.command}</code>
                      <div className="app-job-meta">
                        <Badge variant="outline">argv</Badge>
                        <Badge variant="outline">
                          {worker.autorestart ? "auto restart" : "manual restart"}
                        </Badge>
                        <Badge variant="outline">
                          stop: {worker.stopsignal} / {worker.stopwaitsecs}s
                        </Badge>
                      </div>
                    </div>
                    <Button
                      type="button"
                      variant="ghost"
                      size="xs"
                      className="app-remove"
                      disabled={busy}
                      onClick={() => {
                        if (
                          window.confirm(`Remove worker ${worker.name} from ${application.slug}?`)
                        ) {
                          removeWorker.mutate({ app: application.slug, name: worker.name });
                        }
                      }}
                    >
                      Remove
                    </Button>
                  </article>
                ))}
                {!workers.length && <p className="muted">No workers.</p>}
              </div>
              <details className="app-job-form">
                <summary className={buttonVariants({ variant: "outline", size: "sm" })}>
                  + Add worker
                </summary>
                <form onSubmit={(event) => void submitWorker(event)}>
                  <fieldset disabled={busy}>
                    <label>
                      <span className="label-text">Name</span>
                      <Input className="w-full" name="name" required />
                    </label>
                    <label>
                      <span className="label-text">
                        Command <small>one argument per line</small>
                      </span>
                      <Textarea className="w-full" name="command" required />
                    </label>
                    <div className="form-grid">
                      <label>
                        <span className="label-text">Stop signal</span>
                        <Input className="w-full" name="stopsignal" defaultValue="TERM" required />
                      </label>
                      <label>
                        <span className="label-text">Stop wait seconds</span>
                        <Input
                          className="w-full"
                          name="stopwaitsecs"
                          type="number"
                          min="1"
                          defaultValue="10"
                          required
                        />
                      </label>
                    </div>
                    <label className="checkbox-row">
                      <Checkbox name="autorestart" defaultChecked />
                      <span>Restart automatically</span>
                    </label>
                  </fieldset>
                  <Button size="sm" disabled={busy} type="submit">
                    Add worker
                  </Button>
                </form>
              </details>
            </section>
          </div>
        )}

        <DialogFooter>
          <Button type="button" variant="ghost" disabled={busy} onClick={onClose}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
