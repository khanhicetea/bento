import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, ChevronRight, Cloud, KeyRound, Pencil, Plus, SquareTerminal } from "lucide-react";
import { Link } from "wouter";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { Cell, DomainError, DomainLoading, EmptyState, PageHeader, StateBadge } from "../../components/DomainState.tsx";
import { Mascot } from "../../components/Mascot.tsx";
import { keepEscapeInTerminal, TerminalView } from "../../components/Terminal.tsx";
import { formatBytes, formatCron, formatRelative } from "../../lib/format.ts";
import { useApplicationList, useOperationMutation } from "../applications/useApplications.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";

type Tab = "apps" | "runs" | "remotes";
const tabLabels: Record<Tab, string> = {
  apps: "App backups",
  runs: "Runs",
  remotes: "Remotes",
};

export function BackupsPage() {
  const [tab, setTab] = useState<Tab>("apps");
  return (
    <>
      <PageHeader title="Backups" />
      <BackupStatus />
      <div className="seg mb-5" role="tablist" aria-label="Backup sections">
        {(["apps", "runs", "remotes"] as const).map((value) => (
          <button key={value} type="button" role="tab" aria-selected={tab === value} onClick={() => setTab(value)}>
            {tabLabels[value]}
          </button>
        ))}
      </div>
      {tab === "apps" && <AppBackupsTab />}
      {tab === "runs" && <RunsTab />}
      {tab === "remotes" && <RemotesTab />}
    </>
  );
}

/** Status row: last backup, next run, the stack backup password, apps without an app backup. */
function BackupStatus() {
  const appBackups = useAppBackups();
  const apps = useApplicationList();
  const summaries = appBackups.data?.apps ?? [];
  const last = summaries
    .flatMap((summary) => (summary.lastBackup ? [summary.lastBackup] : []))
    .sort((a, b) => b.at.localeCompare(a.at))[0];
  const next = summaries
    .filter((summary) => summary.scheduleEnabled)
    .map((summary) => summary.nextRun)
    .filter(Boolean)
    .sort()[0];
  const covered = new Set(summaries.filter((summary) => summary.initialized).map((summary) => summary.appId));
  const uncovered = (apps.data?.apps ?? []).filter((app) => !covered.has(app.id));
  const pending = appBackups.isPending;
  const password = appBackups.data?.stackPassword;
  return (
    <section className="box box--4" aria-label="Backup status">
      <Cell kind={last && !last.ok ? "ume" : "gohan"}>
        <div className="flex items-start justify-between gap-3">
          <div className="metric">
            <strong className="text-[1.75rem]!">
              {pending ? "–" : last ? formatRelative(last.at).replace(/ ago$/, "") : "—"}
            </strong>
            <span>Last backup</span>
            {last && <StateBadge state={last.ok ? "succeeded" : "failed"} />}
          </div>
          <Mascot mood={pending ? "busy" : !last ? "idle" : last.ok ? "ok" : "alert"} size={72} />
        </div>
      </Cell>
      <Cell>
        <div className="metric">
          <strong className="text-[1.75rem]!">{next ? formatRelative(next).replace(/^in /, "") : "—"}</strong>
          <span>Next run</span>
        </div>
      </Cell>
      <Cell title="Backup password" icon={<KeyRound />}>
        <div className="grid gap-2">
          {pending ? (
            <span className="note">–</span>
          ) : password ? (
            <StateBadge state="succeeded" label="set" />
          ) : (
            <StateBadge state="absent" label="not yet" />
          )}
          <small className="note">
            Print on the host: <code>bento backup password</code>
          </small>
        </div>
      </Cell>
      <Cell kind={uncovered.length > 0 ? "tamago" : "gohan"}>
        <div className="metric">
          <strong className="text-[1.75rem]!">{apps.isPending ? "–" : uncovered.length}</strong>
          <span>No app backup</span>
          {uncovered.length > 0 && (
            <small className="truncate font-mono text-xs text-muted-foreground">
              {uncovered
                .slice(0, 3)
                .map((app) => app.slug)
                .join(" · ")}
            </small>
          )}
        </div>
      </Cell>
    </section>
  );
}

/** Polled overview of app (restic) backups, shared by the status row and the App backups and Runs tabs. */
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
          <EmptyState title="No app backups" body="Set up from an app's Backup tab." />
        </div>
      </div>
    );
  }
  return (
    <>
      <p className="note mb-3">
        Whole-app snapshots (home, databases, processes &amp; tasks) in each app's restic repository, encrypted with the
        stack backup password. Cron uses server time ({query.data.timeZone}).
      </p>
      <div className="box">
        <div className="tiles tiles--3">
          {apps.map((summary) => (
            <AppBackupTile key={summary.appId} summary={summary} />
          ))}
          <Link href="/apps" className="cell tile tile--add">
            <Plus aria-hidden="true" />
            Set up in an app
          </Link>
        </div>
      </div>
    </>
  );
}

