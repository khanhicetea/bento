import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import type { AddApplicationDatabaseInput, Application, ApplicationList } from "@bento/shared";
import { Database, Eye, EyeOff, Plus, Server } from "lucide-react";
import { orpc } from "../../api/client.ts";
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
import { Table, TableBody, TableCell, TableRow } from "@/components/ui/table";
import { Spinner } from "@/components/ui/spinner";

type ApplicationDatabasesDialogProps = {
  application: Application;
  settings: ApplicationList;
  error: string | null;
  adding: boolean;
  onClose: () => void;
  onAdd: (input: AddApplicationDatabaseInput) => Promise<Application>;
};

type DatabaseBinding = Application["databases"][number];
type DatabaseGroup = {
  key: string;
  label: string;
  service?: string;
  engine: DatabaseBinding["engine"];
  bindings: Array<{ binding: DatabaseBinding; primary: boolean }>;
};
type CredentialsTarget = {
  key: string;
  engine: "mysql" | "postgres";
  service: string;
};

export function ApplicationDatabasesDialog({
  application,
  settings,
  error,
  adding,
  onClose,
  onAdd,
}: ApplicationDatabasesDialogProps) {
  const client = useQueryClient();
  const backup = useMutation(
    orpc.data.backup.mutationOptions({
      onSuccess: async () => {
        await client.invalidateQueries({ queryKey: orpc.data.overview.key() });
      },
    }),
  );
  const defaultSelection = settings.defaults
    ? `${settings.defaults.databaseEngine}:${settings.defaults.databaseService}`
    : "sqlite";
  const [selection, setSelection] = useState(defaultSelection);
  const relational = selection.startsWith("mysql:") || selection.startsWith("postgres:");
  const groups = groupDatabases(application, settings);
  const [credentialsTarget, setCredentialsTarget] = useState<CredentialsTarget | null>(null);
  const credentials = useMutation(orpc.applications.databaseCredentials.mutationOptions());

  function toggleCredentials(group: DatabaseGroup) {
    if (!isManagedEngine(group.engine) || !group.service) return;
    if (credentialsTarget?.key === group.key) {
      setCredentialsTarget(null);
      credentials.reset();
      return;
    }
    const target = { key: group.key, engine: group.engine, service: group.service };
    setCredentialsTarget(target);
    credentials.reset();
    credentials.mutate({
      slug: application.slug,
      engine: target.engine,
      service: target.service,
    });
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const [engine, service] = selection.split(":") as [
      AddApplicationDatabaseInput["engine"],
      string | undefined,
    ];
    const databasePart = String(form.get("databaseName") ?? "").trim();
    const databaseName =
      relational && databasePart
        ? `${application.slug}_${databasePart}`
        : relational
          ? application.slug
          : undefined;
    try {
      await onAdd({
        slug: application.slug,
        engine,
        service,
        databaseName,
      });
      onClose();
    } catch {
      // The mutation error is rendered in this dialog.
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !adding && onClose()}>
      <DialogContent
        className="w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] max-w-[860px] gap-0 overflow-y-auto p-0 sm:!max-w-[860px]"
        showCloseButton={!adding}
      >
        <div className="border-b border-border bg-muted/30 px-7 py-5 pr-16 max-[600px]:px-4 max-[600px]:py-4">
          <DialogHeader className="gap-2">
            <div className="flex items-center gap-3">
              <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-primary/10 text-primary">
                <Database className="size-5" aria-hidden="true" />
              </span>
              <div className="min-w-0">
                <DialogTitle className="text-xl">Application databases</DialogTitle>
                <DialogDescription className="mt-1">
                  Manage data bindings for{" "}
                  <strong className="font-medium text-foreground">{application.slug}</strong>
                </DialogDescription>
              </div>
            </div>
          </DialogHeader>
        </div>

        <div className="space-y-5 px-7 py-5 max-[600px]:space-y-4 max-[600px]:px-4 max-[600px]:py-4">
          {error && <Alert variant="destructive">{error}</Alert>}
          {backup.error && <Alert variant="destructive">{messageOf(backup.error)}</Alert>}
          {backup.data && (
            <Alert className="border-emerald-600/40 bg-emerald-600/10 text-emerald-700 dark:text-emerald-400">
              Backup created: {backup.data.artifacts.map((item) => item.name).join(", ")}
            </Alert>
          )}

          <form onSubmit={(event) => void submit(event)}>
            <fieldset
              disabled={adding}
              className="rounded-xl border border-border bg-muted/30 p-3.5 max-[600px]:p-3"
            >
              <div className="mb-3 flex items-center gap-2.5">
                <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-card text-primary shadow-sm">
                  <Plus className="size-4" aria-hidden="true" />
                </span>
                <div>
                  <h3 className="m-0 text-sm font-semibold">Add a database</h3>
                  <p className="m-0 mt-0.5 text-xs text-muted-foreground">
                    Choose a managed service or local file.
                  </p>
                </div>
              </div>
              <div
                className={`grid gap-3 ${relational ? "sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]" : "sm:grid-cols-[minmax(0,1fr)_auto]"}`}
              >
                <label>
                  <span className="sr-only">Database service</span>
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
                    <NativeSelectOption value="litestream">SQLite + Litestream</NativeSelectOption>
                  </NativeSelect>
                </label>
                {relational ? (
                  <label>
                    <span className="sr-only">Database name</span>
                    <div className="flex min-w-0">
                      <span className="flex shrink-0 items-center rounded-l-md border border-r-0 border-border bg-muted px-2 text-sm text-muted-foreground">
                        {application.slug}_
                      </span>
                      <Input
                        className="min-w-0 rounded-l-none bg-card"
                        name="databaseName"
                        pattern="[A-Za-z0-9_]*"
                        placeholder="database_name"
                        aria-label="Database name suffix"
                      />
                    </div>
                  </label>
                ) : (
                  <p className="m-0 self-center text-xs text-muted-foreground sm:hidden">
                    Creates an independent file for this application.
                  </p>
                )}
                <Button type="submit" className="sm:self-start">
                  {adding && <Spinner />}
                  Add database
                </Button>
              </div>
            </fieldset>
          </form>

          <section aria-labelledby="databases-heading">
            <h3 id="databases-heading" className="mb-3 text-base font-semibold">
              Databases
            </h3>
            <div className="grid gap-3">
              {groups.map((group) => (
                <section
                  className="overflow-hidden rounded-xl border border-border bg-card shadow-sm"
                  key={group.key}
                >
                  <div className="flex items-center justify-between gap-3 border-b border-border bg-muted/30 px-4 py-3">
                    <div className="flex min-w-0 items-center gap-2.5">
                      <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground">
                        <Server className="size-4" aria-hidden="true" />
                      </span>
                      <div className="min-w-0">
                        <h4 className="m-0 truncate text-sm font-semibold">{group.label}</h4>
                        {group.service && (
                          <p className="m-0 mt-0.5 truncate text-xs text-muted-foreground">
                            {group.service}
                          </p>
                        )}
                      </div>
                    </div>
                    <div className="flex shrink-0 items-center gap-2">
                      {isManagedEngine(group.engine) && group.service && (
                        <Button
                          type="button"
                          size="sm"
                          variant={credentialsTarget?.key === group.key ? "secondary" : "outline"}
                          onClick={() => toggleCredentials(group)}
                          aria-expanded={credentialsTarget?.key === group.key}
                        >
                          {credentialsTarget?.key === group.key ? (
                            <EyeOff className="size-3.5" aria-hidden="true" />
                          ) : (
                            <Eye className="size-3.5" aria-hidden="true" />
                          )}
                          {credentialsTarget?.key === group.key
                            ? "Hide credentials"
                            : "Show credentials"}
                        </Button>
                      )}
                      {group.bindings.some((item) => item.primary) && (
                        <Badge className="shrink-0 px-1.5 py-0 text-[0.68rem]">Primary</Badge>
                      )}
                    </div>
                  </div>
                  <div className="overflow-auto">
                    <Table className="min-w-[460px]">
                      <TableBody>
                        {group.bindings.flatMap(({ binding }) => {
                          if (isManagedEngine(binding.engine)) {
                            const engine = binding.engine;
                            return binding.names.map((name) => {
                              const backingUp =
                                backup.isPending && backup.variables?.database === name;
                              return (
                                <TableRow key={`${binding.engine}:${binding.service}:${name}`}>
                                  <TableCell>
                                    <strong>{name}</strong>
                                  </TableCell>
                                  <TableCell className="w-px whitespace-nowrap text-right">
                                    <Button
                                      type="button"
                                      size="sm"
                                      variant="outline"
                                      disabled={backup.isPending}
                                      onClick={() =>
                                        backup.mutate({
                                          app: application.slug,
                                          database: name,
                                          engine,
                                        })
                                      }
                                    >
                                      {backingUp && <Spinner />}
                                      {backingUp ? "Backing up" : "Backup"}
                                    </Button>
                                  </TableCell>
                                </TableRow>
                              );
                            });
                          }

                          if (binding.engine === "sqlite") {
                            const database = binding.file ?? "Local application database";
                            const backingUp =
                              backup.isPending && backup.variables?.database === database;
                            return (
                              <TableRow key={`${binding.engine}:${database}`}>
                                <TableCell>
                                  <code>{database}</code>
                                </TableCell>
                                <TableCell className="w-px whitespace-nowrap text-right">
                                  <Button
                                    type="button"
                                    size="sm"
                                    variant="outline"
                                    disabled={backup.isPending || !binding.file}
                                    onClick={() =>
                                      binding.file &&
                                      backup.mutate({
                                        app: application.slug,
                                        database: binding.file,
                                        engine: "sqlite",
                                      })
                                    }
                                  >
                                    {backingUp && <Spinner />}
                                    {backingUp ? "Backing up" : "Backup"}
                                  </Button>
                                </TableCell>
                              </TableRow>
                            );
                          }

                          return (
                            <TableRow
                              key={`${binding.engine}:${binding.service ?? binding.file ?? "local"}`}
                            >
                              <TableCell>
                                <code>{binding.file ?? "Local application database"}</code>
                              </TableCell>
                            </TableRow>
                          );
                        })}
                      </TableBody>
                    </Table>
                  </div>
                  {credentialsTarget?.key === group.key && (
                    <div
                      className="border-t border-border bg-muted/20 px-4 py-3"
                      aria-live="polite"
                    >
                      {credentials.isPending && !credentials.data && (
                        <div className="flex items-center gap-2 text-sm text-muted-foreground">
                          <Spinner /> Loading credentials…
                        </div>
                      )}
                      {credentials.error && (
                        <Alert variant="destructive">{messageOf(credentials.error)}</Alert>
                      )}
                      {credentials.data && (
                        <div>
                          <p className="m-0 text-xs text-muted-foreground">
                            App-scoped credentials. Keep the password secret.
                          </p>
                          <dl className="mt-3 grid gap-3 sm:grid-cols-2">
                            <CredentialField label="DB host" value={credentials.data.host} />
                            <CredentialField
                              label="DB port"
                              value={String(credentials.data.port)}
                            />
                            <CredentialField label="DB user" value={credentials.data.user} />
                            <CredentialField
                              label="DB password"
                              value={credentials.data.password}
                              secret
                            />
                            <CredentialField
                              label="DB name(s)"
                              value={credentials.data.databases.join(", ") || "None"}
                            />
                          </dl>
                        </div>
                      )}
                    </div>
                  )}
                </section>
              ))}
              {!groups.length && (
                <div className="rounded-xl border border-dashed border-border bg-muted/30 px-4 py-6 text-center text-sm text-muted-foreground">
                  No databases attached yet.
                </div>
              )}
            </div>
          </section>

          <DialogFooter className="mt-1">
            <Button type="button" variant="ghost" disabled={adding} onClick={onClose}>
              Close
            </Button>
          </DialogFooter>

          <p className="m-0 text-xs leading-relaxed text-muted-foreground">
            Database removal and permanent data deletion remain unavailable in the web control
            plane.
          </p>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function groupDatabases(application: Application, settings: ApplicationList): DatabaseGroup[] {
  const groups = new Map<string, DatabaseGroup>();

  application.databases.forEach((binding, index) => {
    const managed =
      binding.engine === "mysql" || binding.engine === "postgres"
        ? settings.databaseServices.find(
            (service) => service.engine === binding.engine && service.service === binding.service,
          )
        : undefined;
    const key = managed ? `${binding.engine}:${binding.service}` : binding.engine;
    const label = managed
      ? `${databaseLabel(binding.engine)} ${managed.version}`
      : databaseLabel(binding.engine);
    const service = managed?.service;
    const group = groups.get(key) ?? {
      key,
      label,
      service,
      engine: binding.engine,
      bindings: [],
    };
    group.bindings.push({ binding, primary: index === 0 });
    groups.set(key, group);
  });

  return [...groups.values()];
}

function isManagedEngine(engine: DatabaseBinding["engine"]): engine is "mysql" | "postgres" {
  return engine === "mysql" || engine === "postgres";
}

function databaseLabel(engine: string): string {
  if (engine === "mysql") return "MySQL";
  if (engine === "postgres") return "PostgreSQL";
  if (engine === "litestream") return "SQLite + Litestream";
  return "SQLite";
}

function CredentialField({
  label,
  value,
  secret = false,
}: {
  label: string;
  value: string;
  secret?: boolean;
}) {
  const [revealed, setRevealed] = useState(false);
  const displayedValue = value;

  return (
    <div className="min-w-0">
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className="m-0 mt-1 flex min-w-0 items-center gap-1">
        {secret ? (
          <input
            type={revealed ? "text" : "password"}
            value={value}
            readOnly
            aria-label={label}
            className="h-8 min-w-0 flex-1 rounded-md border border-input bg-muted px-2 py-1 text-xs outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/50"
          />
        ) : (
          <code className="min-w-0 flex-1 break-all rounded-md bg-muted px-2 py-1.5 text-xs">
            {displayedValue}
          </code>
        )}
        {secret && (
          <>
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label={revealed ? `Hide ${label}` : `Show ${label}`}
              title={revealed ? `Hide ${label}` : `Show ${label}`}
              onClick={() => setRevealed((current) => !current)}
            >
              {revealed ? (
                <EyeOff className="size-3.5" aria-hidden="true" />
              ) : (
                <Eye className="size-3.5" aria-hidden="true" />
              )}
            </Button>
          </>
        )}
      </dd>
    </div>
  );
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
