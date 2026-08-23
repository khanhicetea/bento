import { useMutation } from "@tanstack/react-query";
import { FileText, RotateCw } from "lucide-react";
import { useState } from "react";
import { orpc } from "../../api/client.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";

export function JobLogsButton({
  app,
  name,
  kind,
  disabled = false,
}: {
  app: string;
  name: string;
  kind: "cron" | "worker";
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const logs = useMutation(orpc.jobs.logs.mutationOptions());
  const label = kind === "cron" ? "scheduled job" : "worker";

  function loadLogs() {
    setOpen(true);
    logs.mutate({ app, name, kind });
  }

  return (
    <>
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={disabled || logs.isPending}
        onClick={loadLogs}
      >
        {logs.isPending ? <Spinner /> : <FileText className="size-3.5" aria-hidden="true" />}
        Logs
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] max-w-[1200px] gap-4 overflow-y-auto p-6 max-[640px]:p-4 sm:!max-w-[1200px]">
          <DialogHeader>
            <DialogTitle>{name} logs</DialogTitle>
            <DialogDescription>
              Showing the most recent 1000 lines from this {label}.
            </DialogDescription>
          </DialogHeader>
          {logs.error && <Alert variant="destructive">{messageOf(logs.error)}</Alert>}
          {logs.isPending && (
            <div className="flex min-h-48 items-center justify-center gap-3 text-sm text-muted-foreground">
              <Spinner className="size-5" /> Loading logs…
            </div>
          )}
          {!logs.isPending && logs.data && (
            <div className="overflow-hidden rounded-xl border border-border bg-muted/30">
              <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-3 text-xs text-muted-foreground">
                <span>{logs.data.lines.length} lines returned</span>
                {logs.data.truncated && <span>Output truncated</span>}
              </div>
              <pre
                className="max-h-[60vh] min-h-48 overflow-auto whitespace-pre-wrap break-words p-4 text-xs text-muted-foreground"
                aria-label={`Recent logs for ${name}`}
              >
                {logs.data.lines.join("\n") || "No log lines returned."}
              </pre>
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" disabled={logs.isPending} onClick={loadLogs}>
              <RotateCw className="size-4" aria-hidden="true" />
              Refresh logs
            </Button>
            <Button variant="ghost" onClick={() => setOpen(false)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
