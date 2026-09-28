import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, ChevronRight, RotateCcw } from "lucide-react";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import {
  Cell,
  DomainError,
  DomainLoading,
  EmptyState,
  Field,
  PageHeader,
  StateBadge,
} from "../../components/DomainState.tsx";
import { EngineLogo } from "../../components/EngineLogo.tsx";
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

type Tab = "artifacts" | "runs" | "schedule";
const tabLabels: Record<Tab, string> = { artifacts: "Files", runs: "Runs", schedule: "Schedule" };

export function BackupsPage() {
  const [tab, setTab] = useState<Tab>("artifacts");
  const [runOpen, setRunOpen] = useState(false);
  const [restoreTarget, setRestoreTarget] = useState<T.BackupArtifact | null>(null);
  return (
    <>
      <PageHeader title="Backups" actions={<Button onClick={() => setRunOpen(true)}>Back up now</Button>} />
      <div className="seg mb-5" role="tablist" aria-label="Backup sections">
        {(["artifacts", "runs", "schedule"] as const).map((value) => (
          <button key={value} type="button" role="tab" aria-selected={tab === value} onClick={() => setTab(value)}>
            {tabLabels[value]}
          </button>
        ))}
      </div>
      {tab === "artifacts" && <ArtifactsTab onRestore={setRestoreTarget} />}
      {tab === "runs" && <RunsTab />}
      {tab === "schedule" && <ScheduleForm />}
      <BackupNowDialog open={runOpen} onOpenChange={setRunOpen} />
      {restoreTarget && <RestoreDialog artifact={restoreTarget} onClose={() => setRestoreTarget(null)} />}
    </>
  );
}

const engineLabels: Record<string, string> = { mysql: "MySQL", postgres: "PostgreSQL", sqlite: "SQLite" };

function ArtifactsTab({ onRestore }: { onRestore: (artifact: T.BackupArtifact) => void }) {
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
                <Button size="sm" variant="outline" onClick={() => onRestore(artifact)}>
                  <RotateCcw /> Restore
                </Button>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function RunsTab() {
  const query = useQuery({ queryKey: keys.backups.runs, queryFn: ({ signal }) => api.backups.runs(signal) });
  if (query.isPending) return <DomainLoading label="runs" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  return (
    <div className="box">
      <div className="cell">
        {query.data.runs.length === 0 ? (
          <EmptyState icon={<Archive />} title="No runs yet" />
        ) : (
          <div className="rows rows--lined">
            {query.data.runs.map((run) => (
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
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function BackupNowDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const [compression, setCompression] = useState("zstd");
  const [upload, setUpload] = useState(false);
  const run = useOperationMutation(() => api.backups.run({ scope: "all", compression, upload }));
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Back up now</DialogTitle>
          <DialogDescription>Every app database.</DialogDescription>
        </DialogHeader>
        <Field label="Compression">
          <NativeSelect className="w-full" value={compression} onChange={(event) => setCompression(event.target.value)}>
            <option value="zstd">zstd</option>
            <option value="gzip">gzip</option>
            <option value="none">none</option>
          </NativeSelect>
        </Field>
        <label className="check">
          <Checkbox checked={upload} onCheckedChange={(checked) => setUpload(checked === true)} />
          Upload to rclone remote
        </label>
        {run.error && <Alert variant="destructive">{messageOf(run.error)}</Alert>}
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            disabled={run.isPending}
            onClick={() => run.mutate(undefined, { onSuccess: () => onOpenChange(false) })}
          >
            Start
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ScheduleForm() {
  const query = useQuery({ queryKey: keys.backups.schedule, queryFn: ({ signal }) => api.backups.schedule(signal) });
  if (query.isPending) return <DomainLoading label="schedule" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  return <ScheduleEditor initial={query.data} />;
}

function ScheduleEditor({ initial }: { initial: T.BackupSchedule }) {
  const queryClient = useQueryClient();
  const [schedule, setSchedule] = useState(initial);
  const save = useMutation({
    mutationFn: api.backups.setSchedule,
    onSuccess: (data) => {
      queryClient.setQueryData(keys.backups.schedule, data);
      setSchedule(data);
    },
  });
  const dirty = JSON.stringify(schedule) !== JSON.stringify(initial);
  return (
    <div className="box box--3">
      <Cell>
        <div className="metric">
          <strong className="text-xl!">{initial.enabled ? formatCron(initial.cron) : "Off"}</strong>
          <span>Schedule</span>
        </div>
      </Cell>
      <Cell>
        <div className="metric">
          <strong className="text-xl!">{initial.nextRun ? formatRelative(initial.nextRun) : "—"}</strong>
          <span>Next run</span>
        </div>
      </Cell>
      <Cell>
        <div className="metric">
          <strong className="text-xl!">{initial.lastRun ? formatRelative(initial.lastRun) : "—"}</strong>
          <span>Last run{initial.lastState ? ` · ${initial.lastState}` : ""}</span>
        </div>
      </Cell>
      <Cell className="cell--wide">
        <div className="grid gap-4">
          <label className="check">
            <Checkbox
              checked={schedule.enabled}
              onCheckedChange={(checked) => setSchedule({ ...schedule, enabled: checked === true })}
            />
            Enabled
          </label>
          <div className="grid-2">
            <Field label="Cron" hint={formatCron(schedule.cron)}>
              <Input
                className="font-mono"
                value={schedule.cron}
                onChange={(event) => setSchedule({ ...schedule, cron: event.target.value })}
              />
            </Field>
            <Field label="Keep per database">
              <Input
                type="number"
                min="1"
                value={schedule.retain}
                onChange={(event) => setSchedule({ ...schedule, retain: Number(event.target.value) })}
              />
            </Field>
            <Field label="Compression">
              <NativeSelect
                className="w-full"
                value={schedule.compression}
                onChange={(event) => setSchedule({ ...schedule, compression: event.target.value })}
              >
                <option value="zstd">zstd</option>
                <option value="gzip">gzip</option>
                <option value="none">none</option>
              </NativeSelect>
            </Field>
            <Field label="rclone remote">
              <Input
                value={schedule.rcloneRemote}
                placeholder="remote:bucket/path"
                onChange={(event) => setSchedule({ ...schedule, rcloneRemote: event.target.value })}
              />
            </Field>
          </div>
          <p className="note">Missed runs are not replayed.</p>
        </div>
      </Cell>
      <div className="cell cell--wide cell--muted flex flex-wrap items-center justify-end gap-3 py-3!">
        {save.error && <span className="note note--bad mr-auto">{messageOf(save.error)}</span>}
        {dirty && <span className="note">Unsaved</span>}
        <Button disabled={!dirty || save.isPending} onClick={() => save.mutate(schedule)}>
          Save
        </Button>
      </div>
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
