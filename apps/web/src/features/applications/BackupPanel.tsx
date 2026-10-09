import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Archive,
  History,
  KeyRound,
  Link2,
  RefreshCw,
  RotateCcw,
  Settings2,
  ShieldCheck,
  Trash2,
  TriangleAlert,
  Unlock,
} from "lucide-react";
import { Link } from "wouter";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import {
  Cell,
  CopyableCode,
  DomainError,
  DomainLoading,
  Field,
  KeyValues,
  StateBadge,
} from "../../components/DomainState.tsx";
import { formatBytes, formatRelative } from "../../lib/format.ts";
import { useTrackOperation } from "../operations/OperationTracker.tsx";
import { CloneFromBackupDialog } from "./CloneFromBackupDialog.tsx";
import { DatabaseBackupCard } from "./DatabaseBackupCard.tsx";
import { useOperationMutation } from "./useApplications.ts";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";

/**
 * The app's Backup tab: two independent methods, one box each. Database backup is the stack's dump schedules shown
 * per app; app backup is a restic repository per app on an rclone remote.
 */
export function BackupPanel({ app }: { app: T.App }) {
  return (
    <div className="grid">
      <DatabaseBackupCard app={app} />
      <AppBackupBox app={app} />
    </div>
  );
}

function AppBackupBox({ app }: { app: T.App }) {
  const query = useQuery({
    queryKey: keys.apps.restic(app.id),
    queryFn: ({ signal }) => api.apps.restic.get(app.id, signal),
  });
  if (query.isPending) return <DomainLoading label="backup settings" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  const restic = query.data;
  return (
    <section className="box box--2" aria-label="App backup">
      {restic.initialized && <RepositoryCell app={app} restic={restic} />}
      {restic.initialized && <SnapshotsCell app={app} restic={restic} />}
      {restic.configured && !restic.initialized && <SetupCell app={app} restic={restic} />}
      {/* Re-mounted when the saved settings change (another tab, a clone adopting the repository). */}
      <SettingsCell key={JSON.stringify(restic.settings)} app={app} restic={restic} />
      {restic.initialized && <KeysCell app={app} restic={restic} />}
    </section>
  );
}

const lines = (value: string) =>
  value
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);

const retentionUnits = ["hourly", "daily", "weekly", "monthly"] as const;

