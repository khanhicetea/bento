import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, History, KeyRound, RefreshCw, ShieldCheck, Unlock } from "lucide-react";
import { Link } from "wouter";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import { Cell, CopyableCode, DomainError, DomainLoading, Field, KeyValues } from "../../components/DomainState.tsx";
import { formatBytes, formatRelative } from "../../lib/format.ts";
import { useTrackOperation } from "../operations/OperationTracker.tsx";
import { useOperationMutation } from "./useApplications.ts";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";

/** App-scoped restic backups: one encrypted repository per app on an rclone remote. */
export function BackupPanel({ app }: { app: T.App }) {
  const query = useQuery({
    queryKey: keys.apps.restic(app.id),
    queryFn: ({ signal }) => api.apps.restic.get(app.id, signal),
  });
  if (query.isPending) return <DomainLoading label="backup settings" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  const restic = query.data;
  return (
    <div className="box box--2">
      <SettingsCell app={app} restic={restic} />
      {restic.configured && <RepositoryCell app={app} restic={restic} />}
      {restic.initialized && <SnapshotsCell app={app} restic={restic} />}
      {restic.initialized && <KeysCell app={app} restic={restic} />}
    </div>
  );
}

const lines = (value: string) =>
  value
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);

function SettingsCell({ app, restic }: { app: T.App; restic: T.Restic }) {
  const queryClient = useQueryClient();
  const current = restic.settings;
  const [repository, setRepository] = useState(current.repository);
  const [paths, setPaths] = useState(current.paths.join("\n"));
  const [excludes, setExcludes] = useState(current.excludes.join("\n"));
  const [sqlitePaths, setSqlitePaths] = useState(current.sqlitePaths.join("\n"));
  const [defaultExcludes, setDefaultExcludes] = useState(current.defaultExcludes);
  const [retention, setRetention] = useState(current.retention);
  const [schedule, setSchedule] = useState(current.schedule);
  const rclone = useQuery({ queryKey: keys.backups.rclone, queryFn: ({ signal }) => api.backups.rclone(signal) });
  const save = useMutation({
    mutationFn: () =>
      api.apps.restic.save(app.id, {
        repository: repository.trim(),
        paths: lines(paths),
        excludes: lines(excludes),
        sqlitePaths: lines(sqlitePaths),
        defaultExcludes,
        retention,
        schedule: { enabled: schedule.enabled, cron: schedule.cron.trim() },
      }),
    onSuccess: (next) => queryClient.setQueryData(keys.apps.restic(app.id), next),
  });
  const remotes = rclone.data?.remotes ?? [];
  const textarea = "min-h-20 w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-sm";
  return (
    <Cell title="Backup settings" className="cell--wide">
      <form
        className="grid gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          save.mutate();
        }}
      >
        <Field
          label="Repository"
          hint={
            <>
              An rclone <code>remote:path</code>, one repository per app.{" "}
              {remotes.length > 0 ? (
                <>Remotes: {remotes.map((remote) => remote.name).join(", ")}.</>
              ) : (
                <>
                  Add a remote in <Link href="/backups">Backups → rclone</Link> first.
                </>
              )}
              {restic.initialized && " Changing it disconnects the current repository."}
            </>
          }
        >
          <Input
            value={repository}
            placeholder={`${remotes[0]?.name ?? "b2"}:bento/apps/${app.slug}`}
            spellCheck={false}
            onChange={(event) => setRepository(event.target.value)}
          />
        </Field>
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="Paths" hint="One per line, relative to the app home. . is the whole home (recommended).">
            <textarea className={textarea} value={paths} onChange={(event) => setPaths(event.target.value)} />
          </Field>
          <Field
            label="Excludes"
            hint="restic patterns. With a / they start at the home (app/storage/cache); without, they match any name (node_modules). Prefix ! to re-include. A .nobackup file excludes its directory."
          >
            <textarea className={textarea} value={excludes} onChange={(event) => setExcludes(event.target.value)} />
          </Field>
          <Field
            label="SQLite files in the home"
            hint="Copied consistently with sqlite .backup instead of while live. The scheduler's database is always included."
          >
            <textarea
              className={textarea}
              value={sqlitePaths}
              placeholder="app/database/database.sqlite"
              onChange={(event) => setSqlitePaths(event.target.value)}
            />
          </Field>
          <div className="grid content-start gap-3">
            <label className="check">
              <Checkbox checked={defaultExcludes} onCheckedChange={(checked) => setDefaultExcludes(checked === true)} />
              Skip caches ({restic.defaultExcludes.join(", ")})
            </label>
            <Field label="Keep snapshots" hint="Hourly, daily, weekly, monthly.">
              <div className="grid grid-cols-4 gap-2">
                {(["hourly", "daily", "weekly", "monthly"] as const).map((unit) => (
                  <Input
                    key={unit}
                    type="number"
                    min={0}
                    aria-label={`Keep ${unit}`}
                    value={retention[unit]}
                    onChange={(event) => setRetention({ ...retention, [unit]: Number(event.target.value) })}
                  />
                ))}
              </div>
            </Field>
            <Field label="Schedule" hint="Cron in the server's time zone. Backups run while the app keeps running.">
              <div className="flex items-center gap-3">
                <label className="check">
                  <Checkbox
                    checked={schedule.enabled}
                    onCheckedChange={(checked) => setSchedule({ ...schedule, enabled: checked === true })}
                  />
                  Enabled
                </label>
                <Input
                  value={schedule.cron}
                  spellCheck={false}
                  placeholder="30 3 * * *"
                  onChange={(event) => setSchedule({ ...schedule, cron: event.target.value })}
                />
              </div>
            </Field>
          </div>
        </div>
        <div>
          <Button type="submit" disabled={!repository.trim() || save.isPending}>
            {restic.configured ? "Save" : "Save settings"}
          </Button>
        </div>
        {save.error && <p className="note note--bad">{messageOf(save.error)}</p>}
      </form>
    </Cell>
  );
}

