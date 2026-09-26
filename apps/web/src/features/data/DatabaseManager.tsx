import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import type { DataOverview } from "@bento/shared";
import { Activity, Archive, ArchiveRestore, ChevronDown, Database, RefreshCw } from "lucide-react";
import { orpc } from "../../api/client.ts";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Badge } from "@/components/ui/badge";
import { Alert } from "@/components/ui/alert";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Card, CardContent } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";

type Service = DataOverview["services"][number];

export function DatabaseManager({ service, data }: { service: Service; data: DataOverview }) {
  const client = useQueryClient();
  const [expanded, setExpanded] = useState(false);
  const [artifact, setArtifact] = useState("");
  const [app, setApp] = useState("");
  const [target, setTarget] = useState("");
  const detailsId = `${service.service.replace(/[^a-zA-Z0-9_-]/g, "-")}-database-details`;
  const runtime = useQuery(
    orpc.data.runtime.queryOptions({
      input: { service: service.service, engine: service.engine },
    }),
  );
  const activity = useQuery({
    ...orpc.data.activity.queryOptions({
      input: { service: service.service, engine: service.engine },
    }),
    enabled: expanded,
    refetchInterval: expanded ? 3_000 : false,
  });
  const backup = useMutation(
    orpc.data.backup.mutationOptions({
      onSuccess: async () => {
        await client.invalidateQueries({ queryKey: orpc.data.overview.key() });
      },
    }),
  );
  const restore = useMutation(
    orpc.data.restore.mutationOptions({
      onSuccess: async () => {
        await client.invalidateQueries({ queryKey: orpc.data.overview.key() });
        await runtime.refetch();
        if (expanded) await activity.refetch();
      },
    }),
  );
  const bindings = data.bindings.filter((item) => item.engine === service.engine && item.service === service.service);
  const selectedBinding = bindings.find((item) => item.app === app);
  const backups = data.backups.filter((item) => item.name.startsWith(`${service.service}/`));
  const databases = [...(runtime.data?.databases ?? [])];
  for (const binding of bindings) {
    for (const name of binding.resources) {
      if (!databases.some((database) => database.name === name)) databases.push({ name, bytes: -1 });
    }
  }
  databases.sort((left, right) => left.name.localeCompare(right.name));
  const processes = activity.data?.processes ?? runtime.data?.processes ?? [];

  function restoreBackup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!artifact || !app || !target || selectedBinding?.resources.includes(target)) return;
    restore.mutate({
      app,
      engine: service.engine,
      artifact,
      targetDatabase: target,
      confirmation: target,
    });
  }

  return (
    <Card
      className={`overflow-hidden border-border py-0 shadow-sm ${service.engine === "mysql" ? "border-t-emerald-500/50" : "border-t-sky-500/50"}`}
    >
      <div className={`h-1 w-full ${service.engine === "mysql" ? "bg-emerald-500" : "bg-sky-500"}`} />
      <CardContent className="p-0">
        <div className="flex items-start justify-between gap-5 border-b border-border p-5 max-[760px]:flex-col">
          <div className="flex min-w-0 items-start gap-3">
            <span
              className={`grid size-11 shrink-0 place-items-center rounded-xl ${service.engine === "mysql" ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300" : "bg-sky-500/10 text-sky-700 dark:text-sky-300"}`}
              aria-hidden="true"
            >
              <Database className="size-5" />
            </span>
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <h3 className="m-0 truncate text-base font-semibold tracking-tight">{service.service}</h3>
                <Badge variant="outline">{engineLabel(service.engine)}</Badge>
              </div>
              <p className="m-0 mt-1 text-sm text-muted-foreground">
                Version {runtime.data?.serverVersion ?? service.version}
                {runtime.data?.serverVersion && runtime.data.serverVersion !== service.version ? " · Live runtime" : ""}
              </p>
            </div>
          </div>
          <div className="flex shrink-0 flex-wrap items-center gap-2 max-[760px]:w-full">
            <Button
              className="max-[760px]:flex-1"
              variant="ghost"
              size="sm"
              disabled={runtime.isFetching}
              onClick={() => void runtime.refetch()}
            >
              {runtime.isFetching ? <Spinner /> : <RefreshCw className="size-3.5" />}
              Refresh sizes
            </Button>
            <Button
              className="max-[760px]:flex-1"
              variant={expanded ? "secondary" : "outline"}
              size="sm"
              onClick={() => setExpanded((value) => !value)}
              aria-expanded={expanded}
              aria-controls={detailsId}
            >
              {expanded ? "Hide details" : "Details & restore"}
              <ChevronDown
                className={`size-4 transition-transform ${expanded ? "rotate-180" : ""}`}
                aria-hidden="true"
              />
            </Button>
          </div>
        </div>

        <div className="grid grid-cols-3 divide-x divide-border border-b border-border bg-muted/20 max-[560px]:grid-cols-1 max-[560px]:divide-x-0 max-[560px]:divide-y">
          <ManagerFact label="Databases" value={databases.length.toString()} />
          <ManagerFact
            label="Applications"
            value={`${service.appCount} connected`}
            tone={service.appCount ? "success" : "default"}
          />
          <ManagerFact label="Backup artifacts" value={backups.length.toString()} />
        </div>

        <div className="p-5">
          {runtime.error && (
            <Alert className="mb-4" variant="destructive">
              {messageOf(runtime.error)}
            </Alert>
          )}
          {runtime.data?.error && !expanded && (
            <Alert className="mb-4 border-amber-500/40 bg-amber-500/10 text-amber-800 dark:text-amber-200">
              {runtime.data.error}
            </Alert>
          )}

          <div className="mb-3 flex items-end justify-between gap-3">
            <div>
              <p className="m-0 text-[0.68rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
                Service inventory
              </p>
              <h4 className="m-0 mt-1 text-base font-semibold">Databases</h4>
            </div>
            <Badge variant="secondary">
              {databases.length} {databases.length === 1 ? "database" : "databases"}
            </Badge>
          </div>

          <div className="overflow-hidden rounded-xl border border-border">
            <Table className="min-w-[700px] bg-card">
              <TableHeader>
                <TableRow>
                  <TableHead>Database</TableHead>
                  <TableHead>Application</TableHead>
                  <TableHead className="text-right">Size</TableHead>
                  <TableHead className="w-px whitespace-nowrap text-right">
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {runtime.isPending && databases.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={4} className="h-24 text-center text-muted-foreground">
                      <Spinner /> Loading databases and sizes…
                    </TableCell>
                  </TableRow>
                ) : databases.length ? (
                  databases.map((database) => {
                    const owner = bindings.find((binding) => binding.resources.includes(database.name));
                    const backingUp = backup.isPending && backup.variables?.database === database.name;
                    return (
                      <TableRow key={database.name}>
                        <TableCell>
                          <div className="flex items-center gap-2">
                            <span className="grid size-7 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground">
                              <Database className="size-3.5" aria-hidden="true" />
                            </span>
                            <strong>{database.name}</strong>
                          </div>
                        </TableCell>
                        <TableCell>
                          {owner ? (
                            <Badge variant="outline">{owner.app}</Badge>
                          ) : (
                            <span className="text-sm text-muted-foreground">System database</span>
                          )}
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-right tabular-nums text-muted-foreground">
                          {database.bytes < 0 ? "Unavailable" : formatBytes(database.bytes)}
                        </TableCell>
                        <TableCell className="w-px whitespace-nowrap text-right">
                          <Button
                            variant="outline"
                            size="sm"
                            disabled={!owner || backup.isPending}
                            title={owner ? `Back up ${database.name}` : "Only application databases can be backed up"}
                            onClick={() =>
                              owner &&
                              backup.mutate({
                                app: owner.app,
                                database: database.name,
                                engine: service.engine,
                              })
                            }
                          >
                            {backingUp ? <Spinner /> : <Archive className="size-3.5" />}
                            {backingUp ? "Backing up" : "Backup"}
                          </Button>
                        </TableCell>
                      </TableRow>
                    );
                  })
                ) : (
                  <TableRow>
                    <TableCell colSpan={4} className="h-20 text-center text-muted-foreground">
                      No databases reported by this service.
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </div>

          {backup.data && (
            <Alert className="mt-4 border-emerald-600/40 bg-emerald-600/10 text-emerald-700 dark:text-emerald-400">
              <span>Backup created: {backup.data.artifacts.map((item) => item.name).join(", ")}</span>
            </Alert>
          )}
          {backup.error && (
            <Alert className="mt-4" variant="destructive">
              <span>{messageOf(backup.error)}</span>
            </Alert>
          )}
        </div>

        {expanded && (
          <div
            id={detailsId}
            className="grid gap-5 border-t border-border bg-muted/10 p-5 min-[1100px]:grid-cols-[minmax(0,1.15fr)_minmax(20rem,0.85fr)]"
          >
            <section className="min-w-0 rounded-xl border border-border bg-card p-4 shadow-sm">
              <div className="mb-3 flex items-start justify-between gap-3">
                <div>
                  <div className="mb-2 flex size-8 items-center justify-center rounded-lg bg-muted text-muted-foreground">
                    <Activity className="size-4" aria-hidden="true" />
                  </div>
                  <h4 className="m-0 text-base font-semibold">Active processes</h4>
                  <p className="m-0 mt-1 text-sm text-muted-foreground">
                    Current activity reported by the database runtime.
                  </p>
                </div>
                <div className="flex items-center gap-2">
                  {expanded && (
                    <Badge className="bg-emerald-600 text-white" title="Refreshing every 3 seconds">
                      Live · 3s
                    </Badge>
                  )}
                  <Badge variant="secondary">{processes.length}</Badge>
                </div>
              </div>
              {activity.error && (
                <Alert className="mb-3" variant="destructive">
                  {messageOf(activity.error)}
                </Alert>
              )}
              {activity.data?.error && (
                <Alert className="mb-3 border-amber-500/40 bg-amber-500/10 text-amber-800 dark:text-amber-200">
                  {activity.data.error}
                </Alert>
              )}
              <div className="overflow-hidden rounded-lg border border-border">
                <Table className="min-w-[600px] bg-card">
                  <TableHeader>
                    <TableRow>
                      <TableHead>User / database</TableHead>
                      <TableHead>State</TableHead>
                      <TableHead>Query</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {activity.isPending && !activity.data && !runtime.data?.processes.length ? (
                      <TableRow>
                        <TableCell colSpan={3} className="h-20 text-center text-muted-foreground">
                          <Spinner /> Loading active processes…
                        </TableCell>
                      </TableRow>
                    ) : processes.length ? (
                      processes.map((process) => (
                        <TableRow key={process.id}>
                          <TableCell>
                            <strong>{process.user}</strong>
                            <small className="mt-0.5 block text-muted-foreground">
                              {process.database || `Process ${process.id}`}
                            </small>
                          </TableCell>
                          <TableCell>
                            <Badge variant="secondary" className="text-[0.67rem]">
                              {process.state}
                            </Badge>
                          </TableCell>
                          <TableCell>
                            <code className="text-xs">{process.query}</code>
                          </TableCell>
                        </TableRow>
                      ))
                    ) : (
                      <TableRow>
                        <TableCell colSpan={3} className="h-20 text-center text-muted-foreground">
                          No other active processes.
                        </TableCell>
                      </TableRow>
                    )}
                  </TableBody>
                </Table>
              </div>
            </section>

            <section className="min-w-0 rounded-xl border border-border bg-card p-4 shadow-sm">
              <div className="mb-4 flex items-start gap-3">
                <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-amber-500/10 text-amber-700 dark:text-amber-300">
                  <ArchiveRestore className="size-4" aria-hidden="true" />
                </div>
                <div>
                  <h4 className="m-0 text-base font-semibold">Restore a backup</h4>
                  <p className="m-0 mt-1 text-sm text-muted-foreground">
                    Restore an artifact to an unused app-namespaced verification database.
                  </p>
                </div>
              </div>
              <form className="grid gap-4" onSubmit={restoreBackup}>
                <label className="grid gap-1.5">
                  <span className="text-sm font-medium">Backup artifact</span>
                  <NativeSelect value={artifact} onChange={(event) => setArtifact(event.target.value)}>
                    <NativeSelectOption value="">Select an artifact</NativeSelectOption>
                    {backups.map((item) => (
                      <NativeSelectOption key={item.name} value={item.name}>
                        {item.name} · {formatBytes(item.bytes)}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                  {!backups.length && (
                    <span className="text-xs text-muted-foreground">
                      Create a backup from the inventory above to restore it here.
                    </span>
                  )}
                </label>
                <div className="grid grid-cols-2 gap-3 max-[560px]:grid-cols-1">
                  <label className="grid gap-1.5">
                    <span className="text-sm font-medium">Application</span>
                    <NativeSelect
                      value={app}
                      onChange={(event) => {
                        setApp(event.target.value);
                        setTarget("");
                      }}
                    >
                      <NativeSelectOption value="">Select an application</NativeSelectOption>
                      {bindings.map((item) => (
                        <NativeSelectOption key={item.app} value={item.app}>
                          {item.app}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  </label>
                  <label className="grid gap-1.5">
                    <span className="text-sm font-medium">Target database</span>
                    <Input
                      value={target}
                      onChange={(event) => setTarget(event.target.value)}
                      placeholder={selectedBinding?.resources[0] ?? "database_name"}
                    />
                  </label>
                </div>
                <Alert className="border-amber-500/40 bg-amber-500/10 text-amber-800 dark:text-amber-200">
                  Existing database names are refused. The source must belong to the selected app and service. A failed
                  import may leave a partial new database; validate the result.
                </Alert>
                <Button
                  type="submit"
                  className="w-full bg-amber-500 text-amber-950 hover:bg-amber-500/90"
                  disabled={
                    !artifact || !app || !target || selectedBinding?.resources.includes(target) || restore.isPending
                  }
                >
                  {restore.isPending && <Spinner />}
                  Restore backup
                </Button>
                {restore.data && (
                  <Alert className="border-emerald-600/40 bg-emerald-600/10 text-emerald-700 dark:text-emerald-400">
                    <span>{restore.data.message}</span>
                  </Alert>
                )}
                {restore.error && <Alert variant="destructive">{messageOf(restore.error)}</Alert>}
              </form>
            </section>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function ManagerFact({
  label,
  value,
  tone = "default",
}: {
  label: string;
  value: string;
  tone?: "default" | "success";
}) {
  return (
    <div className="min-w-0 px-4 py-3">
      <span className="block text-[0.68rem] font-medium uppercase tracking-[0.1em] text-muted-foreground">{label}</span>
      <strong
        className={`mt-1 block truncate text-sm font-semibold ${tone === "success" ? "text-emerald-700 dark:text-emerald-300" : ""}`}
      >
        {value}
      </strong>
    </div>
  );
}

function engineLabel(engine: Service["engine"]) {
  return engine === "mysql" ? "MySQL" : "PostgreSQL";
}

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let value = bytes;
  let unit = -1;
  do {
    value /= 1024;
    unit++;
  } while (value >= 1024 && unit < units.length - 1);
  return `${value.toFixed(1)} ${units[unit]}`;
}

function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
