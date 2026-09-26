import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { RoutingProxy, SaveRoutingProxyInput } from "@bento/shared";
import { orpc } from "../../api/client.ts";

export function useRouting() {
  const queryClient = useQueryClient();
  const overviewKey = orpc.routing.overview.queryKey({ input: {} });
  const query = useQuery(orpc.routing.overview.queryOptions({ input: {} }));
  const saveMutation = useMutation(
    orpc.routing.saveProxy.mutationOptions({
      onSuccess: async () => await queryClient.invalidateQueries({ queryKey: overviewKey }),
    }),
  );
  const enableMutation = useMutation(
    orpc.routing.setProxyEnabled.mutationOptions({
      onSuccess: async () => await queryClient.invalidateQueries({ queryKey: overviewKey }),
    }),
  );
  const removeMutation = useMutation(
    orpc.routing.removeProxy.mutationOptions({
      onSuccess: async () => await queryClient.invalidateQueries({ queryKey: overviewKey }),
    }),
  );
  const error = query.error ?? saveMutation.error ?? enableMutation.error ?? removeMutation.error;

  return {
    query,
    error: error ? messageOf(error) : null,
    saving: saveMutation.isPending,
    removing: removeMutation.isPending,
    changing: enableMutation.isPending ? (enableMutation.variables?.name ?? null) : null,
    saveProxy: async (input: SaveRoutingProxyInput) => await saveMutation.mutateAsync(input),
    setProxyEnabled: (proxy: RoutingProxy) => enableMutation.mutate({ name: proxy.name, enabled: !proxy.enabled }),
    removeProxy: async (name: string, confirmation: string) => await removeMutation.mutateAsync({ name, confirmation }),
    resetErrors() {
      saveMutation.reset();
      enableMutation.reset();
      removeMutation.reset();
    },
  };
}

function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
