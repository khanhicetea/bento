import { createContext, use, useEffect, useState, type PropsWithChildren } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, CircleAlert, X } from "lucide-react";
import { Link } from "wouter";
import { type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { describeOp } from "../../lib/format.ts";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { isTerminal, useTrackedOperation } from "./useOperation.ts";

export { isTerminal } from "./useOperation.ts";

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
    // A configure saves settings on acceptance; show them before it finishes.
    if (accepted.operation.targetKind === "service") void queryClient.invalidateQueries({ queryKey: keys.services });
    if (accepted.operation.targetKind === "host") {
      void queryClient.invalidateQueries({ queryKey: keys.hosts });
      void queryClient.invalidateQueries({ queryKey: keys.apps.all });
    }
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
  const query = useTrackedOperation(initial);
  const op = query.data;
  const lastEvent = op.events?.at(-1)?.message;
  const failed = op.state === "failed" || op.state === "interrupted";
  useEffect(() => {
    if (op.state !== "succeeded") return;
    const timer = window.setTimeout(onDismiss, 6_000);
    return () => window.clearTimeout(timer);
    // onDismiss is a new function on every provider render; listing it would
    // restart the timer whenever any toast changes. Any version of it is
    // correct: dismissal filters by id with a functional state update.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [op.state]);
  if (!visible) return null;
  return (
    <div className="flex items-start gap-3 rounded-[1.125rem] bg-[var(--nori)] p-3.5 text-sm text-[var(--nori-ink)] shadow-[0_0_0_4px_var(--rim),0_20px_40px_-20px_rgb(0_0_0/0.6)]">
      <span className="mt-0.5">
        {!isTerminal(op.state) && <Spinner />}
        {op.state === "succeeded" && <CheckCircle2 className="size-4 text-[#9cc27c]" />}
        {isTerminal(op.state) && op.state !== "succeeded" && <CircleAlert className="size-4 text-[#ef7a66]" />}
      </span>
      <div className="min-w-0 flex-1">
        <div className="font-label text-[0.8125rem] font-semibold tracking-[0.08em] uppercase">
          <Link href={`/activity/${op.id}`} className="hover:underline">
            {describeOp(op)}
          </Link>{" "}
          <span className="text-[var(--nori-dim)]">· {op.state}</span>
        </div>
        <div className="truncate font-mono text-xs text-[var(--nori-dim)]">
          {failed
            ? op.errorMessage
            : op.waitingOn
              ? "Waiting for an earlier operation"
              : (lastEvent ?? op.phase ?? "queued")}
        </div>
        {failed && op.guidance && <div className="mt-1 text-xs">{op.guidance}</div>}
      </div>
      {isTerminal(op.state) && (
        <Button
          variant="ghost"
          size="icon-xs"
          className="text-[var(--nori-ink)] hover:bg-white/10"
          aria-label="Dismiss"
          onClick={onDismiss}
        >
          <X />
        </Button>
      )}
    </div>
  );
}
