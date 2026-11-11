import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";

const terminalStates = new Set<string>(["succeeded", "failed", "cancelled", "interrupted"]);

export function isTerminal(state: string | undefined): boolean {
  return state !== undefined && terminalStates.has(state);
}

/** Invalidate only the domains an operation can have changed. */
export function invalidateFor(queryClient: QueryClient, op: T.Operation) {
  // Lists only: invalidating ["operations"] would include this tracker's own
  // detail query and cancel the fetch that observed the terminal state.
  void queryClient.invalidateQueries({ queryKey: keys.operations.lists });
  void queryClient.invalidateQueries({ queryKey: keys.system });
  // Any finished operation can clear (or add to) a reconcile target's budget.
  void queryClient.invalidateQueries({ queryKey: keys.reconcile });
  switch (op.targetKind) {
    case "app":
      void queryClient.invalidateQueries({ queryKey: keys.apps.all });
      // Publication and removal change which hosts are live or exist.
      void queryClient.invalidateQueries({ queryKey: keys.hosts });
      if (op.kind === "app.remove") void queryClient.invalidateQueries({ queryKey: keys.retired });
      if (op.kind.startsWith("restic.")) void queryClient.invalidateQueries({ queryKey: keys.backups.all });
      break;
    case "retired-app":
      void queryClient.invalidateQueries({ queryKey: keys.retired });
      break;
    case "service":
      void queryClient.invalidateQueries({ queryKey: keys.services });
      break;
    case "edge":
    case "host":
      void queryClient.invalidateQueries({ queryKey: keys.edge });
      void queryClient.invalidateQueries({ queryKey: keys.hosts });
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
    case "image":
      // Runtime images and the Docker inventory sit under keys.system; a
      // rollout changes which build each app runs.
      void queryClient.invalidateQueries({ queryKey: keys.apps.all });
      break;
    default:
      void queryClient.invalidateQueries({ predicate: (q) => q.queryKey[0] !== "operations" });
  }
}

/** True when a fetch is the first to observe the operation in a final state. */
export function justFinished(previous: T.Operation | undefined, next: T.Operation): boolean {
  return isTerminal(next.state) && !isTerminal(previous?.state);
}

async function fetchOperation(queryClient: QueryClient, id: string, signal: AbortSignal): Promise<T.Operation> {
  const op = await api.operations.get(id, signal);
  if (justFinished(queryClient.getQueryData<T.Operation>(keys.operations.detail(id)), op)) {
    invalidateFor(queryClient, op);
  }
  return op;
}

const pollWhileActive = (query: { state: { data: T.Operation | undefined } }) =>
  isTerminal(query.state.data?.state) ? false : 1_000;

/**
 * Polls one operation until it is final. Every view of an operation uses
 * this hook (or useTrackedOperation), so whichever observer's fetch first sees
 * the final state refreshes the data the operation changed, exactly once.
 */
export function useOperation(id: string) {
  const queryClient = useQueryClient();
  return useQuery({
    queryKey: keys.operations.detail(id),
    queryFn: ({ signal }) => fetchOperation(queryClient, id, signal),
    enabled: id !== "",
    refetchInterval: pollWhileActive,
  });
}

/** Like useOperation, seeded with an operation the caller already has, so data is never undefined. */
export function useTrackedOperation(initial: T.Operation) {
  const queryClient = useQueryClient();
  return useQuery({
    queryKey: keys.operations.detail(initial.id),
    queryFn: ({ signal }) => fetchOperation(queryClient, initial.id, signal),
    initialData: initial,
    refetchInterval: pollWhileActive,
  });
}
