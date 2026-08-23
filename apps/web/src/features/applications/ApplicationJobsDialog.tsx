import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { AddCronJobInput, Application, JobsOverview } from "@bento/shared";
import { orpc } from "../../api/client.ts";

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
    <dialog className="modal" open onCancel={(event) => event.preventDefault()}>
      <div className="modal-box app-jobs-box">
        <button
          type="button"
          className="btn btn-sm btn-circle btn-ghost modal-close"
          aria-label="Close cron and worker manager"
          disabled={busy}
          onClick={onClose}
        >
          ✕
        </button>
        <h2>{application.slug} crons and workers</h2>
        <p className="form-help">
          Commands run under this application identity in its PHP runner. Full commands are shown
          below with secret-like values redacted.
        </p>
        {error && <div className="alert alert-error">{messageOf(error)}</div>}
        {overview.isPending && (
          <div className="loading-state">
            <span className="loading loading-spinner" /> Loading jobs…
          </div>
        )}

        {data?.initialized && (
          <div className="app-job-management-grid">
            <section>
              <div className="app-job-heading">
                <h3>Scheduled jobs</h3>
                <span className="badge badge-outline">{cronJobs.length}</span>
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
                        <span className="badge badge-outline">{job.commandMode}</span>
                        <span className="badge badge-outline">output: {job.output}</span>
                        {job.timeoutSec && (
                          <span className="badge badge-outline">timeout: {job.timeoutSec}s</span>
                        )}
                      </div>
                    </div>
                    <button
                      type="button"
                      className="btn btn-xs btn-ghost app-remove"
                      disabled={busy}
                      onClick={() => {
                        if (window.confirm(`Remove cron ${job.name} from ${application.slug}?`)) {
                          removeCron.mutate({ app: application.slug, name: job.name });
                        }
                      }}
                    >
                      Remove
                    </button>
                  </article>
                ))}
                {!cronJobs.length && <p className="muted">No scheduled jobs.</p>}
              </div>
              <details className="app-job-form">
                <summary className="btn btn-sm btn-outline">+ Add scheduled job</summary>
                <form onSubmit={(event) => void submitCron(event)}>
                  <fieldset disabled={busy}>
                    <label>
                      <span className="label-text">Name</span>
                      <input className="input input-bordered w-full" name="name" required />
                    </label>
                    <div className="form-grid">
                      <label>
                        <span className="label-text">Cron schedule</span>
                        <input
                          className="input input-bordered w-full"
                          name="schedule"
                          required
                          placeholder="*/5 * * * *"
                        />
                      </label>
                      <label>
                        <span className="label-text">Timezone</span>
                        <input
                          className="input input-bordered w-full"
                          name="timezone"
                          required
                          defaultValue="UTC"
                        />
                      </label>
                    </div>
                    <label>
                      <span className="label-text">
                        Command{" "}
                        <small>{commandMode === "argv" ? "one argument per line" : "shell"}</small>
                      </span>
                      <textarea
                        className="textarea textarea-bordered w-full"
                        name="command"
                        required
                      />
                    </label>
                    <div className="form-grid">
                      <label>
                        <span className="label-text">Command mode</span>
                        <select
                          className="select select-bordered w-full"
                          value={commandMode}
                          onChange={(event) =>
                            setCommandMode(event.target.value as AddCronJobInput["commandMode"])
                          }
                        >
                          <option value="argv">Argument list</option>
                          <option value="shell">Explicit shell</option>
                        </select>
                      </label>
                      <label>
                        <span className="label-text">Output</span>
                        <select
                          className="select select-bordered w-full"
                          name="output"
                          defaultValue="log"
                        >
                          <option value="log">Log</option>
                          <option value="null">Discard</option>
                          <option value="inherit">Inherit</option>
                        </select>
                      </label>
                      <label>
                        <span className="label-text">Timeout seconds (optional)</span>
                        <input
                          className="input input-bordered w-full"
                          name="timeoutSec"
                          type="number"
                          min="1"
                        />
                      </label>
                    </div>
                  </fieldset>
                  <button className="btn btn-sm btn-primary" disabled={busy} type="submit">
                    Add cron
                  </button>
                </form>
              </details>
            </section>

            <section>
              <div className="app-job-heading">
                <h3>Workers</h3>
                <span className="badge badge-outline">{workers.length}</span>
              </div>
              <div className="app-job-list">
                {workers.map((worker) => (
                  <article className="database-binding" key={worker.name}>
                    <div className="app-job-details">
                      <strong>{worker.name}</strong>
                      <code className="app-job-command">{worker.command}</code>
                      <div className="app-job-meta">
                        <span className="badge badge-outline">argv</span>
                        <span className="badge badge-outline">
                          {worker.autorestart ? "auto restart" : "manual restart"}
                        </span>
                        <span className="badge badge-outline">
                          stop: {worker.stopsignal} / {worker.stopwaitsecs}s
                        </span>
                      </div>
                    </div>
                    <button
                      type="button"
                      className="btn btn-xs btn-ghost app-remove"
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
                    </button>
                  </article>
                ))}
                {!workers.length && <p className="muted">No workers.</p>}
              </div>
              <details className="app-job-form">
                <summary className="btn btn-sm btn-outline">+ Add worker</summary>
                <form onSubmit={(event) => void submitWorker(event)}>
                  <fieldset disabled={busy}>
                    <label>
                      <span className="label-text">Name</span>
                      <input className="input input-bordered w-full" name="name" required />
                    </label>
                    <label>
                      <span className="label-text">
                        Command <small>one argument per line</small>
                      </span>
                      <textarea
                        className="textarea textarea-bordered w-full"
                        name="command"
                        required
                      />
                    </label>
                    <div className="form-grid">
                      <label>
                        <span className="label-text">Stop signal</span>
                        <input
                          className="input input-bordered w-full"
                          name="stopsignal"
                          defaultValue="TERM"
                          required
                        />
                      </label>
                      <label>
                        <span className="label-text">Stop wait seconds</span>
                        <input
                          className="input input-bordered w-full"
                          name="stopwaitsecs"
                          type="number"
                          min="1"
                          defaultValue="10"
                          required
                        />
                      </label>
                    </div>
                    <label className="checkbox-row">
                      <input
                        className="checkbox"
                        name="autorestart"
                        type="checkbox"
                        defaultChecked
                      />
                      <span>Restart automatically</span>
                    </label>
                  </fieldset>
                  <button className="btn btn-sm btn-primary" disabled={busy} type="submit">
                    Add worker
                  </button>
                </form>
              </details>
            </section>
          </div>
        )}

        <div className="modal-action">
          <button type="button" className="btn btn-ghost" disabled={busy} onClick={onClose}>
            Close
          </button>
        </div>
      </div>
      <button className="modal-backdrop" aria-label="Close" disabled={busy} onClick={onClose} />
    </dialog>
  );
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
