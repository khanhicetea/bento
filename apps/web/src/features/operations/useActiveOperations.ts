import { useQuery } from "@tanstack/react-query";
import { api } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { isTerminal } from "./OperationTracker.tsx";

export function useActiveOperations(targetId?: string) {
  const query = useQuery({
    queryKey: keys.operations.list(targetId),
    queryFn: ({ signal }) => api.operations.list(targetId, signal),
    refetchInterval: 2_000,
  });
  const operations = (query.data?.operations ?? []).filter(
    (op) => !isTerminal(op.state) && (!targetId || op.targetId === targetId),
  );
  return { ...query, operations, active: operations.length > 0 };
}