function RepositoryCell({ app, restic }: { app: T.App; restic: T.Restic }) {
  const track = useTrackOperation();
  // A key is returned once, by the request that created it.
  const [createdKey, setCreatedKey] = useState("");
  const [connectKey, setConnectKey] = useState("");
  const init = useMutation({
    mutationFn: () => api.apps.restic.init(app.id),
    onSuccess: ({ key, ...accepted }) => {
      track(accepted);
      setCreatedKey(key);
    },
  });
  const connect = useOperationMutation(() => api.apps.restic.connect(app.id, connectKey.trim()));
  const backup = useOperationMutation(() => api.apps.restic.action(app.id, "backup"));
  const check = useOperationMutation(() => api.apps.restic.action(app.id, "check"));
  const refresh = useOperationMutation(() => api.apps.restic.action(app.id, "refresh"));
  const unlock = useOperationMutation(() => api.apps.restic.action(app.id, "unlock"));
  const last = restic.lastBackup;
  const keyNotice = createdKey && (
    <div className="my-3 grid gap-2">
      <CopyableCode value={createdKey} />
      <p className="note font-medium">
        Copy this key and store it somewhere safe now; it is not shown again. It decrypts every backup of this app. If
        every key is lost, the backups cannot be recovered.
      </p>
    </div>
  );
  if (!restic.initialized) {
    return (
      <Cell title="Repository" className="cell--wide">
        <p className="note mb-3">
          Create a new encrypted repository at <code>{restic.settings.repository}</code>, or connect one that already
          exists (for example to restore another stack's backup into this app) with one of its keys.
        </p>
        {keyNotice}
        <div className="grid gap-3 md:grid-cols-2">
          <div>
            <Button disabled={init.isPending} onClick={() => init.mutate()}>
              <Archive /> Create repository
            </Button>
            {init.error && <p className="note note--bad mt-2">{messageOf(init.error)}</p>}
          </div>
          <form
            className="flex gap-2"
            onSubmit={(event) => {
              event.preventDefault();
              connect.mutate(undefined, { onSuccess: () => setConnectKey("") });
            }}
          >
            <Input
              type="password"
              value={connectKey}
              placeholder="Existing repository key"
              autoComplete="off"
              onChange={(event) => setConnectKey(event.target.value)}
            />
            <Button type="submit" variant="outline" disabled={!connectKey.trim() || connect.isPending}>
              Connect
            </Button>
          </form>
        </div>
        {connect.error && <p className="note note--bad mt-2">{messageOf(connect.error)}</p>}
      </Cell>
    );
  }
  return (
    <Cell title="Repository" className="cell--wide">
      {keyNotice}
      <KeyValues
        items={[
          ["Repository", <code>{restic.settings.repository}</code>],
          [
            "Last backup",
            last ? (
              last.ok ? (
                `${formatRelative(last.at)} · ${last.filesTotal} files, ${formatBytes(last.bytesAdded)} added`
              ) : (
                <span className="note--bad">
                  failed {formatRelative(last.at)}: {last.error}
                </span>
              )
            ) : (
              "never"
            ),
          ],
          ["Next run", restic.nextRun ? formatRelative(restic.nextRun) : "not scheduled"],
          [
            "Last check",
            restic.lastCheck
              ? `${restic.lastCheck.ok ? "ok" : "failed"} ${formatRelative(restic.lastCheck.at)}`
              : "never",
          ],
          ["Last prune", restic.lastPruneAt ? formatRelative(restic.lastPruneAt) : "never"],
        ]}
      />
      <div className="mt-3 flex flex-wrap gap-2">
        <Button disabled={backup.isPending} onClick={() => backup.mutate(undefined)}>
          <Archive /> Back up now
        </Button>
        <Button variant="outline" disabled={check.isPending} onClick={() => check.mutate(undefined)}>
          <ShieldCheck /> Verify (5% of data)
        </Button>
        <Button variant="outline" disabled={refresh.isPending} onClick={() => refresh.mutate(undefined)}>
          <RefreshCw /> Refresh
        </Button>
        <Button
          variant="outline"
          disabled={unlock.isPending}
          title="restic unlock: removes locks older than 30 minutes or left by finished processes; running backups keep theirs"
          onClick={() => unlock.mutate(undefined)}
        >
          <Unlock /> Remove stale locks
        </Button>
      </div>
      {[backup.error, check.error, refresh.error, unlock.error].map(
        (error, index) =>
          error && (
            <p key={index} className="note note--bad mt-2">
              {messageOf(error)}
            </p>
          ),
      )}
    </Cell>
  );
}

function SnapshotsCell({ app, restic }: { app: T.App; restic: T.Restic }) {
  const [target, setTarget] = useState<T.ResticSnapshot | null>(null);
  const [files, setFiles] = useState(true);
  const [databases, setDatabases] = useState(true);
  const restore = useOperationMutation((confirm: string) =>
    api.apps.restic.restore(app.id, { snapshot: target?.id ?? "", files, databases, confirm }),
  );
  const running = app.desiredRuntime === "running";
  return (
    <Cell title="Snapshots" className="cell--wide">
      {restic.snapshots.length === 0 ? (
        <p className="note">No snapshots yet.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-left text-muted-foreground">
              <tr>
                <th className="py-1 pr-3 font-medium">Snapshot</th>
                <th className="py-1 pr-3 font-medium">Taken</th>
                <th className="py-1 pr-3 font-medium">Trigger</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {restic.snapshots.map((snapshot) => (
                <tr key={snapshot.id} className="border-t">
                  <td>
                    <code>{snapshot.shortId}</code>
                  </td>
                  <td title={snapshot.time}>{formatRelative(snapshot.time)}</td>
                  <td>
                    {snapshot.tags
                      .filter((tag) => tag.startsWith("trigger=") || tag.startsWith("slug="))
                      .map((tag) => tag.split("=")[1])
                      .join(" · ")}
                  </td>
                  <td className="text-right">
                    <Button size="sm" variant="outline" onClick={() => setTarget(snapshot)}>
                      <History /> Restore
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <ConfirmDialog
        open={target !== null}
        onOpenChange={(open) => !open && setTarget(null)}
        title={`Restore snapshot ${target?.shortId ?? ""}?`}
        description={
          running
            ? "Stop the app first: restore replaces its files and databases and refuses to run while the app is running."
            : "Files: the home (or the backed-up paths) is replaced; the previous files are kept under homes/.pre-restore-*. Databases: each bound database is replaced by its dump; the service version must match the snapshot's."
        }
        confirmLabel="Restore"
        phrase={`restore ${app.slug}`}
        destructive
        pending={restore.isPending}
        error={restore.error}
        onConfirm={(typed) => restore.mutate(typed, { onSuccess: () => setTarget(null) })}
      />
      {target && (
        <div className="mt-3 flex gap-5">
          <label className="check">
            <Checkbox checked={files} onCheckedChange={(checked) => setFiles(checked === true)} />
            Restore files
          </label>
          <label className="check">
            <Checkbox checked={databases} onCheckedChange={(checked) => setDatabases(checked === true)} />
            Restore databases
          </label>
        </div>
      )}
    </Cell>
  );
}

function KeysCell({ app, restic }: { app: T.App; restic: T.Restic }) {
  const track = useTrackOperation();
  const [label, setLabel] = useState("");
  const [addOpen, setAddOpen] = useState(false);
  const [newKey, setNewKey] = useState("");
  const add = useMutation({
    mutationFn: (confirm: string) => api.apps.restic.addKey(app.id, { label: label.trim(), confirm }),
    onSuccess: ({ key, ...accepted }) => {
      track(accepted);
      setNewKey(key);
      setAddOpen(false);
      setLabel("");
    },
  });
  const remove = useOperationMutation((keyId: string) => api.apps.restic.removeKey(app.id, keyId));
  return (
    <Cell title="Access keys" className="cell--wide">
      <p className="note mb-3">
        Give another Bento (or a person) its own key to restore this app's backups, then remove it when done. The key
        Bento uses is never shown.
      </p>
      <KeyValues
        items={restic.keys.map((key): [string, React.ReactNode] => [
          key.id.slice(0, 8),
          <span className="flex items-center gap-3">
            {key.userName} · {formatRelative(key.created)}
            {key.current ? (
              <span className="note">used by Bento</span>
            ) : (
              <Button size="sm" variant="outline" disabled={remove.isPending} onClick={() => remove.mutate(key.id)}>
                Remove
              </Button>
            )}
          </span>,
        ])}
      />
      {newKey && (
        <div className="my-3 grid gap-2">
          <CopyableCode value={newKey} />
          <p className="note font-medium">Copy this key now; it is not shown again.</p>
        </div>
      )}
      <form
        className="mt-3 flex gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          setAddOpen(true);
        }}
      >
        <Input
          value={label}
          placeholder="Label, e.g. staging"
          spellCheck={false}
          onChange={(event) => setLabel(event.target.value)}
        />
        <Button type="submit" variant="outline">
          <KeyRound /> Add key
        </Button>
      </form>
      {remove.error && <p className="note note--bad mt-2">{messageOf(remove.error)}</p>}
      <ConfirmDialog
        open={addOpen}
        onOpenChange={setAddOpen}
        title="Add an access key?"
        description="The new key decrypts every backup of this app. It is shown once."
        confirmLabel="Add key"
        phrase={`export ${app.slug}`}
        pending={add.isPending}
        error={add.error}
        onConfirm={(typed) => add.mutate(typed)}
      />
    </Cell>
  );
}
