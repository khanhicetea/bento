import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { Application, ApplicationList } from "@bento/shared";
import { orpc } from "../../api/client.ts";

export function useApplications() {
  const queryClient = useQueryClient();
  const applicationsQuery = useQuery(orpc.applications.list.queryOptions({ input: {} }));
  const setEnabledMutation = useMutation(
    orpc.applications.setEnabled.mutationOptions({
      onSuccess(updated) {
        queryClient.setQueryData<ApplicationList>(
          orpc.applications.list.queryKey({ input: {} }),
          (current) =>
            current
              ? {
                  ...current,
                  applications: current.applications.map((item) =>
                    item.slug === updated.slug ? updated : item,
                  ),
                }
              : current,
        );
      },
    }),
  );

  const error = applicationsQuery.error ?? setEnabledMutation.error;

  function setEnabled(application: Application) {
    setEnabledMutation.mutate({
      slug: application.slug,
      enabled: !application.enabled,
    });
  }

  return {
    data: applicationsQuery.data ?? null,
    error: error ? messageOf(error) : null,
    loading: applicationsQuery.isFetching,
    changing: setEnabledMutation.isPending ? (setEnabledMutation.variables?.slug ?? null) : null,
    reload: applicationsQuery.refetch,
    setEnabled,
  };
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
