import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { orpc } from "../../api/client.ts";
import { ConfirmOperationDialog } from "./ConfirmOperationDialog.tsx";

export function OperationsControls({ stackName }: { stackName: string }) {
  const queryClient = useQueryClient();
  const [deployApp, setDeployApp] = useState("");
  const [pendingLifecycle, setPendingLifecycle] = useState<"stop" | "restart" | null>(null);
  const [confirmDeploy, setConfirmDeploy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const refresh = () => queryClient.invalidateQueries({ queryKey: orpc.operations.overview.key() });
  const completed = (result: { message: string }) => {
    setNotice(result.message);
    void refresh();
  };
  const stack = useMutation(orpc.operations.stackAction.mutationOptions({ onSuccess: completed }));
  const apply = useMutation(orpc.operations.apply.mutationOptions({ onSuccess: completed }));
  const backup = useMutation(orpc.operations.backup.mutationOptions({ onSuccess: completed }));
  const logs = useMutation(orpc.operations.logs.mutationOptions());
  const maintenance = useMutation(
    orpc.operations.maintenance.mutationOptions({ onSuccess: completed }),
  );
  const deploy = useMutation(orpc.operations.drainDeploy.mutationOptions({ onSuccess: completed }));
  const busy =
    stack.isPending ||
    apply.isPending ||
    backup.isPending ||
    maintenance.isPending ||
    deploy.isPending;
  const error =
    stack.error ?? apply.error ?? backup.error ?? logs.error ?? maintenance.error ?? deploy.error;

  function lifecycle(action: "start" | "stop" | "restart") {
    if (action === "start") stack.mutate({ action });
    else setPendingLifecycle(action);
  }

  function confirmLifecycle() {
    if (!pendingLifecycle) return;
    stack.mutate(
      { action: pendingLifecycle, confirmation: stackName },
      { onSuccess: () => setPendingLifecycle(null) },
    );
  }

  function drain() {
    if (deployApp.trim()) setConfirmDeploy(true);
  }

  function confirmDrain() {
    const app = deployApp.trim();
    if (!app) return;
    deploy.mutate({ app, confirmation: app }, { onSuccess: () => setConfirmDeploy(false) });
  }

  return (
    <article className="panel full">
      <h2>Controls</h2>
      <p className="muted">Mutating actions run directly against this local stack.</p>
      {error && <div className="alert alert-error">{messageOf(error)}</div>}
      {notice && <div className="alert alert-success">{notice}</div>}
      <div className="operations-actions">
        <button className="btn btn-success" disabled={busy} onClick={() => lifecycle("start")}>
          Start
        </button>
        <button className="btn btn-outline" disabled={busy} onClick={() => lifecycle("restart")}>
          Restart
        </button>
        <button
          className="btn btn-error btn-outline"
          disabled={busy}
          onClick={() => lifecycle("stop")}
        >
          Stop
        </button>
        <button className="btn btn-primary" disabled={busy} onClick={() => apply.mutate({})}>
          Render &amp; apply
        </button>
        <button className="btn btn-outline" disabled={busy} onClick={() => backup.mutate({})}>
          Back up all databases
        </button>
        <button
          className="btn btn-outline"
          disabled={busy}
          onClick={() => maintenance.mutate({ retainDays: 14 })}
        >
          Run maintenance
        </button>
        <button
          className="btn btn-outline"
          disabled={logs.isPending}
          onClick={() => logs.mutate({ tail: 100 })}
        >
          Load recent logs
        </button>
      </div>
      <div className="operations-deploy">
        <input
          className="input input-bordered"
          aria-label="Application slug"
          placeholder="Application slug"
          value={deployApp}
          onChange={(event) => setDeployApp(event.target.value)}
        />
        <button className="btn btn-outline" disabled={busy || !deployApp.trim()} onClick={drain}>
          Run queued deploy
        </button>
      </div>
      {backup.data?.artifacts.length ? (
        <div className="alert">
          {backup.data.artifacts
            .map((artifact) => `${artifact.engine}:${artifact.database} (${artifact.bytes} bytes)`)
            .join(" · ")}
        </div>
      ) : null}
      {logs.data && (
        <pre className="operations-logs" aria-label="Recent service logs">
          {logs.data.lines.join("\n") || "No log lines returned."}
        </pre>
      )}
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
      <button
        className="btn btn-sm btn-outline"
        disabled={restart.isPending}
        onClick={() => setConfirming(true)}
      >
        {restart.isPending ? "Restarting…" : "Restart"}
      </button>
      {restart.error && <small className="text-error">{messageOf(restart.error)}</small>}
      {restart.data && <small className="text-success">Restarted</small>}
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

function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