function SettingsCell({ app, restic }: { app: T.App; restic: T.Restic }) {
  const queryClient = useQueryClient();
  const current = restic.settings;
  const [repository, setRepository] = useState(current.repository);
  const [paths, setPaths] = useState(current.paths.join("\n"));
  const [excludes, setExcludes] = useState(current.excludes.join("\n"));
  const [sqlitePaths, setSqlitePaths] = useState(current.sqlitePaths.join("\n"));
  const [defaultExcludes, setDefaultExcludes] = useState(current.defaultExcludes);
  // Raw field text, so an emptied field is not silently saved as 0.
  const [retention, setRetention] = useState<Record<(typeof retentionUnits)[number], string>>({
    hourly: String(current.retention.hourly),
    daily: String(current.retention.daily),
    weekly: String(current.retention.weekly),
    monthly: String(current.retention.monthly),
  });
  const [retentionError, setRetentionError] = useState("");
  const [confirmRepo, setConfirmRepo] = useState(false);
  const [schedule, setSchedule] = useState(current.schedule);
  const [includeSecrets, setIncludeSecrets] = useState(current.includeSecrets);
  const rclone = useQuery({ queryKey: keys.backups.rclone, queryFn: ({ signal }) => api.backups.rclone(signal) });
  const save = useMutation({
    mutationFn: ({ kept, confirm }: { kept: T.ResticRetention; confirm?: string }) =>
      api.apps.restic.save(app.id, {
        repository: repository.trim(),
        paths: lines(paths),
        excludes: lines(excludes),
        sqlitePaths: lines(sqlitePaths),
        defaultExcludes,
        includeSecrets,
        retention: kept,
        schedule: { enabled: schedule.enabled, cron: schedule.cron.trim() },
        confirm,
      }),
    onSuccess: (next) => {
      setConfirmRepo(false);
      queryClient.setQueryData(keys.apps.restic(app.id), next);
    },
  });
  function parsedRetention(): T.ResticRetention | null {
    const out = { hourly: 0, daily: 0, weekly: 0, monthly: 0 };
    for (const unit of retentionUnits) {
      const raw = retention[unit].trim();
      if (!/^\d+$/.test(raw)) return null;
      out[unit] = Number(raw);
    }
    return out;
  }
  const repoChanges = restic.initialized && repository.trim() !== current.repository;
  const remotes = rclone.data?.remotes ?? [];
  const textarea = "min-h-20 w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-sm";
  return (
    <Cell
      title="Backup settings"
      icon={<Settings2 />}
      className="cell--wide"
      action={
        <Button type="submit" size="xs" disabled={!repository.trim() || save.isPending}>
          {save.isPending ? "Saving" : "Save"}
        </Button>
      }
      onSubmit={(event) => {
        event.preventDefault();
        const kept = parsedRetention();
        setRetentionError(kept ? "" : "✕ WHOLE NUMBERS ONLY");
        if (!kept) return;
        if (repoChanges) setConfirmRepo(true);
        else save.mutate({ kept });
      }}
    >
      <div className="grid gap-4">
        <div className="grid gap-3 md:grid-cols-2">
          <Field
            label="Repository"
            hint={
              remotes.length > 0 ? (
                <>rclone remote:path · {remotes.map((remote) => remote.name).join(", ")}</>
              ) : (
                <>
                  No remote · <Link href="/backups">add one in Backups</Link>
                </>
              )
            }
          >
            <Input
              value={repository}
              placeholder={`${remotes[0]?.name ?? "b2"}:bento/apps/${app.slug}`}
              spellCheck={false}
              onChange={(event) => setRepository(event.target.value)}
            />
          </Field>
          <Field label="Schedule" hint="Cron · server time · app stays up">
            <div className="flex items-center gap-3">
              <label className="check shrink-0">
                <Checkbox
                  checked={schedule.enabled}
                  onCheckedChange={(checked) => setSchedule({ ...schedule, enabled: checked === true })}
                />
                {schedule.enabled ? "ON" : "OFF"}
              </label>
              <Input
                value={schedule.cron}
                spellCheck={false}
                aria-label="Cron"
                placeholder="30 3 * * *"
                onChange={(event) => setSchedule({ ...schedule, cron: event.target.value })}
              />
            </div>
          </Field>
        </div>
        <Field label="Keep" hint={retentionError || "Snapshots per period · 0 keeps none"}>
          <div className="grid grid-cols-4 gap-2">
            {retentionUnits.map((unit) => (
              <label key={unit} className="grid gap-1">
                <Input
                  type="number"
                  min={0}
                  aria-label={`Keep ${unit}`}
                  value={retention[unit]}
                  onChange={(event) => setRetention({ ...retention, [unit]: event.target.value })}
                />
                <small className="text-xs text-muted-foreground uppercase">{unit}</small>
              </label>
            ))}
          </div>
        </Field>
        <details className="grid gap-3">
          <summary className="cursor-pointer text-xs font-semibold tracking-widest text-muted-foreground uppercase">
            Advanced
          </summary>
          <div className="mt-3 grid gap-3 md:grid-cols-3">
            <Field label="Paths" hint="One per line · . = whole home">
              <textarea className={textarea} value={paths} onChange={(event) => setPaths(event.target.value)} />
            </Field>
            <Field label="Excludes" hint="restic patterns · ! re-includes · .nobackup skips dir">
              <textarea className={textarea} value={excludes} onChange={(event) => setExcludes(event.target.value)} />
            </Field>
            <Field label="SQLite files" hint="Copied with .backup · scheduler DB always">
              <textarea
                className={textarea}
                value={sqlitePaths}
                placeholder="app/database/database.sqlite"
                onChange={(event) => setSqlitePaths(event.target.value)}
              />
            </Field>
          </div>
          <div className="mt-3 grid gap-3 md:grid-cols-2">
            <label className="check">
              <Checkbox checked={defaultExcludes} onCheckedChange={(checked) => setDefaultExcludes(checked === true)} />
              <span>
                Skip caches
                <small className="block text-muted-foreground">{restic.defaultExcludes.join(", ")}</small>
              </span>
            </label>
            <label className="check">
              <Checkbox checked={includeSecrets} onCheckedChange={(checked) => setIncludeSecrets(checked === true)} />
              <span>
                Include secrets
                <small className="block text-muted-foreground">
                  Env values and DB passwords. Anyone with a repository key can read them. Applies to later snapshots.
                </small>
              </span>
            </label>
          </div>
        </details>
        {save.error && !confirmRepo && <p className="note note--bad">{messageOf(save.error)}</p>}
      </div>
      <ConfirmDialog
        open={confirmRepo}
        onOpenChange={setConfirmRepo}
        title="Disconnect the current repository?"
        description={`The app stops using ${current.repository} and its snapshots are no longer listed here. The repository and its snapshots are not deleted; Bento keeps its key on the server. Create or connect the new repository afterwards.`}
        confirmLabel="Change repository"
        phrase={`disconnect ${app.slug}`}
        destructive
        pending={save.isPending}
        error={save.error}
        onConfirm={(typed) => {
          const kept = parsedRetention();
          if (kept) save.mutate({ kept, confirm: typed });
        }}
      />
    </Cell>
  );
}

