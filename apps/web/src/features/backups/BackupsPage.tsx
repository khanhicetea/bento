import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import {
  DomainError,
  DomainLoading,
  EmptyPanel,
  Field,
  Page,
  PageHeader,
  Panel,
  StateBadge,
} from "../../components/DomainState.tsx";
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
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

type Tab = "artifacts" | "runs" | "schedule";

export function BackupsPage() {
  const [tab, setTab] = useState<Tab>("artifacts");
  const [runOpen, setRunOpen] = useState(false);
  const [restoreTarget, setRestoreTarget] = useState<T.BackupArtifact | null>(null);
  return (
    <Page>
      <PageHeader
        title="Backups"
        description="Logical backups publish atomically only when complete and non-empty."
        actions={<Button onClick={() => setRunOpen(true)}>Back up now</Button>}
      />
      <div className="mb-6 flex gap-1 border-b" role="tablist" aria-label="Backup sections">
        {(["artifacts", "runs", "schedule"] as const).map((value) => (
          <Button
            key={value}
            role="tab"
            aria-selected={tab === value}
            variant={tab === value ? "default" : "ghost"}
            onClick={() => setTab(value)}
          >
            {value[0]?.toUpperCase() + value.slice(1)}
          </Button>
        ))}
      </div>
      {tab === "artifacts" && <ArtifactsTab onRestore={setRestoreTarget} />}
      {tab === "runs" && <RunsTab />}
      {tab === "schedule" && <ScheduleForm />}
      <BackupNowDialog open={runOpen} onOpenChange={setRunOpen} />
      {restoreTarget && <RestoreDialog artifact={restoreTarget} onClose={() => setRestoreTarget(null)} />}
    </Page>
  );
}

