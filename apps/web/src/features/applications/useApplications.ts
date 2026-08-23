import { useCallback, useEffect, useState } from "react";
import type { Application, ApplicationList } from "@bento/shared";
import { api } from "../../api/client.ts";

type ApplicationsState = {
  data: ApplicationList | null;
  error: string | null;
  loading: boolean;
  changing: string | null;
};

export function useApplications() {
  const [state, setState] = useState<ApplicationsState>({
    data: null,
    error: null,
    loading: true,
    changing: null,
  });

  const load = useCallback(async () => {
    setState((current) => ({ ...current, loading: true, error: null }));
    try {
      const data = await api.applications.list({});
      setState((current) => ({ ...current, data, loading: false }));
    } catch (error) {
      setState((current) => ({ ...current, error: messageOf(error), loading: false }));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const setEnabled = useCallback(async (application: Application) => {
    setState((current) => ({ ...current, changing: application.slug, error: null }));
    try {
      const updated = await api.applications.setEnabled({
        slug: application.slug,
        enabled: !application.enabled,
      });
      setState((current) => ({
        ...current,
        changing: null,
        data: current.data
          ? {
              ...current.data,
              applications: current.data.applications.map((item) =>
                item.slug === updated.slug ? updated : item,
              ),
            }
          : current.data,
      }));
    } catch (error) {
      setState((current) => ({ ...current, changing: null, error: messageOf(error) }));
    }
  }, []);

  return { ...state, reload: load, setEnabled };
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
