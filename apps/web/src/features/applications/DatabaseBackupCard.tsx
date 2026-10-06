import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Archive, CalendarPlus } from "lucide-react";
import { Link } from "wouter";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { Cell, DomainError, DomainLoading } from "../../components/DomainState.tsx";
import { formatBytes, formatCron, formatRelative } from "../../lib/format.ts";
import { newSchedule, ScheduleDialog } from "../backups/BackupsPage.tsx";
import { useOperationMutation } from "./useApplications.ts";
import { Button } from "@/components/ui/button";

/**
 * Database backup for one app: the stack schedules that cover it, the newest dump of each database, and manual runs.
 * It reads the same data as Backups → Schedules; app backup (restic) is a separate method.
 */
export function DatabaseBackupCard({ app }: { app: T.App }) {
  const [adding, setAdding] = useState(false);
  const schedules = useQuery({
    queryKey: keys.backups.schedules,
    queryFn: ({ signal }) => api.backups.schedules(signal),
    refetchInterval: 30_000,
  });
  const artifacts = useQuery({
    queryKey: keys.backups.artifacts,
    queryFn: ({ signal }) => api.backups.artifacts(signal),
    refetchInterval: 30_000,
  });
  const backupNow = useOperationMutation(() => api.backups.run({ scope: "app", appId: app.id, compression: "zstd" }));
  if (schedules.isPending || artifacts.isPending) return <DomainLoading label="database backups" />;
  if (schedules.error)
    return <DomainError message={messageOf(schedules.error)} onRetry={() => void schedules.refetch()} />;
  if (artifacts.error)
    return <DomainError message={messageOf(artifacts.error)} onRetry={() => void artifacts.refetch()} />;

  const covering = schedules.data.schedules.filter((schedule) => schedule.scope === "all" || schedule.appId === app.id);
  const scopeLabel = (schedule: T.BackupSchedule) =>
    schedule.scope === "all"
      ? "All databases"
      : schedule.scope === "app"
        ? "This app"
        : `Databases: ${schedule.databases.join(", ")}`;
  const latest = new Map<string, T.BackupArtifact>();
  for (const artifact of artifacts.data.artifacts) {
    if (artifact.appSlug !== app.slug) continue;
    const current = latest.get(artifact.database);
    if (!current || artifact.createdAt > current.createdAt) latest.set(artifact.database, artifact);
  }
  const databases = [...latest.values()].sort((a, b) => a.database.localeCompare(b.database));
  return (
    <Cell title="Database backup" className="cell--wide">
      <p className="note mb-3">
        Compressed dumps of this app's databases. Restore one database in place from{" "}
        <Link className="underline" href="/backups">
          Backups
        </Link>
        .
      </p>
      <h3 className="mb-1 text-sm font-medium">Schedules covering this app</h3>
      {covering.length === 0 ? (
        <p className="note mb-3">No schedule covers this app.</p>
      ) : (
        <ul className="mb-3 grid gap-1 text-sm">
          {covering.map((schedule) => (
            <li key={schedule.id} className={schedule.enabled ? "" : "opacity-60"}>
              <strong>{schedule.name}</strong> · {scopeLabel(schedule)} · {formatCron(schedule.cron)}
              {!schedule.enabled && " · disabled"}
              {schedule.lastRun && ` · last ${formatRelative(schedule.lastRun)}`}
              {schedule.lastState && schedule.lastState !== "succeeded" && (
                <span className="note--bad"> ({schedule.lastState})</span>
              )}
            </li>
          ))}
        </ul>
      )}
      <h3 className="mb-1 text-sm font-medium">Last backup per database</h3>
      {databases.length === 0 ? (
        <p className="note mb-3">No database backups yet.</p>
      ) : (
        <ul className="mb-3 grid gap-1 text-sm">
          {databases.map((artifact) => (
            <li key={artifact.database}>
              <code>{artifact.database}</code> ·{" "}
              <span title={artifact.createdAt}>{formatRelative(artifact.createdAt)}</span> ·{" "}
              {formatBytes(artifact.sizeBytes)}
            </li>
          ))}
        </ul>
      )}
      <div className="flex flex-wrap gap-2">
        <Button disabled={backupNow.isPending} onClick={() => backupNow.mutate(undefined)}>
          <Archive /> Back up now
        </Button>
        <Button variant="outline" onClick={() => setAdding(true)}>
          <CalendarPlus /> Add schedule for this app
        </Button>
      </div>
      {backupNow.error && <p className="note note--bad mt-2">{messageOf(backupNow.error)}</p>}
      {adding && (
        <ScheduleDialog
          initial={{ ...newSchedule, name: `${app.slug} databases`, scope: "app", appId: app.id }}
          onClose={() => setAdding(false)}
        />
      )}
    </Cell>
  );
}