/** Shown once, by the request that created the key. The loss warning is safety text: keep it complete. */
function NewKey({ value, children }: { value: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-2">
      <CopyableCode value={value} />
      <p className="notice notice--warning">
        <TriangleAlert />
        <strong>Copy key now</strong>
        <span>{children}</span>
      </p>
    </div>
  );
}

function SetupCell({ app, restic }: { app: T.App; restic: T.Restic }) {
  const track = useTrackOperation();
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
  return (
    <Cell title="Repository" icon={<Archive />} kind="tamago" className="cell--wide">
      <div className="grid gap-4">
        <KeyValues items={[["Location", <code>{restic.settings.repository}</code>]]} />
        {createdKey && (
          <NewKey value={createdKey}>
            It is not shown again. It decrypts every backup of this app. If every key is lost, the backups cannot be
            recovered.
          </NewKey>
        )}
        <div className="grid gap-3 md:grid-cols-2">
          <div className="grid content-start gap-2">
            <span className="text-xs font-semibold tracking-widest text-muted-foreground uppercase">New</span>
            <div>
              <Button disabled={init.isPending} onClick={() => init.mutate()}>
                <Archive /> {init.isPending ? "Creating" : "Create repository"}
              </Button>
            </div>
            {init.error && <p className="note note--bad">{messageOf(init.error)}</p>}
          </div>
          <form
            className="grid content-start gap-2"
            onSubmit={(event) => {
              event.preventDefault();
              connect.mutate(undefined, { onSuccess: () => setConnectKey("") });
            }}
          >
            <span className="text-xs font-semibold tracking-widest text-muted-foreground uppercase">Existing</span>
            <div className="flex gap-2">
              <Input
                type="password"
                value={connectKey}
                placeholder="Repository key"
                aria-label="Existing repository key"
                autoComplete="off"
                onChange={(event) => setConnectKey(event.target.value)}
              />
              <Button type="submit" variant="outline" disabled={!connectKey.trim() || connect.isPending}>
                <Link2 /> Connect
              </Button>
            </div>
            {connect.error && <p className="note note--bad">{messageOf(connect.error)}</p>}
          </form>
        </div>
      </div>
    </Cell>
  );
}

function RepositoryCell({ app, restic }: { app: T.App; restic: T.Restic }) {
  const backup = useOperationMutation(() => api.apps.restic.action(app.id, "backup"));
  const check = useOperationMutation(() => api.apps.restic.action(app.id, "check"));
  const refresh = useOperationMutation(() => api.apps.restic.action(app.id, "refresh"));
  const unlock = useOperationMutation(() => api.apps.restic.action(app.id, "unlock"));
  const last = restic.lastBackup;
  const failed = last !== undefined && !last.ok;
  const errors = [backup.error, check.error, refresh.error, unlock.error].filter(Boolean);
  return (
    <Cell
      title="App backup"
      icon={<Archive />}
      kind={failed ? "ume" : "gohan"}
      action={last && <StateBadge state={last.ok ? "succeeded" : last.partial ? "partial" : "failed"} />}
      foot={
        <>
          <Button size="sm" disabled={backup.isPending} onClick={() => backup.mutate(undefined)}>
            <Archive /> {backup.isPending ? "Backing up" : "Back up now"}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={check.isPending}
            title="Reads 5% of the data"
            onClick={() => check.mutate(undefined)}
          >
            <ShieldCheck /> Verify
          </Button>
          <Button size="sm" variant="ghost" disabled={refresh.isPending} onClick={() => refresh.mutate(undefined)}>
            <RefreshCw /> Refresh
          </Button>
          <Button
            size="sm"
            variant="ghost"
            disabled={unlock.isPending}
            title="restic unlock: removes locks older than 30 minutes or left by finished processes; running backups keep theirs"
            onClick={() => unlock.mutate(undefined)}
          >
            <Unlock /> Unlock
          </Button>
          {errors.map((error, index) => (
            <p key={index} className="note note--bad w-full">
              {messageOf(error)}
            </p>
          ))}
        </>
      }
    >
      <div className="grid gap-3">
        {failed && (
          <p className="note note--bad">
            ✕ {last.partial ? "PARTIAL" : "FAILED"} · {last.error}
          </p>
        )}
        <KeyValues
          items={[
            ["Repository", <code className="truncate">{restic.settings.repository}</code>],
            ["Last", last ? <span title={last.at}>{formatRelative(last.at)}</span> : "—"],
            ["Size", last?.ok ? `${last.filesTotal} files · +${formatBytes(last.bytesAdded)}` : "—"],
            ["Next", restic.nextRun ? formatRelative(restic.nextRun) : "— Off"],
            [
              "Verified",
              restic.lastCheck ? `${restic.lastCheck.ok ? "✓" : "✕"} ${formatRelative(restic.lastCheck.at)}` : "—",
            ],
            ["Pruned", restic.lastPruneAt ? formatRelative(restic.lastPruneAt) : "—"],
          ]}
        />
      </div>
    </Cell>
  );
}

