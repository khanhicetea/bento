import { useQuery } from "@tanstack/react-query";
import { orpc } from "../../api/client.ts";

/** User definitions belong to minicrond, never to Bento's desired state. */
export function SchedulerPage() {
  const apps = useQuery(orpc.applications.list.queryOptions({ input: {} }));
  const access = useQuery(orpc.jobs.schedulerAccess.queryOptions({ input: {} }));
  return (
    <main className="space-y-5 p-6">
      <h1 className="text-2xl font-semibold">App schedulers</h1>
      <p>
        Each enabled PHP app has its own minicrond registry for jobs, workers, runs, and logs. Bento no longer edits
        user definitions.
      </p>
      {access.data && !access.data.enabled && <p className="text-sm opacity-75">{access.data.reason}</p>}
      {access.isError && <p role="alert">Unable to determine browser scheduler access.</p>}
      {apps.isPending && <p>Loading applications…</p>}
      {apps.isError && <p role="alert">Unable to load applications.</p>}
      {apps.data?.applications
        .filter((app) => app.kind === "php" && app.enabled)
        .map((app) => {
          const scheduler = access.data?.schedulers.find((item) => item.app === app.slug);
          return (
            <section key={app.slug} className="space-y-3 rounded-lg border p-4">
              <div className="flex items-center justify-between gap-3">
                <h2 className="font-medium">{app.slug}</h2>
                {scheduler && (
                  <a href={scheduler.path} target="_blank" rel="noreferrer">
                    Open scheduler
                  </a>
                )}
              </div>
              {scheduler ? (
                <iframe
                  className="h-[70vh] w-full rounded border"
                  src={scheduler.path}
                  title={`Scheduler for ${app.slug}`}
                />
              ) : (
                <>
                  <code className="block overflow-x-auto text-sm">bento app minicrond {app.slug} -- list</code>
                  <code className="block overflow-x-auto text-sm">bento app minicrond {app.slug} -- status</code>
                </>
              )}
            </section>
          );
        })}
    </main>
  );
}
