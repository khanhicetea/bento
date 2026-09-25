import { useQuery } from "@tanstack/react-query";
import type { Application } from "@bento/shared";
import { orpc } from "../../api/client.ts";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

type Props = { application: Application; onClose: () => void };

export function SchedulerInfoDialog({ application, onClose }: Props) {
  const access = useQuery(orpc.jobs.schedulerAccess.queryOptions({ input: {} }));
  const scheduler = access.data?.schedulers.find((item) => item.app === application.slug);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Scheduler for {application.slug}</DialogTitle>
          <DialogDescription>
            Minicrond owns this app’s jobs and workers in an app-isolated registry.
          </DialogDescription>
        </DialogHeader>
        {scheduler ? (
          <>
            <a href={scheduler.path} target="_blank" rel="noreferrer">
              Open scheduler in a new tab
            </a>
            <iframe
              className="h-[65vh] w-full rounded border"
              src={scheduler.path}
              title={`Scheduler for ${application.slug}`}
            />
          </>
        ) : (
          <>
            <p>{access.data?.reason ?? "Checking browser scheduler access…"}</p>
            <code className="block overflow-x-auto rounded bg-base-200 p-3 text-sm">
              bento app minicrond {application.slug} -- list
            </code>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
