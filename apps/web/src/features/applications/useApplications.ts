import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  AddApplicationDatabaseInput,
  Application,
  ApplicationList,
  SaveApplicationInput,
} from "@bento/shared";
import { orpc } from "../../api/client.ts";

export function useApplications() {
  const queryClient = useQueryClient();
  const applicationsQuery = useQuery(orpc.applications.list.queryOptions({ input: {} }));
  const saveMutation = useMutation(
    orpc.applications.save.mutationOptions({
      onSuccess(updated) {
        queryClient.setQueryData<ApplicationList>(
          orpc.applications.list.queryKey({ input: {} }),
          (current) => upsertApplication(current, updated),
        );
      },
      onError() {
        // Applying the configuration can fail after the app has been persisted.
        // Refresh the list so the operator sees the saved app without a page reload.
        void queryClient.invalidateQueries({
          queryKey: orpc.applications.list.queryKey({ input: {} }),
        });
      },
    }),
  );
  const addDatabaseMutation = useMutation(
    orpc.applications.addDatabase.mutationOptions({
      onSuccess(updated) {
        queryClient.setQueryData<ApplicationList>(
          orpc.applications.list.queryKey({ input: {} }),
          (current) => upsertApplication(current, updated),
        );
      },
    }),
  );
  const setEnabledMutation = useMutation(
    orpc.applications.setEnabled.mutationOptions({
      onSuccess(updated) {
        queryClient.setQueryData<ApplicationList>(
          orpc.applications.list.queryKey({ input: {} }),
          (current) => upsertApplication(current, updated),
        );
      },
    }),
  );
  const setRunningMutation = useMutation(
    orpc.applications.setRunning.mutationOptions({
      onSuccess(updated) {
        queryClient.setQueryData<ApplicationList>(
          orpc.applications.list.queryKey({ input: {} }),
          (current) => upsertApplication(current, updated),
        );
      },
    }),
  );
  const removeMutation = useMutation(
    orpc.applications.remove.mutationOptions({
      onSuccess(removed) {
        queryClient.setQueryData<ApplicationList>(
          orpc.applications.list.queryKey({ input: {} }),
          (current) =>
            current
              ? {
                  ...current,
                  applications: current.applications.filter(
                    (application) => application.slug !== removed.slug,
                  ),
                }
              : current,
        );
      },
    }),
  );

  const error =
    applicationsQuery.error ??
    saveMutation.error ??
    addDatabaseMutation.error ??
    setEnabledMutation.error ??
    setRunningMutation.error ??
    removeMutation.error;

  function setEnabled(application: Application) {
    setEnabledMutation.mutate({
      slug: application.slug,
      enabled: !application.enabled,
    });
  }

  function setRunning(application: Application, action: "start" | "stop") {
    setRunningMutation.mutate({ slug: application.slug, action });
  }

  async function saveApplication(input: SaveApplicationInput) {
    return await saveMutation.mutateAsync(input);
  }

  async function addDatabase(input: AddApplicationDatabaseInput) {
    return await addDatabaseMutation.mutateAsync(input);
  }

  async function removeApplication(slug: string, confirmation: string) {
    return await removeMutation.mutateAsync({ slug, confirmation });
  }

  function resetErrors() {
    saveMutation.reset();
    addDatabaseMutation.reset();
    setEnabledMutation.reset();
    setRunningMutation.reset();
    removeMutation.reset();
  }

  return {
    data: applicationsQuery.data ?? null,
    error: error ? messageOf(error) : null,
    loading: applicationsQuery.isFetching,
    changing: setEnabledMutation.isPending
      ? (setEnabledMutation.variables?.slug ?? null)
      : setRunningMutation.isPending
        ? (setRunningMutation.variables?.slug ?? null)
        : null,
    saving: saveMutation.isPending,
    addingDatabase: addDatabaseMutation.isPending,
    removing: removeMutation.isPending,
    reload: applicationsQuery.refetch,
    setEnabled,
    setRunning,
    saveApplication,
    addDatabase,
    removeApplication,
    resetErrors,
  };
}

function upsertApplication(
  current: ApplicationList | undefined,
  updated: Application,
): ApplicationList | undefined {
  if (!current) return current;
  const exists = current.applications.some((application) => application.slug === updated.slug);
  const applications = exists
    ? current.applications.map((application) =>
        application.slug === updated.slug ? updated : application,
      )
    : [...current.applications, updated];
  applications.sort((left, right) => left.slug.localeCompare(right.slug));
  return { ...current, applications };
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
