import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { DataOverview } from "@bento/shared";
import { orpc } from "../../api/client.ts";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Badge } from "@/components/ui/badge";
import { Alert } from "@/components/ui/alert";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Card, CardContent, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";

type Service = DataOverview["services"][number];

export function DatabaseManager({ service, data }: { service: Service; data: DataOverview }) {
  const client = useQueryClient();
  const [expanded, setExpanded] = useState(false);
  const [artifact, setArtifact] = useState("");
  const [app, setApp] = useState("");
  const [target, setTarget] = useState("");
  const runtime = useQuery(
    orpc.data.runtime.queryOptions({
      input: { service: service.service, engine: service.engine },
    }),
  );
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
      },
    }),
  );
  const bindings = data.bindings.filter(
    (item) => item.engine === service.engine && item.service === service.service,
  );
  const selectedBinding = bindings.find((item) => item.app === app);
  const backups = data.backups.filter((item) => item.name.startsWith(`${service.service}/`));
  const databases = [...(runtime.data?.databases ?? [])];
  for (const binding of bindings) {
    for (const name of binding.resources) {
      if (!databases.some((database) => database.name === name))
        databases.push({ name, bytes: -1 });
    }
  }
  databases.sort((left, right) => left.name.localeCompare(right.name));

  return (
    <Card className="shadow-sm">
      <CardContent>
        <div className="flex items-center justify-between gap-4 max-[760px]:items-stretch max-[760px]:flex-col">
          <div>
            <div className="flex items-center gap-2.5">
              <CardTitle>{service.service}</CardTitle>
              <Badge variant="outline">{engineLabel(service.engine)}</Badge>
            </div>
            <p className="text-sm opacity-60">
              Version {runtime.data?.serverVersion ?? service.version} · {service.appCount}{" "}
              application{service.appCount === 1 ? "" : "s"}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2.5">
            <Button
              variant="ghost"
              size="sm"
              disabled={runtime.isFetching}
              onClick={() => void runtime.refetch()}
            >
              {runtime.isFetching && <Spinner />}
              Refresh sizes
            </Button>
            <Button
              variant={expanded ? "ghost" : "outline"}
              size="sm"
              onClick={() => setExpanded((value) => !value)}
              aria-expanded={expanded}
            >
              {expanded ? "Hide details" : "Details & restore"}
            </Button>
          </div>
        </div>

        {runtime.error && <Alert variant="destructive">{messageOf(runtime.error)}</Alert>}
        {runtime.data?.error && (
          <Alert className="border-amber-500/40 bg-amber-500/10">{runtime.data.error}</Alert>
        )}

        <div className="overflow-auto rounded-md border border-border">
          <Table className="min-w-[650px] bg-card">
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
                  <TableCell colSpan={4} className="h-20 text-center opacity-65">
                    <Spinner /> Loading databases and sizes…
                  </TableCell>
                </TableRow>
              ) : databases.length ? (
                databases.map((database) => {
                  const owner = bindings.find((binding) =>
                    binding.resources.includes(database.name),
                  );
                  const backingUp =
                    backup.isPending && backup.variables?.database === database.name;
                  return (
                    <TableRow key={database.name}>
                      <TableCell>
                        <strong>{database.name}</strong>
                      </TableCell>
                      <TableCell>
                        {owner?.app ?? <span className="text-sm opacity-60">System</span>}
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-right tabular-nums">
                        {database.bytes < 0 ? "Unavailable" : formatBytes(database.bytes)}
                      </TableCell>
                      <TableCell className="w-px whitespace-nowrap text-right">
                        <Button
                          size="sm"
                          disabled={!owner || backup.isPending}
                          title={
                            owner
                              ? `Back up ${database.name}`
                              : "Only application databases can be backed up"
                          }
                          onClick={() =>
                            owner &&
                            backup.mutate({
                              app: owner.app,
                              database: database.name,
                              engine: service.engine,
                            })
                          }
                        >
                          {backingUp && <Spinner />}
                          {backingUp ? "Backing up" : "Backup"}
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })
              ) : (
                <TableRow>
                  <TableCell colSpan={4}>No databases reported by this service.</TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>

        {backup.data && (
          <Alert className="border-emerald-600/40 bg-emerald-600/10 text-emerald-700 dark:text-emerald-400">
            <span>Backup created: {backup.data.artifacts.map((item) => item.name).join(", ")}</span>
          </Alert>
        )}
        {backup.error && (
          <Alert variant="destructive">
            <span>{messageOf(backup.error)}</span>
          </Alert>
        )}

        {expanded && (
          <div className="grid gap-5 border-t border-border pt-4">
            <Card className="bg-muted">
              <CardContent>
                <CardTitle>Active processes</CardTitle>
                <div className="overflow-auto rounded-[0.8rem] border border-border">
                  <Table className="min-w-[650px] bg-card">
                    <TableHeader>
                      <TableRow>
                        <TableHead>User / database</TableHead>
                        <TableHead>State</TableHead>
                        <TableHead>Query</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {runtime.data?.processes.length ? (
                        runtime.data.processes.map((process) => (
                          <TableRow key={process.id}>
                            <TableCell>
                              <strong>{process.user}</strong>
                              <small className="mt-0.5 block opacity-60">
                                {process.database || `Process ${process.id}`}
                              </small>
                            </TableCell>
                            <TableCell>
                              <Badge variant="secondary" className="text-[0.67rem]">
                                {process.state}
                              </Badge>
                            </TableCell>
                            <TableCell>
                              <code>{process.query}</code>
                            </TableCell>
                          </TableRow>
                        ))
                      ) : (
                        <TableRow>
                          <TableCell colSpan={3}>No other active processes.</TableCell>
                        </TableRow>
                      )}
                    </TableBody>
                  </Table>
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardContent>
                <div>
                  <CardTitle>Restore a backup</CardTitle>
                  <p className="text-sm opacity-60">
                    Restore an artifact to a new or existing application database.
                  </p>
                </div>
                <label className="w-full">
                  <span className="mt-3 mb-1.5 block font-semibold">Backup artifact</span>
                  <NativeSelect
                    className="w-full"
                    value={artifact}
                    onChange={(event) => setArtifact(event.target.value)}
                  >
                    <NativeSelectOption value="">Select an artifact</NativeSelectOption>
                    {backups.map((item) => (
                      <NativeSelectOption key={item.name} value={item.name}>
                        {item.name} · {formatBytes(item.bytes)}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </label>
                <div className="grid grid-cols-2 gap-4 max-[900px]:grid-cols-1">
                  <label className="w-full">
                    <span className="mt-3 mb-1.5 block font-semibold">Application</span>
                    <NativeSelect
                      className="w-full"
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
                  <label className="w-full">
                    <span className="mt-3 mb-1.5 block font-semibold">Target database</span>
                    <Input
                      className="w-full"
                      value={target}
                      onChange={(event) => setTarget(event.target.value)}
                      placeholder={selectedBinding?.resources[0] ?? "database_name"}
                    />
                  </label>
                </div>
                <Alert className="border-amber-500/40 bg-amber-500/10">
                  <span>
                    Restoring to an existing name overwrites that database. The target name is used
                    as confirmation.
                  </span>
                </Alert>
                <div className="flex justify-end gap-2">
                  <Button
                    className="bg-amber-500 text-amber-950 hover:bg-amber-500/90"
                    disabled={!artifact || !app || !target || restore.isPending}
                    onClick={() =>
                      restore.mutate({
                        app,
                        engine: service.engine,
                        artifact,
                        targetDatabase: target,
                        confirmation: target,
                      })
                    }
                  >
                    {restore.isPending && <Spinner />}
                    Restore backup
                  </Button>
                </div>
                {restore.data && (
                  <Alert className="border-emerald-600/40 bg-emerald-600/10 text-emerald-700 dark:text-emerald-400">
                    <span>{restore.data.message}</span>
                  </Alert>
                )}
                {restore.error && (
                  <Alert variant="destructive">
                    <span>{messageOf(restore.error)}</span>
                  </Alert>
                )}
              </CardContent>
            </Card>
          </div>
        )}
      </CardContent>
    </Card>
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
