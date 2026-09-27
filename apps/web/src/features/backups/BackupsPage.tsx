import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { EmptyPanel, Field, Page, PageHeader, Panel, StateBadge } from "../../components/DomainState.tsx";
import { useApplicationList, useOperationMutation } from "../applications/useApplications.ts";
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
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";

export function BackupsPage() {
  const artifacts = useQuery({
    queryKey: keys.backups.artifacts,
    queryFn: ({ signal }) => api.backups.artifacts(signal),
  });
  const runs = useQuery({ queryKey: keys.backups.runs, queryFn: ({ signal }) => api.backups.runs(signal) });
  const [compression, setCompression] = useState("zstd");
  const [upload, setUpload] = useState(false);
  const [restoreTarget, setRestoreTarget] = useState<T.BackupArtifact | null>(null);
  const run = useOperationMutation(() => api.backups.run({ scope: "all", compression, upload }));
  return (
    <Page>
      <PageHeader
        section="Backups"
        title="Backups"
        description="Logical backups of every app binding. Artifacts publish atomically only when complete and non-empty; retention runs only after a fully successful batch."
        actions={
          <>
            <NativeSelect value={compression} onChange={(e) => setCompression(e.target.value)}>
              <option value="zstd">zstd</option>
              <option value="gzip">gzip</option>
              <option value="none">none</option>
            </NativeSelect>
            <label className="flex items-center gap-1 text-sm">
              <input type="checkbox" checked={upload} onChange={(e) => setUpload(e.target.checked)} /> upload
            </label>
            <Button onClick={() => run.mutate(undefined)} disabled={run.isPending}>
              Back up now
            </Button>
          </>
        }
      />
      {run.error && (
        <Alert variant="destructive" className="mb-4">
          {messageOf(run.error)}
        </Alert>
      )}
      <ScheduleForm />
      <Panel title="Artifacts">
        {(artifacts.data?.artifacts ?? []).length === 0 ? (
          <EmptyPanel>No backup artifacts yet.</EmptyPanel>
        ) : (
          <table className="w-full text-sm">
            <thead className="text-left text-xs text-muted-foreground uppercase">
              <tr>
                {["Artifact", "Engine", "Database", "Size", "Created", ""].map((h) => (
                  <th key={h} className="py-2">
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {artifacts.data?.artifacts.map((a) => (
                <tr key={a.path} className="border-t border-border">
                  <td className="py-2 text-xs">
                    <code>{a.path}</code>
                  </td>
                  <td>{a.engine}</td>
                  <td>{a.database}</td>
                  <td>{(a.sizeBytes / 1024).toFixed(1)} KiB</td>
                  <td className="text-xs">{a.createdAt}</td>
                  <td>
                    <Button size="xs" variant="outline" onClick={() => setRestoreTarget(a)}>
                      Restore…
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
      <Panel title="Recent runs">
        {(runs.data?.runs ?? []).map((r) => (
          <div
            key={r.id}
            className="flex items-center justify-between border-t border-border py-2 text-sm first:border-0"
          >
            <span>
              <StateBadge state={r.state} /> {r.trigger} · {r.startedAt} · {r.artifacts.length} artifact(s){" "}
              {r.uploadState && `· upload ${r.uploadState}`}
            </span>
            {r.error && <span className="text-xs text-destructive">{r.error}</span>}
          </div>
        ))}
      </Panel>
      {restoreTarget && <RestoreDialog artifact={restoreTarget} onClose={() => setRestoreTarget(null)} />}
    </Page>
  );
}

function ScheduleForm() {
  const q = useQuery({ queryKey: keys.backups.schedule, queryFn: ({ signal }) => api.backups.schedule(signal) });
  if (!q.data) return null;
  return <ScheduleEditor key={JSON.stringify(q.data)} initial={q.data} />;
}

function ScheduleEditor({ initial }: { initial: T.BackupSchedule }) {
  const queryClient = useQueryClient();
  const [s, setS] = useState(initial);
  const save = useMutation({
    mutationFn: api.backups.setSchedule,
    onSuccess: (data) => queryClient.setQueryData(keys.backups.schedule, data),
  });
  return (
    <Panel
      title="Schedule"
      description={`Evaluated by the backend. Slots missed while it was down are recorded, not replayed.${initial.lastState ? ` Last: ${initial.lastState}${initial.lastRun ? ` at ${initial.lastRun}` : ""}.` : ""}`}
    >
      <div className="grid grid-cols-5 items-end gap-3 max-[900px]:grid-cols-2">
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={s.enabled} onChange={(e) => setS({ ...s, enabled: e.target.checked })} />{" "}
          Enabled
        </label>
        <Field label="Cron (5 fields)">
          <Input value={s.cron} onChange={(e) => setS({ ...s, cron: e.target.value })} />
        </Field>
        <Field label="Keep per database">
          <Input type="number" value={s.retain} onChange={(e) => setS({ ...s, retain: Number(e.target.value) })} />
        </Field>
        <Field label="rclone remote">
          <Input
            value={s.rcloneRemote}
            placeholder="remote:bucket/path"
            onChange={(e) => setS({ ...s, rcloneRemote: e.target.value })}
          />
        </Field>
        <Button onClick={() => save.mutate(s)} disabled={save.isPending}>
          Save schedule
        </Button>
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
  const app = apps.data?.apps.find((a) => a.slug === artifact.appSlug);
  const [database, setDatabase] = useState(artifact.database);
  const [confirm, setConfirm] = useState("");
  const phrase = `replace ${database}`;
  const restore = useOperationMutation(() =>
    api.backups.restore({ artifact: artifact.path, appId: app?.id ?? "", database, confirm }),
  );
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Restore {artifact.path}</DialogTitle>
          <DialogDescription>
            Replaces the contents of an app-owned database. It is not atomic per object and may leave a partial result
            on failure. Prefer restoring into a verification database first. SQLite restores require the app to be
            stopped.
          </DialogDescription>
        </DialogHeader>
        {!app && <Alert variant="destructive">App {artifact.appSlug} no longer exists.</Alert>}
        <Field label="Target database">
          <Input value={database} onChange={(e) => setDatabase(e.target.value)} />
        </Field>
        <Field label={`Type "${phrase}" to confirm`}>
          <Input value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </Field>
        {restore.error && <Alert variant="destructive">{messageOf(restore.error)}</Alert>}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            disabled={!app || confirm !== phrase || restore.isPending}
            onClick={() => restore.mutate(undefined, { onSuccess: onClose })}
          >
            Restore
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
