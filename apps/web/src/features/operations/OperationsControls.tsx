import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { orpc } from "../../api/client.ts";
import { ConfirmOperationDialog } from "./ConfirmOperationDialog.tsx";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert } from "@/components/ui/alert";

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
      {error && <Alert variant="destructive">{messageOf(error)}</Alert>}
      {notice && (
        <Alert className="border-emerald-600/40 bg-emerald-600/10 text-emerald-700 dark:text-emerald-400">
          {notice}
        </Alert>
      )}
      <div className="operations-actions">
        <Button
          className="bg-emerald-600 text-white hover:bg-emerald-600/90"
          disabled={busy}
          onClick={() => lifecycle("start")}
        >
          Start
        </Button>
        <Button variant="outline" disabled={busy} onClick={() => lifecycle("restart")}>
          Restart
        </Button>
        <Button
          variant="outline"
          className="border-destructive text-destructive hover:bg-destructive/10 hover:text-destructive"
          disabled={busy}
          onClick={() => lifecycle("stop")}
        >
          Stop
        </Button>
        <Button disabled={busy} onClick={() => apply.mutate({})}>
          Render &amp; apply
        </Button>
        <Button variant="outline" disabled={busy} onClick={() => backup.mutate({})}>
          Back up all databases
        </Button>
        <Button
          variant="outline"
          disabled={busy}
          onClick={() => maintenance.mutate({ retainDays: 14 })}
        >
          Run maintenance
        </Button>
        <Button
          variant="outline"
          disabled={logs.isPending}
          onClick={() => logs.mutate({ tail: 100 })}
        >
          Load recent logs
        </Button>
      </div>
      <div className="operations-deploy">
        <Input
          className="min-w-64"
          aria-label="Application slug"
          placeholder="Application slug"
          value={deployApp}
          onChange={(event) => setDeployApp(event.target.value)}
        />
        <Button variant="outline" disabled={busy || !deployApp.trim()} onClick={drain}>
          Run queued deploy
        </Button>
      </div>
      {backup.data?.artifacts.length ? (
        <Alert>
          {backup.data.artifacts
            .map((artifact) => `${artifact.engine}:${artifact.database} (${artifact.bytes} bytes)`)
            .join(" · ")}
        </Alert>
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
      <Button
        variant="outline"
        size="sm"
        disabled={restart.isPending}
        onClick={() => setConfirming(true)}
      >
        {restart.isPending ? "Restarting…" : "Restart"}
      </Button>
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
