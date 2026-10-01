import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Archive,
  CalendarClock,
  ChevronRight,
  Cloud,
  Download,
  Pencil,
  Plus,
  RotateCcw,
  SquareTerminal,
  Trash2,
} from "lucide-react";
import { Link } from "wouter";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import {
  DomainError,
  DomainLoading,
  EmptyState,
  Field,
  PageHeader,
  StateBadge,
} from "../../components/DomainState.tsx";
import { EngineLogo } from "../../components/EngineLogo.tsx";
import { keepEscapeInTerminal, TerminalView } from "../../components/TerminalDialog.tsx";
import { formatBytes, formatCron, formatDuration, formatRelative } from "../../lib/format.ts";
import { useApplication, useApplicationList, useOperationMutation } from "../applications/useApplications.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";

type Tab = "artifacts" | "apps" | "runs" | "schedules";
const tabLabels: Record<Tab, string> = {
  artifacts: "Files",
  apps: "App backups",
  runs: "Runs",
  schedules: "Schedules",
};

export function BackupsPage() {
  const [tab, setTab] = useState<Tab>("artifacts");
  const [runOpen, setRunOpen] = useState(false);
  const [restoreTarget, setRestoreTarget] = useState<T.BackupArtifact | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<T.BackupArtifact | null>(null);
  return (
    <>
      <PageHeader title="Backups" actions={<Button onClick={() => setRunOpen(true)}>Back up now</Button>} />
      <div className="seg mb-5" role="tablist" aria-label="Backup sections">
        {(["artifacts", "apps", "runs", "schedules"] as const).map((value) => (
          <button key={value} type="button" role="tab" aria-selected={tab === value} onClick={() => setTab(value)}>
            {tabLabels[value]}
          </button>
        ))}
      </div>
      {tab === "artifacts" && <ArtifactsTab onRestore={setRestoreTarget} onDelete={setDeleteTarget} />}
      {tab === "apps" && <AppBackupsTab />}
      {tab === "runs" && <RunsTab />}
      {tab === "schedules" && <SchedulesTab />}
      {runOpen && <BackupNowDialog onClose={() => setRunOpen(false)} />}
      {restoreTarget && <RestoreDialog artifact={restoreTarget} onClose={() => setRestoreTarget(null)} />}
      {deleteTarget && <DeleteDialog artifact={deleteTarget} onClose={() => setDeleteTarget(null)} />}
    </>
  );
}

const engineLabels: Record<string, string> = { mysql: "MySQL", postgres: "PostgreSQL", sqlite: "SQLite" };

