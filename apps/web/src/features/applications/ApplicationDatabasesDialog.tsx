import { useState, type FormEvent } from "react";
import type { AddApplicationDatabaseInput, Application, ApplicationList } from "@bento/shared";
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
      // The TanStack mutation exposes the sanitized oRPC error in the dialog.
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !adding && onClose()}>
      <DialogContent
        className="app-databases-box max-h-[calc(100vh-2rem)] overflow-y-auto"
        showCloseButton={!adding}
      >
        <DialogHeader>
          <DialogTitle>{application.slug} databases</DialogTitle>
          <DialogDescription>
            An application can own multiple engine bindings and multiple names on each relational
            binding.
          </DialogDescription>
        </DialogHeader>
        {error && <Alert variant="destructive">{error}</Alert>}

        <div className="database-binding-list">
          {application.databases.map((binding, index) => (
            <section
              className="database-binding"
              key={`${binding.engine}:${binding.service ?? binding.file ?? index}`}
            >
              <div>
                <strong>{databaseLabel(binding.engine)}</strong>
                <small>{binding.service ?? binding.file ?? "Local application database"}</small>
              </div>
              <div className="database-names">
                {binding.names.map((name) => (
                  <Badge variant="outline" key={name}>
                    {name}
                  </Badge>
                ))}
                {!binding.names.length && binding.file && (
                  <Badge variant="outline">SQLite file</Badge>
                )}
                {index === 0 && <Badge>primary</Badge>}
              </div>
            </section>
          ))}
        </div>

        <form onSubmit={(event) => void submit(event)}>
          <fieldset disabled={adding}>
            <div className="form-section">
              <h3>Add a database</h3>
              <label>
                <span className="label-text">Engine or managed service</span>
                <NativeSelect
                  className="w-full"
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
              {relational && (
                <label>
                  <span className="label-text">
                    Database name{" "}
                    <small>
                      {application.slug} or {application.slug}_*
                    </small>
                  </span>
                  <Input
                    className="w-full"
                    name="databaseName"
                    required
                    pattern={`${application.slug}(_[A-Za-z0-9_]+)?`}
                    defaultValue={`${application.slug}_`}
                  />
                </label>
              )}
              <p className="form-help">
                Selecting an existing service adds a database name to that binding. Selecting a new
                engine or service creates another binding. SQLite selections create independent
                files.
              </p>
            </div>
          </fieldset>
          <DialogFooter>
            <Button type="button" variant="ghost" disabled={adding} onClick={onClose}>
              Close
            </Button>
            <Button type="submit" disabled={adding}>
              {adding && <Spinner />}
              Add database
            </Button>
          </DialogFooter>
        </form>
        <p className="form-help">
          Database removal and permanent data deletion remain intentionally unavailable in the web
          control plane.
        </p>
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