function SnapshotsCell({ app, restic }: { app: T.App; restic: T.Restic }) {
  const [target, setTarget] = useState<T.ResticSnapshot | null>(null);
  return (
    <Cell title="Snapshots" icon={<History />} action={<span className="row__meta">{restic.snapshots.length}</span>}>
      <CloneFromBackupDialog app={app} snapshot={target} onClose={() => setTarget(null)} />
      {restic.snapshots.length === 0 ? (
        <p className="note">— No snapshots yet</p>
      ) : (
        <div className="rows rows--lined max-h-80 overflow-y-auto">
          {restic.snapshots.map((snapshot) => {
            const trigger = snapshot.tags
              .filter((tag) => tag.startsWith("trigger=") || tag.startsWith("slug="))
              .map((tag) => tag.split("=")[1])
              .join(" · ");
            return (
              <div key={snapshot.id} className="row">
                <span className="row__main">
                  <strong className="flex items-center gap-2">
                    {snapshot.shortId}
                    {snapshot.tags.includes("secrets=1") && (
                      <KeyRound className="size-3.5 text-muted-foreground" aria-label="Includes secrets" />
                    )}
                  </strong>
                  <small title={snapshot.time}>
                    {formatRelative(snapshot.time)}
                    {trigger && ` · ${trigger}`}
                  </small>
                </span>
                <Button size="xs" variant="outline" title="Restore into a new app" onClick={() => setTarget(snapshot)}>
                  <RotateCcw /> Restore
                </Button>
              </div>
            );
          })}
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
  const [removing, setRemoving] = useState<T.ResticKey | null>(null);
  return (
    <Cell
      title="Access keys"
      icon={<KeyRound />}
      className="cell--wide"
      foot={
        <form
          className="flex w-full gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            setAddOpen(true);
          }}
        >
          <Input
            value={label}
            placeholder="Label, e.g. staging"
            aria-label="Key label"
            spellCheck={false}
            onChange={(event) => setLabel(event.target.value)}
          />
          <Button type="submit" size="sm" variant="outline">
            <KeyRound /> Add key
          </Button>
        </form>
      }
    >
      <div className="grid gap-3">
        {newKey && <NewKey value={newKey}>It is not shown again.</NewKey>}
        <div className="rows rows--lined">
          {restic.keys.map((key) => (
            <div key={key.id} className="row">
              <span className="row__main">
                <strong>{key.id.slice(0, 8)}</strong>
                <small>
                  {key.userName} · {formatRelative(key.created)}
                </small>
              </span>
              {key.current ? (
                <span className="row__meta" title="The key Bento uses is never shown">
                  BENTO
                </span>
              ) : (
                <Button size="xs" variant="danger" disabled={remove.isPending} onClick={() => setRemoving(key)}>
                  <Trash2 /> Remove
                </Button>
              )}
            </div>
          ))}
        </div>
      </div>
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={`Remove key ${removing?.id.slice(0, 8) ?? ""}?`}
        description={`Whoever holds this key (${removing?.userName ?? "unknown"}) can no longer open the repository. This cannot be undone.`}
        confirmLabel="Remove key"
        destructive
        pending={remove.isPending}
        error={remove.error}
        onConfirm={() => {
          if (removing) remove.mutate(removing.id, { onSuccess: () => setRemoving(null) });
        }}
      />
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