function ArtifactsTab({
  onRestore,
  onDelete,
}: {
  onRestore: (artifact: T.BackupArtifact) => void;
  onDelete: (artifact: T.BackupArtifact) => void;
}) {
  const [engine, setEngine] = useState("all");
  const query = useQuery({ queryKey: keys.backups.artifacts, queryFn: ({ signal }) => api.backups.artifacts(signal) });
  if (query.isPending) return <DomainLoading label="backups" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  const all = [...query.data.artifacts].sort((a, b) => b.createdAt.localeCompare(a.createdAt));
  const engines = [...new Set(all.map((artifact) => artifact.engine))].sort();
  const artifacts = all.filter((artifact) => engine === "all" || artifact.engine === engine);
  return (
    <div className="box">
      <div className="cell">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
          <span className="note">
            {artifacts.length} {artifacts.length === 1 ? "file" : "files"}
          </span>
          {engines.length > 1 && (
            <div className="seg" role="group" aria-label="Filter by database type">
              {["all", ...engines].map((value) => (
                <button key={value} type="button" aria-pressed={engine === value} onClick={() => setEngine(value)}>
                  {value === "all" ? "All" : <EngineLogo engine={value} className="size-3.5" />}
                  {value !== "all" && (engineLabels[value] ?? value)}
                </button>
              ))}
            </div>
          )}
        </div>
        {artifacts.length === 0 ? (
          <EmptyState icon={<Archive />} title="No backups yet" />
        ) : (
          <div className="rows rows--lined">
            {artifacts.map((artifact) => (
              <div key={artifact.path} className="row">
                <span className="grid size-9 shrink-0 place-items-center rounded-lg border bg-muted/40">
                  <EngineLogo engine={artifact.engine} />
                </span>
                <span className="row__main">
                  <strong className="flex flex-wrap items-center gap-2">
                    {artifact.database}
                    <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
                      {engineLabels[artifact.engine] ?? artifact.engine}
                    </span>
                  </strong>
                  <small className="truncate" title={artifact.path}>
                    {artifact.appSlug} · {artifact.path.split("/").at(-1)}
                  </small>
                </span>
                <span className="row__meta grid justify-items-end max-sm:hidden">
                  <span className="tabular-nums">{formatBytes(artifact.sizeBytes)}</span>
                  <span title={artifact.createdAt}>{formatRelative(artifact.createdAt)}</span>
                </span>
                <Button size="sm" variant="outline" asChild>
                  <a href={api.backups.downloadUrl(artifact.path)} download aria-label={`Download ${artifact.path}`}>
                    <Download />
                  </a>
                </Button>
                <Button size="sm" variant="outline" onClick={() => onRestore(artifact)}>
                  <RotateCcw /> Restore
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  aria-label={`Delete ${artifact.path}`}
                  onClick={() => onDelete(artifact)}
                >
                  <Trash2 />
                </Button>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

/** Polled overview of app (restic) backups, shared by the App backups, Runs and Schedules tabs. */
function useAppBackups() {
  return useQuery({
    queryKey: keys.backups.apps,
    queryFn: ({ signal }) => api.backups.apps(signal),
    refetchInterval: 30_000,
  });
}

function AppBackupsTab() {
  const query = useAppBackups();
  if (query.isPending) return <DomainLoading label="app backups" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  const apps = query.data.apps;
  if (apps.length === 0) {
    return (
      <div className="box">
        <div className="cell">
          <EmptyState
            icon={<Archive />}
            title="No app backups"
            body="Open an app's Backup tab to back up its files, databases and scheduler into an encrypted restic repository."
          />
        </div>
      </div>
    );
  }
  return (
    <>
      <p className="note mb-3">
        Whole-app snapshots (home, databases, scheduler) in each app's restic repository. Cron uses server time (
        {query.data.timeZone}).
      </p>
      <div className="box">
        <div className="tiles">
          {apps.map((summary) => (
            <AppBackupTile key={summary.appId} summary={summary} />
          ))}
        </div>
      </div>
    </>
  );
}

function AppBackupTile({ summary, compact = false }: { summary: T.AppBackupSummary; compact?: boolean }) {
  const last = summary.lastBackup;
  return (
    <article
      className={`cell tile ${last && !last.ok ? "cell--alert" : ""}`}
      aria-label={`App backup of ${summary.slug}`}
    >
      <div className="tile__top">
        <span className={`mono mono--lg ${summary.scheduleEnabled ? "" : "opacity-50"}`} aria-hidden="true">
          <Archive className="size-5" />
        </span>
        <div className="tile__name">
          <strong>{summary.slug}</strong>
          <small className="font-mono" title={summary.cron}>
            {summary.cron ? formatCron(summary.cron) : "no schedule"}
            {summary.cron && !summary.scheduleEnabled && " (off)"}
          </small>
        </div>
        {!summary.initialized && <StateBadge state="pending" label="not set up" />}
      </div>
      <dl className="facts">
        <div>
          <dt>Next run</dt>
          <dd title={summary.nextRun ? new Date(summary.nextRun).toLocaleString() : undefined}>
            {summary.nextRun ? formatRelative(summary.nextRun) : "—"}
          </dd>
        </div>
        <div>
          <dt>Last backup</dt>
          <dd title={last?.at} className="flex flex-wrap items-center gap-1.5">
            {last ? formatRelative(last.at) : "—"}
            {last && <StateBadge state={last.ok ? "succeeded" : "failed"} />}
          </dd>
        </div>
        {!compact && (
          <>
            <div>
              <dt>Snapshots</dt>
              <dd>{summary.snapshotCount}</dd>
            </div>
            <div>
              <dt>Added</dt>
              <dd>{last?.ok ? formatBytes(last.bytesAdded) : "—"}</dd>
            </div>
            <div className="facts__wide">
              <dt>What</dt>
              <dd>{summary.paths.includes(".") ? "Whole home" : summary.paths.join(", ")} + databases + scheduler</dd>
            </div>
            <div>
              <dt>Verified</dt>
              <dd>
                {summary.lastCheck
                  ? `${summary.lastCheck.ok ? "ok" : "failed"} ${formatRelative(summary.lastCheck.at)}`
                  : "never"}
              </dd>
            </div>
          </>
        )}
        <div className="facts__wide">
          <dt>Repository</dt>
          <dd className="inline-flex min-w-0 items-center gap-1">
            <Cloud className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
            <code className="truncate">{summary.repository}</code>
          </dd>
        </div>
      </dl>
      {last && !last.ok && <p className="note note--bad line-clamp-2">{last.error}</p>}
      <div className="mt-auto flex justify-end">
        <Button size="sm" variant="outline" asChild>
          <Link href={`/apps/${summary.slug}/backup`}>
            <Pencil /> Open in app
          </Link>
        </Button>
      </div>
    </article>
  );
}

type RunItem = { at: string; db?: T.BackupRun; app?: T.AppBackupRun };

function RunsTab() {
  const query = useQuery({ queryKey: keys.backups.runs, queryFn: ({ signal }) => api.backups.runs(signal) });
  const appRuns = useAppBackups();
  if (query.isPending || appRuns.isPending) return <DomainLoading label="runs" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  if (appRuns.error) return <DomainError message={messageOf(appRuns.error)} onRetry={() => void appRuns.refetch()} />;
  const items: RunItem[] = [
    ...query.data.runs.map((run) => ({ at: run.startedAt, db: run })),
    ...appRuns.data.runs.map((run) => ({ at: run.run.at, app: run })),
  ].sort((a, b) => b.at.localeCompare(a.at));
  return (
    <div className="box">
      <div className="cell">
        {items.length === 0 ? (
          <EmptyState icon={<Archive />} title="No runs yet" />
        ) : (
          <div className="rows rows--lined">
            {items.map(({ db: run, app }) =>
              app ? (
                <AppRunRow key={`app-${app.appId}-${app.run.opId || app.run.at}`} entry={app} />
              ) : (
                run && (
                  <details key={run.id} className="group">
                    <summary className="row cursor-pointer list-none">
                      <ChevronRight className="size-4 transition-transform group-open:rotate-90" aria-hidden="true" />
                      <span className="row__main">
                        <strong className="capitalize">{run.trigger}</strong>
                        <small title={run.startedAt}>
                          {formatRelative(run.startedAt)} · {formatDuration(run.startedAt, run.finishedAt)} ·{" "}
                          {run.artifacts.length} files
                          {run.uploadState && ` · upload ${run.uploadState}`}
                        </small>
                      </span>
                      <StateBadge state={run.state} />
                    </summary>
                    <div className="grid gap-2 pb-3 pl-9">
                      {run.error && <p className="note note--bad">{run.error}</p>}
                      {run.artifacts.map((artifact) => (
                        <code key={artifact} className="note truncate">
                          {artifact}
                        </code>
                      ))}
                    </div>
                  </details>
                )
              ),
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function AppRunRow({ entry }: { entry: T.AppBackupRun }) {
  const run = entry.run;
  return (
    <details className="group">
      <summary className="row cursor-pointer list-none">
        <ChevronRight className="size-4 transition-transform group-open:rotate-90" aria-hidden="true" />
        <span className="row__main">
          <strong>
            App backup · {entry.slug}{" "}
            <span className="font-normal capitalize text-muted-foreground">{run.trigger}</span>
          </strong>
          <small title={run.at}>
            {formatRelative(run.at)} · {Math.round(run.seconds)}s
            {run.ok && ` · ${run.filesTotal} files (${run.filesNew} new), ${formatBytes(run.bytesAdded)} added`}
          </small>
        </span>
        <StateBadge state={run.ok ? "succeeded" : "failed"} />
      </summary>
      <div className="grid gap-2 pb-3 pl-9">
        {run.error && <p className="note note--bad">{run.error}</p>}
        {run.snapshotId && <code className="note">snapshot {run.snapshotId.slice(0, 8)}</code>}
        <Link className="note underline" href={`/apps/${entry.slug}/backup`}>
          Open {entry.slug} → Backup
        </Link>
      </div>
    </details>
  );
}

function BackupNowDialog({ onClose }: { onClose: () => void }) {
  const [compression, setCompression] = useState("zstd");
  const [rcloneRemote, setRcloneRemote] = useState("");
  const rclone = useQuery({ queryKey: keys.backups.rclone, queryFn: ({ signal }) => api.backups.rclone(signal) });
  const run = useOperationMutation(() => api.backups.run({ scope: "all", compression, rcloneRemote }));
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Back up now</DialogTitle>
          <DialogDescription>Every app database. Manual backups are never removed by retention.</DialogDescription>
        </DialogHeader>
        <Field label="Compression">
          <CompressionSelect value={compression} onChange={setCompression} />
        </Field>
        <Field label="Upload to" hint="rclone name:path — leave empty to keep the files on this host only">
          <Input
            className="font-mono"
            value={rcloneRemote}
            placeholder="remote:bucket/path"
            onChange={(event) => setRcloneRemote(event.target.value)}
          />
        </Field>
        {rcloneRemote && rclone.data?.encrypted && (
          <span className="note note--bad">The rclone config is encrypted, so this upload will fail.</span>
        )}
        {run.error && <Alert variant="destructive">{messageOf(run.error)}</Alert>}
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={run.isPending} onClick={() => run.mutate(undefined, { onSuccess: onClose })}>
            Start
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function CompressionSelect({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return (
    <NativeSelect className="w-full" value={value} onChange={(event) => onChange(event.target.value)}>
      <option value="zstd">zstd</option>
      <option value="gzip">gzip</option>
    </NativeSelect>
  );
}

const newSchedule: T.BackupSchedule = {
  id: "",
  name: "",
  enabled: true,
  cron: "30 2 * * *",
  scope: "all",
  databases: [],
  compression: "zstd",
  retain: 7,
  rcloneRemote: "",
};

function SchedulesTab() {
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState<T.BackupSchedule | null>(null);
  const [deleting, setDeleting] = useState<T.BackupSchedule | null>(null);
  // Polled so "Next run" and "Last run" move on after a slot fires.
  const query = useQuery({
    queryKey: keys.backups.schedules,
    queryFn: ({ signal }) => api.backups.schedules(signal),
    refetchInterval: 30_000,
  });
  const apps = useApplicationList();
  const appBackups = useAppBackups();
  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => api.backups.enableSchedule(id, enabled),
    onSettled: () => queryClient.invalidateQueries({ queryKey: keys.backups.schedules }),
  });
  if (query.isPending) return <DomainLoading label="schedules" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  const appSlug = (id?: string) => apps.data?.apps.find((app) => app.id === id)?.slug ?? id ?? "";
  const scopeLabel = (schedule: T.BackupSchedule) =>
    schedule.scope === "all"
      ? "All databases"
      : schedule.scope === "app"
        ? `App ${appSlug(schedule.appId)}`
        : `${appSlug(schedule.appId)}: ${schedule.databases.join(", ")}`;
  const schedules = query.data.schedules;
  return (
    <>
      <p className="note mb-3">Cron uses server time ({query.data.timeZone}). Missed runs are not replayed.</p>
      {toggle.error && <Alert variant="destructive">{messageOf(toggle.error)}</Alert>}
      <div className="box">
        <div className="tiles">
          {schedules.map((schedule) => (
            <article
              key={schedule.id}
              className={`cell tile ${schedule.enabled && schedule.lastState === "submit-failed" ? "cell--alert" : ""}`}
              aria-label={schedule.name}
            >
              <div className="tile__top">
                <span className={`mono mono--lg ${schedule.enabled ? "" : "opacity-50"}`} aria-hidden="true">
                  <CalendarClock className="size-5" />
                </span>
                <div className="tile__name">
                  <strong title={schedule.name}>{schedule.name}</strong>
                  <small className="font-mono" title={schedule.cron}>
                    {formatCron(schedule.cron)}
                  </small>
                </div>
                <label className="check shrink-0 text-sm">
                  <Checkbox
                    aria-label={`${schedule.enabled ? "Disable" : "Enable"} ${schedule.name}`}
                    checked={schedule.enabled}
                    disabled={toggle.isPending}
                    onCheckedChange={(checked) => toggle.mutate({ id: schedule.id, enabled: checked === true })}
                  />
                  {schedule.enabled ? "On" : "Off"}
                </label>
              </div>
              <dl className="facts">
                <div>
                  <dt>Next run</dt>
                  <dd title={schedule.nextRun ? new Date(schedule.nextRun).toLocaleString() : undefined}>
                    {schedule.nextRun ? formatRelative(schedule.nextRun) : "—"}
                  </dd>
                </div>
                <div>
                  <dt>Last run</dt>
                  <dd title={schedule.lastRun} className="flex flex-wrap items-center gap-1.5">
                    {schedule.lastRun ? formatRelative(schedule.lastRun) : "—"}
                    {schedule.lastState && <StateBadge state={schedule.lastState} />}
                  </dd>
                </div>
                <div className="facts__wide">
                  <dt>Databases</dt>
                  <dd>{scopeLabel(schedule)}</dd>
                </div>
                <div>
                  <dt>Keep</dt>
                  <dd>{schedule.retain} per database</dd>
                </div>
                <div>
                  <dt>Compression</dt>
                  <dd>{schedule.compression}</dd>
                </div>
                <div className="facts__wide">
                  <dt>Upload</dt>
                  <dd className="inline-flex min-w-0 items-center gap-1">
                    <Cloud className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                    {schedule.rcloneRemote ? (
                      <code className="truncate">{schedule.rcloneRemote}</code>
                    ) : (
                      <span className="text-muted-foreground">This host only</span>
                    )}
                  </dd>
                </div>
              </dl>
              <div className="mt-auto flex justify-end gap-2">
                <Button size="sm" variant="outline" onClick={() => setEditing(schedule)}>
                  <Pencil /> Edit
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  aria-label={`Delete ${schedule.name}`}
                  onClick={() => setDeleting(schedule)}
                >
                  <Trash2 />
                </Button>
              </div>
            </article>
          ))}
          {(appBackups.data?.apps ?? [])
            .filter((summary) => summary.cron)
            .map((summary) => (
              <AppBackupTile key={`app-${summary.appId}`} summary={summary} compact />
            ))}
          <button type="button" className="cell tile tile--add" onClick={() => setEditing(newSchedule)}>
            <Plus aria-hidden="true" />
            New schedule
          </button>
        </div>
      </div>
      {editing && <ScheduleDialog initial={editing} onClose={() => setEditing(null)} />}
      {deleting && <DeleteScheduleDialog schedule={deleting} onClose={() => setDeleting(null)} />}
    </>
  );
}

function ScheduleDialog({ initial, onClose }: { initial: T.BackupSchedule; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [schedule, setSchedule] = useState(initial);
  const apps = useApplicationList();
  const detail = useApplication(schedule.scope === "all" || !schedule.appId ? null : schedule.appId);
  const save = useMutation({
    mutationFn: api.backups.saveSchedule,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: keys.backups.schedules });
      onClose();
    },
  });
  const databases = (detail.data?.bindings ?? [])
    .flatMap((binding) => (binding.engine === "sqlite" ? [sqliteFileId(binding.sqlitePath)] : binding.databases))
    .filter((value): value is string => !!value);
  const toggleDatabase = (db: string, on: boolean) =>
    setSchedule({
      ...schedule,
      databases: on ? [...schedule.databases, db] : schedule.databases.filter((value) => value !== db),
    });
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{initial.id ? `Edit ${initial.name}` : "New backup schedule"}</DialogTitle>
          <DialogDescription>
            Retention keeps the newest files of each database made by this schedule; other schedules and manual backups
            are not affected.
          </DialogDescription>
        </DialogHeader>
        <div className="grid items-start gap-4 sm:grid-cols-2">
          <Field label="Name">
            <Input value={schedule.name} onChange={(event) => setSchedule({ ...schedule, name: event.target.value })} />
          </Field>
          <Field label="Cron" hint={`${formatCron(schedule.cron)}${initial.timeZone ? ` · ${initial.timeZone}` : ""}`}>
            <Input
              className="font-mono"
              value={schedule.cron}
              onChange={(event) => setSchedule({ ...schedule, cron: event.target.value })}
            />
          </Field>
          <Field label="Databases">
            <NativeSelect
              className="w-full"
              value={schedule.scope}
              onChange={(event) => setSchedule({ ...schedule, scope: event.target.value, databases: [] })}
            >
              <option value="all">All apps</option>
              <option value="app">One app</option>
              <option value="database">Specific databases</option>
            </NativeSelect>
          </Field>
          {schedule.scope !== "all" && (
            <Field label="App">
              <NativeSelect
                className="w-full"
                value={schedule.appId ?? ""}
                onChange={(event) => setSchedule({ ...schedule, appId: event.target.value, databases: [] })}
              >
                <option value="" disabled>
                  Select an app
                </option>
                {(apps.data?.apps ?? []).map((app) => (
                  <option key={app.id} value={app.id}>
                    {app.slug}
                  </option>
                ))}
              </NativeSelect>
            </Field>
          )}
          {schedule.scope === "database" && schedule.appId && (
            <div className="grid gap-2 sm:col-span-2">
              {detail.isPending && <DomainLoading label="databases" />}
              {detail.data && databases.length === 0 && <span className="note">This app has no databases.</span>}
              {databases.map((db) => (
                <label key={db} className="check">
                  <Checkbox
                    checked={schedule.databases.includes(db)}
                    onCheckedChange={(checked) => toggleDatabase(db, checked === true)}
                  />
                  <code>{db}</code>
                </label>
              ))}
            </div>
          )}
          <Field label="Keep per database">
            <Input
              type="number"
              min="1"
              max="365"
              value={schedule.retain}
              onChange={(event) => setSchedule({ ...schedule, retain: Number(event.target.value) })}
            />
          </Field>
          <Field label="Compression">
            <CompressionSelect
              value={schedule.compression}
              onChange={(compression) => setSchedule({ ...schedule, compression })}
            />
          </Field>
        </div>
        <UploadSection
          remote={schedule.rcloneRemote}
          onRemoteChange={(rcloneRemote) => setSchedule({ ...schedule, rcloneRemote })}
        />
        <label className="check">
          <Checkbox
            checked={schedule.enabled}
            onCheckedChange={(checked) => setSchedule({ ...schedule, enabled: checked === true })}
          />
          Enabled
        </label>
        {save.error && <Alert variant="destructive">{messageOf(save.error)}</Alert>}
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={save.isPending} onClick={() => save.mutate(schedule)}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function DeleteScheduleDialog({ schedule, onClose }: { schedule: T.BackupSchedule; onClose: () => void }) {
  const queryClient = useQueryClient();
  const remove = useMutation({
    mutationFn: () => api.backups.removeSchedule(schedule.id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: keys.backups.schedules });
      onClose();
    },
  });
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete schedule {schedule.name}?</DialogTitle>
          <DialogDescription>
            No more backups will run for it. Files it already made are kept until you delete them.
          </DialogDescription>
        </DialogHeader>
        {remove.error && <Alert variant="destructive">{messageOf(remove.error)}</Alert>}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            No
          </Button>
          <Button variant="destructive" disabled={remove.isPending} onClick={() => remove.mutate()}>
            Yes, delete
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Upload destination plus the rclone remotes it can use, which are
 * configured in a throwaway rclone shell. */
function UploadSection({ remote, onRemoteChange }: { remote: string; onRemoteChange: (remote: string) => void }) {
  const queryClient = useQueryClient();
  const [shellOpen, setShellOpen] = useState(false);
  const query = useQuery({ queryKey: keys.backups.rclone, queryFn: ({ signal }) => api.backups.rclone(signal) });
  const test = useOperationMutation((value: string) => api.backups.rcloneTest({ remote: value }));
  const closeShell = () => {
    setShellOpen(false);
    void queryClient.invalidateQueries({ queryKey: keys.backups.rclone });
  };
  const remotes = query.data?.remotes ?? [];
  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <Cloud className="size-4 text-muted-foreground" aria-hidden="true" />
        <strong className="mr-auto">Upload</strong>
        <Button size="sm" variant="outline" onClick={() => setShellOpen(true)}>
          <SquareTerminal /> Open rclone shell
        </Button>
      </div>
      <div className="flex gap-2">
        <Input
          className="font-mono"
          aria-label="rclone remote"
          value={remote}
          placeholder="remote:bucket/path — empty keeps backups on this host"
          onChange={(event) => onRemoteChange(event.target.value)}
        />
        <Button variant="outline" disabled={!remote || test.isPending} onClick={() => test.mutate(remote)}>
          Test
        </Button>
      </div>
      {test.error && <span className="note note--bad">{messageOf(test.error)}</span>}
      {query.error && <span className="note note--bad">{messageOf(query.error)}</span>}
      {query.data?.error && <span className="note note--bad">{query.data.error}</span>}
      {query.data?.encrypted ? (
        <Alert variant="destructive">
          The rclone config is encrypted, so uploads can’t unlock it. In the shell: <code>rclone config</code> →{" "}
          <code>s</code> → <code>u</code> (unencrypt).
        </Alert>
      ) : (
        query.data && (
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="note">Remotes:</span>
            {remotes.length === 0 ? (
              <span className="note">
                none yet — open the shell and run <code>rclone config</code>.
              </span>
            ) : (
              remotes.map((item) => (
                <button
                  key={item.name}
                  type="button"
                  title={`Use ${item.name}:`}
                  className="rounded-full border bg-muted/60 px-2 py-0.5 font-mono text-xs hover:bg-muted"
                  onClick={() => onRemoteChange(`${item.name}:`)}
                >
                  {item.name}: <span className="text-muted-foreground">{item.type || "?"}</span>
                </button>
              ))
            )}
          </div>
        )
      )}
      <details className="note">
        <summary className="cursor-pointer select-none">How to add a remote</summary>
        <ul className="mt-2 grid list-disc gap-1 pl-5">
          <li>
            In the shell, run <code>rclone config</code> and check it with <code>rclone lsd name:</code>. Then pick it
            above, add a path (<code>name:bucket/bento</code>), and press Test.
          </li>
          <li>
            Google Drive, OneDrive, Dropbox: answer <code>n</code> to “Use web browser”, run{" "}
            <code>rclone authorize</code> on a computer with a browser, and paste the token.
          </li>
          <li>
            A <code>crypt</code> remote encrypts backups before they leave this host. Don’t set a config password.
          </li>
        </ul>
      </details>
      {shellOpen && (
        <Dialog open onOpenChange={(open) => !open && closeShell()}>
          <DialogContent className="sm:max-w-5xl" onEscapeKeyDown={keepEscapeInTerminal}>
            <DialogHeader>
              <DialogTitle>rclone shell</DialogTitle>
              <DialogDescription>
                A throwaway rclone container that can see only this stack’s rclone config. Changes are saved as rclone
                writes them. The shell is kept 15 minutes after you disconnect. Esc goes to the shell; close with ×.
              </DialogDescription>
            </DialogHeader>
            <TerminalView path={api.backups.rcloneTerminalPath} />
          </DialogContent>
        </Dialog>
      )}
    </div>
  );
}

function RestoreDialog({ artifact, onClose }: { artifact: T.BackupArtifact; onClose: () => void }) {
  const apps = useApplicationList();
  const summary = apps.data?.apps.find((app) => app.slug === artifact.appSlug);
  const detail = useApplication(summary?.id ?? null);
  const [database, setDatabase] = useState(artifact.database);
  const [confirm, setConfirm] = useState("");
  const targets = (detail.data?.bindings ?? [])
    .filter((binding) => binding.engine === artifact.engine)
    .flatMap((binding) => (binding.engine === "sqlite" ? [sqliteFileId(binding.sqlitePath)] : binding.databases))
    .filter((value): value is string => !!value);
  const phrase = `replace ${database}`;
  const restore = useOperationMutation(() =>
    api.backups.restore({ artifact: artifact.path, appId: summary?.id ?? "", database, confirm }),
  );
  const validTarget = targets.includes(database);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Restore {artifact.appSlug} / {artifact.database}
          </DialogTitle>
          <DialogDescription>
            Not atomic — a failure can leave partial data. Try a spare database first.
          </DialogDescription>
        </DialogHeader>
        {!summary && !apps.isPending && <Alert variant="destructive">App {artifact.appSlug} no longer exists.</Alert>}
        {detail.isPending && <DomainLoading label="bindings" />}
        {detail.error && <Alert variant="destructive">{messageOf(detail.error)}</Alert>}
        {summary && detail.data && targets.length === 0 && (
          <Alert variant="destructive">No {artifact.engine} binding can receive this.</Alert>
        )}
        {artifact.engine === "sqlite" && summary && summary.desiredRuntime !== "stopped" && (
          <Alert variant="destructive">Stop the app first — SQLite restore needs it stopped.</Alert>
        )}
        <Field label="Target">
          <NativeSelect
            className="w-full"
            value={validTarget ? database : ""}
            disabled={targets.length === 0}
            onChange={(event) => {
              setDatabase(event.target.value);
              setConfirm("");
            }}
          >
            <option value="" disabled>
              Select a database
            </option>
            {targets.map((target) => (
              <option key={target} value={target}>
                {target}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label={`Type “${phrase}” to confirm`}>
          <Input value={confirm} onChange={(event) => setConfirm(event.target.value)} autoComplete="off" />
        </Field>
        {restore.error && <Alert variant="destructive">{messageOf(restore.error)}</Alert>}
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            disabled={
              !summary ||
              !validTarget ||
              confirm !== phrase ||
              restore.isPending ||
              (artifact.engine === "sqlite" && summary.desiredRuntime !== "stopped")
            }
            onClick={() => restore.mutate(undefined, { onSuccess: onClose })}
          >
            Restore
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function sqliteFileId(path?: string): string | null {
  if (!path) return null;
  const parts = path.split("/").filter(Boolean);
  return parts.length >= 2 ? (parts.at(-2) ?? null) : null;
}

function DeleteDialog({ artifact, onClose }: { artifact: T.BackupArtifact; onClose: () => void }) {
  const remove = useOperationMutation(() => api.backups.remove({ artifact: artifact.path, confirm: "delete" }));
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete this backup?</DialogTitle>
          <DialogDescription>
            <code className="break-all">{artifact.path}</code> will be permanently removed from disk.
          </DialogDescription>
        </DialogHeader>
        {remove.error && <Alert variant="destructive">{messageOf(remove.error)}</Alert>}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            No
          </Button>
          <Button
            variant="destructive"
            disabled={remove.isPending}
            onClick={() => remove.mutate(undefined, { onSuccess: onClose })}
          >
            Yes, delete
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
