import { useMutation, useQuery } from "@tanstack/react-query";
import { api, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { useTrackOperation } from "../operations/OperationTracker.tsx";

export function useApplicationList() {
  return useQuery({
    queryKey: keys.apps.list(),
    queryFn: ({ signal }) => api.apps.list(signal),
    refetchInterval: 15_000,
  });
}

export function useApplication(id: string | null) {
  return useQuery({
    queryKey: keys.apps.detail(id ?? ""),
    queryFn: ({ signal }) => api.apps.get(id ?? "", signal),
    enabled: id !== null,
    refetchInterval: 10_000,
  });
}

export function useCatalog() {
  return useQuery({ queryKey: keys.catalog, queryFn: ({ signal }) => api.system.catalog(signal), staleTime: Infinity });
}

/** A mutation whose accepted operation is tracked until it finishes. */
export function useOperationMutation<V>(fn: (vars: V) => Promise<T.Accepted>) {
  const track = useTrackOperation();
  return useMutation({ mutationFn: fn, onSuccess: track });
}

export type AppAction = "start" | "stop" | "restart" | "publish" | "unpublish";

export function useAppAction() {
  return useOperationMutation(({ id, action }: { id: string; action: AppAction }) => api.apps.action(id, action));
}