function ArtifactsTab({ onRestore }: { onRestore: (artifact: T.BackupArtifact) => void }) {
  const query = useQuery({ queryKey: keys.backups.artifacts, queryFn: ({ signal }) => api.backups.artifacts(signal) });
  if (query.isPending) return <DomainLoading label="backup artifacts" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  const artifacts = [...query.data.artifacts].sort((a, b) => b.createdAt.localeCompare(a.createdAt));
  return (
    <Panel
      title="Artifacts"
      description="Newest first. Restore is limited to currently recorded bindings of the same engine."
    >
      {artifacts.length === 0 ? (
        <EmptyPanel>No backup artifacts yet.</EmptyPanel>
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Application</TableHead>
                <TableHead>Database</TableHead>
                <TableHead>Engine</TableHead>
                <TableHead>Size</TableHead>
                <TableHead>Created</TableHead>
                <TableHead>
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {artifacts.map((artifact) => (
                <TableRow key={artifact.path}>
                  <TableCell>
                    <strong>{artifact.appSlug}</strong>
                    <div className="max-w-56 truncate text-xs text-muted-foreground" title={artifact.path}>
                      {artifact.path}
                    </div>
                  </TableCell>
                  <TableCell>
                    <code>{artifact.database}</code>
                  </TableCell>
                  <TableCell>{artifact.engine}</TableCell>
                  <TableCell>{formatBytes(artifact.sizeBytes)}</TableCell>
                  <TableCell title={artifact.createdAt}>{formatRelative(artifact.createdAt)}</TableCell>
                  <TableCell>
                    <Button size="xs" variant="outline" onClick={() => onRestore(artifact)}>
                      Restore…
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </Panel>
  );
}

function RunsTab() {
  const query = useQuery({ queryKey: keys.backups.runs, queryFn: ({ signal }) => api.backups.runs(signal) });
  if (query.isPending) return <DomainLoading label="backup runs" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  return (
    <Panel title="Runs" description="Recent backup batches, durations, artifact counts, and upload results.">
      {query.data.runs.length === 0 ? (
        <EmptyPanel>No backup runs yet.</EmptyPanel>
      ) : (
        <div className="grid">
          {query.data.runs.map((run) => (
            <details key={run.id} className="border-t py-3 first:border-0">
              <summary className="flex cursor-pointer list-none flex-wrap items-center gap-2 text-sm">
                <StateBadge state={run.state} />
                <strong>{run.trigger}</strong>
                <span className="text-muted-foreground" title={run.startedAt}>
                  {formatRelative(run.startedAt)} · {formatDuration(run.startedAt, run.finishedAt)} ·{" "}
                  {run.artifacts.length} artifact(s)
                </span>
                {run.uploadState && <span className="ml-auto text-xs">Upload: {run.uploadState}</span>}
              </summary>
              {run.error && (
                <Alert variant="destructive" className="mt-2">
                  {run.error}
                </Alert>
              )}
              {run.artifacts.length > 0 && (
                <ul className="mb-0 text-xs text-muted-foreground">
                  {run.artifacts.map((artifact) => (
                    <li key={artifact}>
                      <code>{artifact}</code>
                    </li>
                  ))}
                </ul>
              )}
            </details>
          ))}
        </div>
      )}
    </Panel>
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
          <DialogDescription>Creates logical artifacts for every application binding.</DialogDescription>
        </DialogHeader>
        <Field label="Compression">
          <NativeSelect value={compression} onChange={(event) => setCompression(event.target.value)}>
            <option value="zstd">zstd</option>
            <option value="gzip">gzip</option>
            <option value="none">none</option>
          </NativeSelect>
        </Field>
        <label className="flex items-center gap-2 text-sm">
          <Checkbox checked={upload} onCheckedChange={(checked) => setUpload(checked === true)} />
          Upload with configured rclone remote
        </label>
        {run.error && <Alert variant="destructive">{messageOf(run.error)}</Alert>}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            disabled={run.isPending}
            onClick={() => run.mutate(undefined, { onSuccess: () => onOpenChange(false) })}
          >
            Start backup
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ScheduleForm() {
  const query = useQuery({ queryKey: keys.backups.schedule, queryFn: ({ signal }) => api.backups.schedule(signal) });
  if (query.isPending) return <DomainLoading label="backup schedule" />;
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
    <Panel
      title="Schedule"
      description={`Missed slots while the backend is down are recorded, not replayed.${initial.lastState ? ` Last: ${initial.lastState}${initial.lastRun ? ` ${formatRelative(initial.lastRun)}` : ""}.` : ""}`}
    >
      <div className="grid items-end gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <label className="flex items-center gap-2 text-sm">
          <Checkbox
            checked={schedule.enabled}
            onCheckedChange={(checked) => setSchedule({ ...schedule, enabled: checked === true })}
          />
          Enabled
        </label>
        <Field
          label="Cron (5 fields)"
          hint={`${formatCron(schedule.cron)}${initial.nextRun ? ` · next ${formatRelative(initial.nextRun)}` : ""}`}
        >
          <Input value={schedule.cron} onChange={(event) => setSchedule({ ...schedule, cron: event.target.value })} />
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
      <div className="mt-4 flex items-center gap-3">
        <Button disabled={!dirty || save.isPending} onClick={() => save.mutate(schedule)}>
          Save schedule
        </Button>
        {dirty && <span className="text-xs text-warning">Unsaved changes</span>}
      </div>
      {save.error && (
        <Alert variant="destructive" className="mt-3">
          {messageOf(save.error)}
        </Alert>
      )}
    </Panel>
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
            Restore is not atomic per object and failure may leave a partial result. Prefer a verification database
            first.
          </DialogDescription>
        </DialogHeader>
        {!summary && !apps.isPending && <Alert variant="destructive">App {artifact.appSlug} no longer exists.</Alert>}
        {detail.isPending && <DomainLoading label="application bindings" />}
        {detail.error && <Alert variant="destructive">{messageOf(detail.error)}</Alert>}
        {summary && detail.data && targets.length === 0 && (
          <Alert variant="destructive">No current {artifact.engine} binding can receive this artifact.</Alert>
        )}
        {artifact.engine === "sqlite" && summary && (
          <Alert variant={summary.desiredRuntime === "stopped" ? "default" : "destructive"}>
            SQLite restore requires the application to be stopped. Current desired state:{" "}
            <strong>{summary.desiredRuntime}</strong>.
          </Alert>
        )}
        <Field label="Target database" hint="Only currently recorded bindings with the artifact's engine are listed.">
          <NativeSelect
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
          <Button variant="outline" onClick={onClose}>
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
