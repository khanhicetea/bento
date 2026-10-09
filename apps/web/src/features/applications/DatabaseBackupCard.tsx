import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Archive, CalendarPlus, Clock, Database } from "lucide-react";
import { Link } from "wouter";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { Cell, DomainError, DomainLoading, StateBadge } from "../../components/DomainState.tsx";
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
    schedule.scope === "all" ? "All DBs" : schedule.scope === "app" ? "This app" : schedule.databases.join(", ");
  const latest = new Map<string, T.BackupArtifact>();
  for (const artifact of artifacts.data.artifacts) {
    if (artifact.appSlug !== app.slug) continue;
    const current = latest.get(artifact.database);
    if (!current || artifact.createdAt > current.createdAt) latest.set(artifact.database, artifact);
  }
  const databases = [...latest.values()].sort((a, b) => a.database.localeCompare(b.database));
  return (
    <section className="box box--2" aria-label="Database backup">
      <Cell
        title="DB schedules"
        icon={<Clock />}
        action={<Link href="/backups">Backups</Link>}
        foot={
          <>
            <Button size="sm" disabled={backupNow.isPending} onClick={() => backupNow.mutate(undefined)}>
              <Archive /> {backupNow.isPending ? "Backing up" : "Back up now"}
            </Button>
            <Button size="sm" variant="outline" onClick={() => setAdding(true)}>
              <CalendarPlus /> Add schedule
            </Button>
            {backupNow.error && <p className="note note--bad w-full">{messageOf(backupNow.error)}</p>}
          </>
        }
      >
        {covering.length === 0 ? (
          <p className="note">— No schedule</p>
        ) : (
          <div className="rows rows--lined">
            {covering.map((schedule) => (
              <div key={schedule.id} className={schedule.enabled ? "row" : "row row--muted"}>
                <span className="row__main">
                  <strong>{schedule.name}</strong>
                  <small>
                    {scopeLabel(schedule)} · {formatCron(schedule.cron)}
                  </small>
                </span>
                <span className="row__meta">{schedule.lastRun ? formatRelative(schedule.lastRun) : "—"}</span>
                {!schedule.enabled ? (
                  <StateBadge state="absent" />
                ) : (
                  schedule.lastState && <StateBadge state={schedule.lastState} iconOnly />
                )}
              </div>
            ))}
          </div>
        )}
      </Cell>
      <Cell title="Last dumps" icon={<Database />}>
        {databases.length === 0 ? (
          <p className="note">— No dumps yet</p>
        ) : (
          <div className="rows rows--lined">
            {databases.map((artifact) => (
              <div key={artifact.database} className="row">
                <span className="row__main">
                  <strong>{artifact.database}</strong>
                  <small title={artifact.createdAt}>{formatRelative(artifact.createdAt)}</small>
                </span>
                <span className="row__meta">{formatBytes(artifact.sizeBytes)}</span>
              </div>
            ))}
          </div>
        )}
      </Cell>
      {adding && (
        <ScheduleDialog
          initial={{ ...newSchedule, name: `${app.slug} databases`, scope: "app", appId: app.id }}
          onClose={() => setAdding(false)}
        />
      )}
    </section>
  );
}