function AppBackupTile({ summary }: { summary: T.AppBackupSummary }) {
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
          <dd>
            {summary.paths.includes(".") ? "Whole home" : summary.paths.join(", ")} + databases + processes &amp; tasks
          </dd>
        </div>
        <div>
          <dt>Verified</dt>
          <dd>
            {summary.lastCheck
              ? `${summary.lastCheck.ok ? "ok" : "failed"} ${formatRelative(summary.lastCheck.at)}`
              : "never"}
          </dd>
        </div>
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

function RunsTab() {
  const query = useAppBackups();
  if (query.isPending) return <DomainLoading label="runs" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  const runs = query.data.runs;
  return (
    <div className="box">
      <div className="cell">
        {runs.length === 0 ? (
          <EmptyState title="No runs yet" />
        ) : (
          <div className="rows rows--lined">
            {runs.map((entry) => (
              <AppRunRow key={`${entry.appId}-${entry.run.opId || entry.run.at}`} entry={entry} />
            ))}
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
            {(run.ok || run.partial) &&
              ` · ${run.filesTotal} files (${run.filesNew} new), ${formatBytes(run.bytesAdded)} added`}
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

/** The stack's rclone remotes, which app backup repositories live on. They
 * are configured in a throwaway rclone shell; credentials never leave it. */
function RemotesTab() {
  const queryClient = useQueryClient();
  const [shellOpen, setShellOpen] = useState(false);
  const [remote, setRemote] = useState("");
  const query = useQuery({ queryKey: keys.backups.rclone, queryFn: ({ signal }) => api.backups.rclone(signal) });
  const test = useOperationMutation((value: string) => api.backups.rcloneTest({ remote: value }));
  const closeShell = () => {
    setShellOpen(false);
    void queryClient.invalidateQueries({ queryKey: keys.backups.rclone });
  };
  const remotes = query.data?.remotes ?? [];
  return (
    <section className="box box--2" aria-label="rclone remotes">
      <Cell
        title="Remotes"
        icon={<Cloud />}
        kind={query.data?.encrypted || query.data?.error ? "ume" : "gohan"}
        action={<span className="row__meta">{remotes.length}</span>}
        foot={
          <Button size="sm" onClick={() => setShellOpen(true)}>
            <SquareTerminal /> Open shell
          </Button>
        }
      >
        <div className="grid gap-3">
          {query.isPending && <DomainLoading label="remotes" />}
          {query.error && <p className="note note--bad">{messageOf(query.error)}</p>}
          {query.data?.error && <p className="note note--bad">{query.data.error}</p>}
          {query.data?.encrypted && (
            <Alert variant="destructive">
              The rclone config is encrypted, so backups can’t unlock it. In the shell: <code>rclone config</code> →{" "}
              <code>s</code> → <code>u</code> (unencrypt).
            </Alert>
          )}
          {query.data && remotes.length === 0 && (
            <p className="note">
              — None yet · open the shell and run <code>rclone config</code>
            </p>
          )}
          {remotes.length > 0 && (
            <div className="rows rows--lined">
              {remotes.map((item) => (
                <button
                  key={item.name}
                  type="button"
                  className="row text-left"
                  title={`Test ${item.name}:`}
                  onClick={() => setRemote(`${item.name}:`)}
                >
                  <Cloud className="size-4 text-muted-foreground" aria-hidden="true" />
                  <span className="row__main">
                    <strong className="font-mono">{item.name}:</strong>
                  </span>
                  <span className="row__meta">{item.type || "?"}</span>
                </button>
              ))}
            </div>
          )}
        </div>
      </Cell>
      <Cell
        title="Test remote"
        icon={<Cloud />}
        onSubmit={(event) => {
          event.preventDefault();
          test.mutate(remote.trim());
        }}
        action={
          <Button type="submit" size="xs" disabled={!remote.trim() || test.isPending}>
            {test.isPending ? "Testing" : "Test"}
          </Button>
        }
      >
        <div className="grid gap-3">
          <Input
            className="font-mono"
            aria-label="rclone remote path"
            value={remote}
            placeholder="remote:bucket/bento"
            spellCheck={false}
            onChange={(event) => setRemote(event.target.value)}
          />
          {test.error && <p className="note note--bad">{messageOf(test.error)}</p>}
          <details className="note">
            <summary className="cursor-pointer select-none">How to add a remote</summary>
            <ul className="mt-2 grid list-disc gap-1 pl-5">
              <li>
                In the shell, run <code>rclone config</code> and check it with <code>rclone lsd name:</code>. Then use{" "}
                <code>name:bucket/bento/apps/slug</code> as an app&apos;s repository.
              </li>
              <li>
                Google Drive, OneDrive, Dropbox: answer <code>n</code> to “Use web browser”, run{" "}
                <code>rclone authorize</code> on a computer with a browser, and paste the token.
              </li>
              <li>Restic encrypts every snapshot. Don’t set an rclone config password.</li>
            </ul>
          </details>
        </div>
      </Cell>
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
    </section>
  );
}
