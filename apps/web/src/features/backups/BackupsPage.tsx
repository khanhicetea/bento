import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent, type ReactNode } from "react";
import { Archive, ArchiveRestore, Database, FileArchive, HardDrive, Info, RefreshCw, Search } from "lucide-react";
import type { DataOverview } from "@bento/shared";
import { orpc } from "../../api/client.ts";
import { BackupRunCard } from "./BackupRunCard.tsx";
import { DomainError, DomainLoading, StackNotReady } from "../../components/DomainState.tsx";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

type Artifact = DataOverview["backups"][number];
type Filter = "all" | "relational" | "manual";

export function BackupsPage() {
  const client = useQueryClient();
  const overview = useQuery({ ...orpc.data.overview.queryOptions({ input: {} }), refetchInterval: 10_000 });
  const runs = useQuery(orpc.data.backupRuns.queryOptions({ input: {}, refetchInterval: 3_000 }));
  const start = useMutation(
    orpc.data.startBackup.mutationOptions({
      onSuccess: async () => {
        await client.invalidateQueries({ queryKey: orpc.data.backupRuns.key() });
      },
    }),
  );
  const restore = useMutation(
    orpc.data.restore.mutationOptions({
      onSuccess: async () => {
        await client.invalidateQueries({ queryKey: orpc.data.overview.key() });
      },
    }),
  );
  const [scope, setScope] = useState("");
  const [selected, setSelected] = useState("");
  const [app, setApp] = useState("");
  const [target, setTarget] = useState("");
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const data = overview.data;
  const active = runs.data?.runs.some((run) => run.status === "running") ?? false;
  const apps = [...new Set(data?.bindings.map((binding) => binding.app) ?? [])].sort();
  const artifacts = [...(data?.backups ?? [])].sort(
    (a, b) => (b.modifiedAt ?? "").localeCompare(a.modifiedAt ?? "") || a.name.localeCompare(b.name),
  );
  const relationalCount = artifacts.filter((item) => restoreSource(item, data).length > 0).length;
  const visible = artifacts.filter((item) => {
    const relational = restoreSource(item, data).length > 0;
    return (
      (filter === "all" || (filter === "relational") === relational) &&
      item.name.toLowerCase().includes(search.trim().toLowerCase())
    );
  });
  const artifact = data?.backups.find((item) => item.name === selected);
  const source = artifact ? restoreSource(artifact, data) : [];
  const engine = data?.services.find((service) => service.service === source[0])?.engine;
  const compatible =
    data?.bindings.filter(
      (binding) =>
        binding.engine === engine && binding.service === source[0] && binding.resources.includes(source[1] ?? ""),
    ) ?? [];
  const compatibleApps = [...new Set(compatible.map((binding) => binding.app))];
  const selectedBinding = compatible.find((binding) => binding.app === app);
  const validTarget = Boolean(
    selectedBinding &&
      target &&
      (target === app || target.startsWith(`${app}_`)) &&
      /^[a-zA-Z0-9_]+$/.test(target) &&
      !data?.bindings.some(
        (binding) => binding.service === selectedBinding.service && binding.resources.includes(target),
      ),
  );

  function openRestore(name: string) {
    const [service, database] = name.split("/");
    const owners = [
      ...new Set(
        data?.bindings
          .filter(
            (binding) =>
              binding.engine !== "sqlite" && binding.service === service && binding.resources.includes(database ?? ""),
          )
          .map((binding) => binding.app) ?? [],
      ),
    ];
    const owner = owners.length === 1 ? owners[0]! : "";
    setSelected(name);
    setApp(owner);
    setTarget(owner ? suggestedTarget(owner, service ?? "", data) : "");
    restore.reset();
  }

  function closeRestore() {
    if (restore.isPending) return;
    setSelected("");
    restore.reset();
  }

  function submitRestore(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!artifact || !engine || !validTarget || restore.isPending) return;
    restore.mutate({ artifact: artifact.name, app, engine, targetDatabase: target, confirmation: target });
  }

  if (!data && overview.isPending) {
    return (
      <div className="mx-auto max-w-[1500px] p-6">
        <DomainLoading label="backups" />
      </div>
    );
  }
  if (!data && overview.error) {
    return (
      <div className="mx-auto max-w-[1500px] p-6">
        <DomainError
          message={`Could not load backups: ${overview.error.message}`}
          onRetry={() => void overview.refetch()}
        />
      </div>
    );
  }
  if (!data) return null;

  return (
    <section className="mx-auto w-full max-w-[1500px] p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
      <header className="flex flex-wrap items-end justify-between gap-5">
        <div>
          <p className="mb-2 text-[0.68rem] font-semibold uppercase tracking-[0.16em] text-muted-foreground">
            Recovery / Data
          </p>
          <h2 className="m-0 text-[clamp(1.7rem,3vw,2.35rem)] font-semibold tracking-tight">Backups</h2>
          <p className="mb-0 mt-2 max-w-[680px] text-sm text-muted-foreground">
            Create local copies, monitor backup runs, and restore relational dumps into new databases.
          </p>
        </div>
        <Button
          className="max-[560px]:w-full"
          variant="outline"
          disabled={overview.isFetching || runs.isFetching}
          onClick={() => {
            void overview.refetch();
            void runs.refetch();
          }}
        >
          {overview.isFetching || runs.isFetching ? <Spinner /> : <RefreshCw className="size-4" aria-hidden="true" />}
          Refresh status
        </Button>
      </header>

      {overview.error && (
        <Alert className="mt-6" variant="destructive">
          Inventory may be out of date: {overview.error.message}
        </Alert>
      )}
      {!data.initialized ? (
        <div className="mt-8">
          <StackNotReady stackRoot={data.stackRoot} error={data.error} />
        </div>
      ) : (
        <>
          <div className="mt-8 grid gap-3 sm:grid-cols-3">
            <Metric
              icon={<FileArchive className="size-4" />}
              label="Local artifacts"
              value={String(artifacts.length)}
            />
            <Metric
              icon={<ArchiveRestore className="size-4" />}
              label="Relational restore options"
              value={String(relationalCount)}
            />
            <Metric
              icon={<HardDrive className="size-4" />}
              label="Stored locally"
              value={formatBytes(artifacts.reduce((sum, item) => sum + item.bytes, 0))}
            />
          </div>

          <div className="mt-5 grid items-stretch gap-5 lg:grid-cols-2">
            <section
              className="flex flex-col rounded-2xl border border-border bg-card p-5 shadow-sm sm:p-6"
              aria-labelledby="new-backup-title"
            >
              <div className="flex items-start gap-3">
                <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-primary text-primary-foreground">
                  <Archive className="size-5" aria-hidden="true" />
                </span>
                <div>
                  <p className="m-0 text-[0.68rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
                    On demand
                  </p>
                  <h3 id="new-backup-title" className="m-0 mt-1 text-lg font-semibold tracking-tight">
                    Create local backup
                  </h3>
                  <p className="m-0 mt-1 text-sm text-muted-foreground">
                    Back up all applications or just one. This action does not upload artifacts.
                  </p>
                </div>
              </div>
              <div className="mt-auto pt-6">
                <label className="grid gap-1.5 text-sm font-medium">
                  Backup scope
                  <NativeSelect value={scope} onChange={(event) => setScope(event.target.value)}>
                    <NativeSelectOption value="">All applications</NativeSelectOption>
                    {apps.map((name) => (
                      <NativeSelectOption key={name} value={name}>
                        {name}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </label>
                <Button
                  className="mt-3 w-full sm:w-auto"
                  disabled={start.isPending || active || apps.length === 0 || runs.isPending || Boolean(runs.error)}
                  onClick={() => start.mutate(scope ? { app: scope } : {})}
                >
                  {start.isPending ? <Spinner /> : <Archive className="size-4" aria-hidden="true" />}
                  {active ? "Backup in progress" : start.isPending ? "Starting…" : "Create backup"}
                </Button>
                {apps.length === 0 && (
                  <p className="mb-0 mt-2 text-xs text-muted-foreground">
                    No application bindings available to back up.
                  </p>
                )}
                {runs.error && (
                  <p className="mb-0 mt-2 text-xs text-destructive">
                    Run status is unavailable. Refresh before starting another backup.
                  </p>
                )}
                {start.data && (
                  <p className="mb-0 mt-3 text-xs text-muted-foreground" role="status">
                    Run started: <code>{start.data.id}</code>. Follow its progress below.
                  </p>
                )}
                {start.error && (
                  <Alert className="mt-3" variant="destructive">
                    {start.error.message}
                  </Alert>
                )}
              </div>
            </section>
            <BackupRunCard />
          </div>

          <div className="mt-10 flex items-start gap-3 rounded-xl border border-amber-500/30 bg-amber-500/5 p-4 text-sm">
            <Info className="mt-0.5 size-4 shrink-0 text-amber-700 dark:text-amber-400" aria-hidden="true" />
            <p className="m-0 text-muted-foreground">
              <strong className="text-foreground">Local files are not recovery proof.</strong> Check off-host copies and
              test restores separately. SQLite restore is not available here.
            </p>
          </div>

          <section className="mt-10" aria-labelledby="runs-title">
            <div className="flex items-end justify-between gap-3">
              <div>
                <p className="m-0 text-[0.68rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
                  Activity
                </p>
                <h3 id="runs-title" className="m-0 mt-1 text-xl font-semibold tracking-tight">
                  On-demand runs
                </h3>
                <p className="m-0 mt-1 text-sm text-muted-foreground">
                  Only backups started from this page appear here. Completed artifacts may remain after a failed run.
                </p>
              </div>
              {runs.data && <Badge variant="secondary">{runs.data.runs.length} runs</Badge>}
            </div>
            {runs.error && (
              <Alert className="mt-4" variant="destructive">
                Could not load on-demand runs: {runs.error.message}
              </Alert>
            )}
            {runs.isPending && <p className="mt-4 text-sm text-muted-foreground">Loading runs…</p>}
            {runs.data?.runs.length === 0 && (
              <div className="mt-4 rounded-xl border border-dashed border-border bg-card p-8 text-center text-sm text-muted-foreground">
                No on-demand runs yet. Start a backup above to follow its progress here.
              </div>
            )}
            {runs.data && runs.data.runs.length > 0 && (
              <div className="mt-4 overflow-hidden rounded-xl border border-border bg-card shadow-sm">
                <Table className="min-w-[760px] table-fixed">
                  <caption className="sr-only">On-demand backup runs</caption>
                  <TableHeader className="bg-muted/40">
                    <TableRow>
                      <TableHead className="w-[115px] pl-5">Status</TableHead>
                      <TableHead className="w-[210px]">Run</TableHead>
                      <TableHead className="w-[185px]">Targets</TableHead>
                      <TableHead className="pr-5">Result</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {runs.data.runs.map((run) => (
                      <TableRow key={run.id}>
                        <TableCell className="pl-5 align-top pt-4">
                          <Badge className={statusClass(run.status)}>{statusLabel(run.status)}</Badge>
                        </TableCell>
                        <TableCell className="align-top pt-4">
                          <code className="block text-xs">{run.id}</code>
                          <span className="mt-1 block text-xs text-muted-foreground">
                            Started {formatDate(run.startedAt)}
                          </span>
                        </TableCell>
                        <TableCell className="align-top pt-4" aria-live={run.status === "running" ? "polite" : "off"}>
                          <span className="text-sm font-medium">
                            {run.progress
                              ? `${run.progress.completed} / ${run.progress.total} completed`
                              : run.status === "running"
                                ? "Preparing…"
                                : "Progress unavailable"}
                          </span>
                          {run.progress && run.progress.total > 0 && (
                            <div
                              role="progressbar"
                              aria-label={`Backup ${run.id}`}
                              aria-valuenow={run.progress.completed}
                              aria-valuemin={0}
                              aria-valuemax={run.progress.total}
                              className="mt-2 h-1.5 overflow-hidden rounded-full bg-muted"
                            >
                              <div
                                className="h-full rounded-full bg-primary transition-[width]"
                                style={{
                                  width: `${Math.min(100, (run.progress.completed / run.progress.total) * 100)}%`,
                                }}
                              />
                            </div>
                          )}
                        </TableCell>
                        <TableCell className="whitespace-normal align-top pr-5 pt-4">
                          <span className="text-xs text-muted-foreground">
                            {run.finishedAt ? `Finished ${formatDate(run.finishedAt)}` : "Not finished"}
                          </span>
                          {run.steps.length > 0 && (
                            <ul className="mb-0 mt-2 list-none space-y-1 p-0 text-xs">
                              {run.steps.map((step) => (
                                <li key={step.name}>
                                  <span className="font-medium">
                                    {step.name === "backup" ? "Local backup" : "Remote upload"}
                                  </span>
                                  <span className="text-muted-foreground"> · {statusLabel(step.status)}</span>
                                  {step.error && <p className="m-0 break-words text-destructive">{step.error}</p>}
                                </li>
                              ))}
                            </ul>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </section>

          <section className="mt-10" aria-labelledby="artifacts-title">
            <div className="flex flex-wrap items-end justify-between gap-3">
              <div>
                <p className="m-0 text-[0.68rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
                  Inventory
                </p>
                <h3 id="artifacts-title" className="m-0 mt-1 text-xl font-semibold tracking-tight">
                  Local artifacts
                </h3>
                <p className="m-0 mt-1 text-sm text-muted-foreground">
                  Backup files on this host. Relational dumps can be restored into new databases.
                </p>
              </div>
              <Badge variant="secondary">{artifacts.length} files</Badge>
            </div>
            {artifacts.length > 0 && (
              <div className="mt-5 flex flex-wrap items-center gap-3">
                <div className="relative min-w-0 flex-1 sm:max-w-sm">
                  <Search
                    className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
                    aria-hidden="true"
                  />
                  <Input
                    className="pl-9"
                    aria-label="Search backup artifacts"
                    placeholder="Search by path…"
                    value={search}
                    onChange={(event) => setSearch(event.target.value)}
                  />
                </div>
                <div
                  className="flex flex-wrap gap-1 rounded-lg border border-border bg-muted/40 p-1"
                  role="group"
                  aria-label="Filter artifacts"
                >
                  {(["all", "relational", "manual"] as const).map((option) => (
                    <Button
                      key={option}
                      size="sm"
                      variant={filter === option ? "secondary" : "ghost"}
                      aria-pressed={filter === option}
                      onClick={() => setFilter(option)}
                    >
                      {option === "all" ? "All" : option === "relational" ? "Restore options" : "Manual recovery"}
                    </Button>
                  ))}
                </div>
              </div>
            )}
            {artifacts.length === 0 ? (
              <div className="mt-4 rounded-xl border border-dashed border-border bg-card p-8 text-center text-sm text-muted-foreground">
                No finalized backup files found. Create a backup to populate this inventory.
              </div>
            ) : visible.length === 0 ? (
              <div className="mt-4 rounded-xl border border-dashed border-border bg-card p-8 text-center text-sm text-muted-foreground">
                No artifacts match this search or filter.
              </div>
            ) : (
              <div className="mt-4 overflow-hidden rounded-xl border border-border bg-card shadow-sm">
                {visible.map((item) => {
                  const relational = restoreSource(item, data).length > 0;
                  const segments = item.name.split("/");
                  return (
                    <div
                      key={item.name}
                      className="flex flex-wrap items-center justify-between gap-4 border-b border-border p-4 last:border-b-0 hover:bg-muted/20 sm:px-5"
                    >
                      <div className="flex min-w-0 flex-1 items-start gap-3">
                        <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground">
                          <Database className="size-4" aria-hidden="true" />
                        </span>
                        <div className="min-w-0">
                          <p className="m-0 break-all text-sm font-medium">{segments.at(-1)}</p>
                          <p className="m-0 mt-0.5 break-all font-mono text-xs text-muted-foreground">{item.name}</p>
                          <p className="m-0 mt-1 text-xs text-muted-foreground">
                            {formatBytes(item.bytes)} · {item.modifiedAt ? formatDate(item.modifiedAt) : "Date unknown"}
                          </p>
                        </div>
                      </div>
                      {relational ? (
                        <Button variant="outline" size="sm" onClick={() => openRestore(item.name)}>
                          <ArchiveRestore className="size-4" aria-hidden="true" /> Restore to new database
                        </Button>
                      ) : (
                        <Badge variant="secondary">Manual recovery</Badge>
                      )}
                    </div>
                  );
                })}
              </div>
            )}
          </section>
        </>
      )}

      <Dialog open={Boolean(artifact)} onOpenChange={(open) => !open && closeRestore()}>
        <DialogContent
          className="max-h-[calc(100vh-2rem)] overflow-y-auto sm:max-w-[520px]"
          showCloseButton={!restore.isPending}
        >
          <DialogHeader>
            <DialogTitle>Restore to new database</DialogTitle>
            <DialogDescription>
              {restore.isSuccess
                ? "The new database is ready to inspect. Existing databases were not replaced."
                : "Creates a new database for the source application. Existing databases are not replaced."}
            </DialogDescription>
          </DialogHeader>
          <div className="rounded-lg border border-border bg-muted/40 p-3">
            <p className="m-0 text-xs font-medium text-muted-foreground">SOURCE ARTIFACT</p>
            <code className="mt-1 block break-all text-xs">{artifact?.name}</code>
          </div>
          {restore.isSuccess ? (
            <>
              <Alert className="border-emerald-500/40 bg-emerald-500/10" role="status">
                <strong>Restore complete.</strong> {restore.data?.message}
              </Alert>
              <DialogFooter>
                <Button type="button" onClick={closeRestore}>
                  Close
                </Button>
              </DialogFooter>
            </>
          ) : (
            <>
              <p className="m-0 text-xs text-muted-foreground">
                If the import fails, the new database may be incomplete.
              </p>
              <form className="grid gap-4" onSubmit={submitRestore}>
                <div className="grid gap-4 sm:grid-cols-2">
                  {compatibleApps.length === 1 ? (
                    <div className="grid content-start gap-1.5 text-sm">
                      <span className="font-medium">Source application</span>
                      <span className="flex h-9 items-center rounded-md border border-border bg-muted/40 px-3">
                        {app}
                      </span>
                    </div>
                  ) : (
                    <label className="grid gap-1.5 text-sm font-medium">
                      Source application
                      <NativeSelect
                        value={app}
                        disabled={restore.isPending || restore.isSuccess}
                        onChange={(event) => {
                          setApp(event.target.value);
                          setTarget(
                            event.target.value ? suggestedTarget(event.target.value, source[0] ?? "", data) : "",
                          );
                          if (restore.isError) restore.reset();
                        }}
                      >
                        <NativeSelectOption value="">Select application</NativeSelectOption>
                        {compatibleApps.map((name) => (
                          <NativeSelectOption key={name} value={name}>
                            {name}
                          </NativeSelectOption>
                        ))}
                      </NativeSelect>
                    </label>
                  )}
                  <label className="grid gap-1.5 text-sm font-medium">
                    New target database
                    <Input
                      value={target}
                      disabled={!app || restore.isPending || restore.isSuccess}
                      onChange={(event) => {
                        setTarget(event.target.value);
                        if (restore.isError) restore.reset();
                      }}
                      placeholder={app ? `${app}_restore` : "Select application first"}
                      aria-invalid={Boolean(target && !validTarget && !restore.isPending && !restore.error)}
                    />
                  </label>
                </div>
                {target && !validTarget && !restore.isPending && !restore.error && (
                  <p className="m-0 text-xs text-destructive">
                    Use a new app-namespaced name (for example {app || "app"}_restore).
                  </p>
                )}
                {restore.error && <Alert variant="destructive">{restore.error.message}</Alert>}
                <DialogFooter>
                  <Button type="button" variant="outline" disabled={restore.isPending} onClick={closeRestore}>
                    Cancel
                  </Button>
                  <Button type="submit" disabled={!validTarget || !engine || restore.isPending}>
                    {restore.isPending && <Spinner />} Restore to new database
                  </Button>
                </DialogFooter>
              </form>
            </>
          )}
        </DialogContent>
      </Dialog>
    </section>
  );
}

function Metric({ icon, label, value }: { icon: ReactNode; label: string; value: string }) {
  return (
    <div className="flex items-center gap-3 rounded-xl border border-border bg-card px-4 py-3 shadow-sm">
      <span
        className="grid size-9 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground"
        aria-hidden="true"
      >
        {icon}
      </span>
      <div>
        <strong className="block text-xl leading-none tracking-tight">{value}</strong>
        <span className="mt-1 block text-xs text-muted-foreground">{label}</span>
      </div>
    </div>
  );
}

function restoreSource(item: Artifact, data?: DataOverview) {
  const parts = item.name.split("/");
  if (parts.length !== 3 || !/\.sql(?:\.gz|\.zst|\.zstd)$/i.test(parts[2] ?? "")) return [];
  return data?.bindings.some(
    (binding) =>
      binding.engine !== "sqlite" &&
      data.services.some((service) => service.service === binding.service && service.engine === binding.engine) &&
      binding.service === parts[0] &&
      binding.resources.includes(parts[1] ?? ""),
  )
    ? parts
    : [];
}

function suggestedTarget(app: string, service: string, data?: DataOverview) {
  const used = new Set(
    data?.bindings.filter((binding) => binding.service === service).flatMap((binding) => binding.resources),
  );
  const base = `${app}_restore`;
  let name = base;
  let suffix = 2;
  while (used.has(name)) name = `${base}_${suffix++}`;
  return name;
}

function statusClass(status: string) {
  return status === "succeeded"
    ? "bg-emerald-600 text-white"
    : status === "running"
      ? "bg-amber-500 text-amber-950"
      : "bg-destructive text-white";
}
function statusLabel(status: string) {
  return status.charAt(0).toUpperCase() + status.slice(1);
}
function formatDate(value: string) {
  return new Date(value).toLocaleString();
}
function formatBytes(value: number) {
  return value < 1024
    ? `${value} B`
    : value < 1048576
      ? `${Math.round(value / 1024)} KB`
      : value < 1073741824
        ? `${(value / 1048576).toFixed(1)} MB`
        : `${(value / 1073741824).toFixed(1)} GB`;
}
