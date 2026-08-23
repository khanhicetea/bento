import { useState, type FormEvent } from "react";
import type { AddApplicationDatabaseInput, Application, ApplicationList } from "@bento/shared";
import { Database, Plus, Server } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Badge } from "@/components/ui/badge";
import { Alert } from "@/components/ui/alert";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";

type ApplicationDatabasesDialogProps = {
  application: Application;
  settings: ApplicationList;
  error: string | null;
  adding: boolean;
  onClose: () => void;
  onAdd: (input: AddApplicationDatabaseInput) => Promise<Application>;
};

export function ApplicationDatabasesDialog({
  application,
  settings,
  error,
  adding,
  onClose,
  onAdd,
}: ApplicationDatabasesDialogProps) {
  const defaultSelection = settings.defaults
    ? `${settings.defaults.databaseEngine}:${settings.defaults.databaseService}`
    : "sqlite";
  const [selection, setSelection] = useState(defaultSelection);
  const relational = selection.startsWith("mysql:") || selection.startsWith("postgres:");

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const [engine, service] = selection.split(":") as [
      AddApplicationDatabaseInput["engine"],
      string | undefined,
    ];
    const databaseName = String(form.get("databaseName") ?? "").trim();
    try {
      await onAdd({
        slug: application.slug,
        engine,
        service,
        databaseName: databaseName || undefined,
      });
      onClose();
    } catch {
      // The mutation error is rendered in this dialog.
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !adding && onClose()}>
      <DialogContent
        className="w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] max-w-[960px] gap-0 overflow-y-auto p-0 sm:!max-w-[960px]"
        showCloseButton={!adding}
      >
        <div className="border-b border-border bg-muted/30 px-7 py-6 pr-16 max-[600px]:px-4 max-[600px]:py-5">
          <DialogHeader className="gap-3">
            <div className="flex items-center gap-3">
              <span className="grid size-11 shrink-0 place-items-center rounded-xl bg-primary/10 text-primary">
                <Database className="size-5" aria-hidden="true" />
              </span>
              <div className="min-w-0">
                <DialogTitle className="text-xl">Application databases</DialogTitle>
                <DialogDescription className="mt-1 flex flex-wrap items-center gap-1.5">
                  Manage data bindings for{" "}
                  <strong className="font-medium text-foreground">{application.slug}</strong>
                </DialogDescription>
              </div>
            </div>
          </DialogHeader>
        </div>

        <div className="space-y-7 px-7 py-6 max-[600px]:space-y-6 max-[600px]:px-4 max-[600px]:py-5">
          {error && <Alert variant="destructive">{error}</Alert>}

          <section aria-labelledby="current-databases-heading">
            <div className="mb-3 flex items-end justify-between gap-4">
              <div>
                <h3 id="current-databases-heading" className="m-0 text-base font-semibold">
                  Current bindings
                </h3>
                <p className="m-0 mt-1 text-sm text-muted-foreground">
                  Services and files currently available to this application.
                </p>
              </div>
              <Badge variant="secondary">
                {application.databases.length}{" "}
                {application.databases.length === 1 ? "binding" : "bindings"}
              </Badge>
            </div>
            <div className="grid gap-3">
              {application.databases.map((binding, index) => (
                <section
                  className="flex items-start justify-between gap-5 rounded-xl border border-border bg-card p-4 shadow-sm max-[600px]:flex-col"
                  key={`${binding.engine}:${binding.service ?? binding.file ?? index}`}
                >
                  <div className="flex min-w-0 items-start gap-3">
                    <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground">
                      <Server className="size-4" aria-hidden="true" />
                    </span>
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <strong className="text-sm">{databaseLabel(binding.engine)}</strong>
                        {index === 0 && (
                          <Badge className="px-1.5 py-0 text-[0.68rem]">Primary</Badge>
                        )}
                      </div>
                      <p className="m-0 mt-1 break-words text-sm text-muted-foreground">
                        {binding.service ?? binding.file ?? "Local application database"}
                      </p>
                    </div>
                  </div>
                  <div className="flex flex-wrap justify-end gap-1.5 max-[600px]:justify-start">
                    {binding.names.map((name) => (
                      <Badge variant="outline" key={name}>
                        {name}
                      </Badge>
                    ))}
                    {!binding.names.length && binding.file && (
                      <Badge variant="outline">SQLite file</Badge>
                    )}
                  </div>
                </section>
              ))}
              {!application.databases.length && (
                <div className="rounded-xl border border-dashed border-border bg-muted/30 px-4 py-6 text-center text-sm text-muted-foreground">
                  No database bindings yet. Add one below to connect storage to this application.
                </div>
              )}
            </div>
          </section>

          <form onSubmit={(event) => void submit(event)}>
            <fieldset
              disabled={adding}
              className="rounded-2xl border border-border bg-muted/30 p-5 max-[600px]:p-4"
            >
              <div className="mb-5 flex items-start gap-3">
                <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-card text-primary shadow-sm">
                  <Plus className="size-4" aria-hidden="true" />
                </span>
                <div>
                  <h3 className="m-0 text-base font-semibold">Attach a database</h3>
                  <p className="m-0 mt-1 text-sm text-muted-foreground">
                    Add a managed service, SQLite file, or Litestream-backed file.
                  </p>
                </div>
              </div>
              <div className="grid grid-cols-2 gap-4 max-[600px]:grid-cols-1">
                <label>
                  <span className="mb-1.5 block text-sm font-medium">
                    Engine or managed service
                  </span>
                  <NativeSelect
                    className="w-full bg-card"
                    value={selection}
                    onChange={(event) => setSelection(event.target.value)}
                  >
                    {settings.databaseServices.map((service) => (
                      <NativeSelectOption
                        key={`${service.engine}:${service.service}`}
                        value={`${service.engine}:${service.service}`}
                      >
                        {databaseLabel(service.engine)} {service.version} ({service.service})
                      </NativeSelectOption>
                    ))}
                    <NativeSelectOption value="sqlite">SQLite file</NativeSelectOption>
                    <NativeSelectOption value="litestream">
                      SQLite file with Litestream
                    </NativeSelectOption>
                  </NativeSelect>
                </label>
                {relational ? (
                  <label>
                    <span className="mb-1.5 block text-sm font-medium">
                      Database name{" "}
                      <small className="font-normal text-muted-foreground">(required)</small>
                    </span>
                    <Input
                      className="w-full bg-card"
                      name="databaseName"
                      required
                      pattern={`${application.slug}(_[A-Za-z0-9_]+)?`}
                      defaultValue={`${application.slug}_`}
                    />
                    <small className="mt-1.5 block text-xs text-muted-foreground">
                      Use {application.slug} or a name beginning with {application.slug}_
                    </small>
                  </label>
                ) : (
                  <div className="flex items-center rounded-lg border border-dashed border-border bg-card/50 px-3 text-sm text-muted-foreground">
                    SQLite creates an independent file for this application.
                  </div>
                )}
              </div>
              <p className="m-0 mt-4 border-t border-border pt-4 text-xs leading-relaxed text-muted-foreground">
                Selecting an existing service adds a database name to that binding. Selecting a new
                engine or service creates another binding. Existing data is never removed here.
              </p>
            </fieldset>
            <DialogFooter className="mt-5">
              <Button type="button" variant="ghost" disabled={adding} onClick={onClose}>
                Close
              </Button>
              <Button type="submit" disabled={adding}>
                {adding && <Spinner />}
                Add database
              </Button>
            </DialogFooter>
          </form>

          <p className="m-0 text-xs leading-relaxed text-muted-foreground">
            Database removal and permanent data deletion remain intentionally unavailable in the web
            control plane.
          </p>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function databaseLabel(engine: string): string {
  if (engine === "mysql") return "MySQL";
  if (engine === "postgres") return "PostgreSQL";
  if (engine === "litestream") return "SQLite + Litestream";
  return "SQLite";
}
