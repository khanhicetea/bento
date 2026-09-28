import { createContext, use, useEffect, useState, type PropsWithChildren } from "react";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { CheckCircle2, CircleAlert, X } from "lucide-react";
import { Link } from "wouter";
import { api, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { describeOp } from "../../lib/format.ts";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";

const terminalStates = new Set<string>(["succeeded", "failed", "cancelled", "interrupted"]);

export function isTerminal(state: string | undefined): boolean {
  return state !== undefined && terminalStates.has(state);
}

/** Invalidate only the domains an operation can have changed. */
function invalidateFor(queryClient: QueryClient, op: T.Operation) {
  // Lists only: invalidating ["operations"] would include this tracker's own
  // detail query and cancel the fetch that observed the terminal state.
  void queryClient.invalidateQueries({ queryKey: keys.operations.lists });
  void queryClient.invalidateQueries({ queryKey: keys.system });
  // Any finished operation can clear (or add to) a reconcile target's budget.
  void queryClient.invalidateQueries({ queryKey: keys.reconcile });
  switch (op.targetKind) {
    case "app":
      void queryClient.invalidateQueries({ queryKey: keys.apps.all });
      if (op.kind === "app.remove") void queryClient.invalidateQueries({ queryKey: keys.retired });
      if (op.kind === "backup.restore" || op.kind === "backup.delete")
        void queryClient.invalidateQueries({ queryKey: keys.backups.all });
      break;
    case "retired-app":
      void queryClient.invalidateQueries({ queryKey: keys.retired });
      break;
    case "service":
      void queryClient.invalidateQueries({ queryKey: keys.services });
      break;
    case "edge":
    case "proxy":
      void queryClient.invalidateQueries({ queryKey: keys.edge });
      void queryClient.invalidateQueries({ queryKey: keys.proxies });
      void queryClient.invalidateQueries({ queryKey: keys.apps.all });
      break;
    case "tunnel":
      void queryClient.invalidateQueries({ queryKey: keys.tunnel });
      break;
    case "dbadmin":
      void queryClient.invalidateQueries({ queryKey: keys.dbadmin });
      break;
    case "backup":
      void queryClient.invalidateQueries({ queryKey: keys.backups.all });
      break;
    default:
      void queryClient.invalidateQueries({ predicate: (q) => q.queryKey[0] !== "operations" });
  }
}

const TrackContext = createContext<(accepted: T.Accepted) => void>(() => {});

/** Returns a function that tracks an accepted operation until it finishes. */
export function useTrackOperation() {
  return use(TrackContext);
}

export function OperationTrackerProvider({ children }: PropsWithChildren) {
  const [tracked, setTracked] = useState<T.Operation[]>([]);
  const queryClient = useQueryClient();

  function track(accepted: T.Accepted) {
    // Acceptance itself changes intent (for example desired state).
    void queryClient.invalidateQueries({ queryKey: keys.operations.lists });
    if (accepted.operation.targetKind === "app") void queryClient.invalidateQueries({ queryKey: keys.apps.all });
    setTracked((current) => [accepted.operation, ...current.filter((op) => op.id !== accepted.operation.id)]);
  }

  function dismiss(id: string) {
    setTracked((current) => current.filter((op) => op.id !== id));
  }

  return (
    <TrackContext value={track}>
      {children}
      {tracked.length > 0 && (
        <div
          className="fixed right-4 bottom-20 z-50 md:bottom-4 grid w-[min(420px,calc(100vw-2rem))] gap-2"
          aria-live="polite"
        >
          {tracked.map((op, index) => (
            <TrackedOperation key={op.id} initial={op} visible={index < 5} onDismiss={() => dismiss(op.id)} />
          ))}
        </div>
      )}
    </TrackContext>
  );
}

function TrackedOperation({
  initial,
  visible,
  onDismiss,
}: {
  initial: T.Operation;
  visible: boolean;
  onDismiss: () => void;
}) {
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: keys.operations.detail(initial.id),
    queryFn: async ({ signal }) => {
      const op = await api.operations.get(initial.id, signal);
      if (isTerminal(op.state)) invalidateFor(queryClient, op);
      return op;
    },
    initialData: initial,
    refetchInterval: (q) => (isTerminal(q.state.data?.state) ? false : 1000),
  });
  const op = query.data;
  const lastEvent = op.events?.at(-1)?.message;
  const failed = op.state === "failed" || op.state === "interrupted";
  useEffect(() => {
    if (op.state !== "succeeded") return;
    const timer = window.setTimeout(onDismiss, 6_000);
    return () => window.clearTimeout(timer);
  }, [op.state, onDismiss]);
  if (!visible) return null;
  return (
    <div className="flex items-start gap-3 rounded-2xl border border-border bg-card p-3.5 text-sm shadow-xl">
      <span className="mt-0.5">
        {!isTerminal(op.state) && <Spinner />}
        {op.state === "succeeded" && <CheckCircle2 className="size-4 text-success" />}
        {isTerminal(op.state) && op.state !== "succeeded" && <CircleAlert className="size-4 text-destructive" />}
      </span>
      <div className="min-w-0 flex-1">
        <div className="font-medium">
          <Link href={`/activity/${op.id}`} className="hover:underline">
            {describeOp(op)}
          </Link>{" "}
          <span className="text-muted-foreground">· {op.state}</span>
        </div>
        <div className="truncate text-xs text-muted-foreground">
          {failed ? op.errorMessage : (lastEvent ?? op.phase ?? "queued")}
        </div>
        {failed && op.guidance && <div className="mt-1 text-xs">{op.guidance}</div>}
      </div>
      {isTerminal(op.state) && (
        <Button variant="ghost" size="icon-xs" aria-label="Dismiss" onClick={onDismiss}>
          <X />
        </Button>
      )}
    </div>
  );
}
