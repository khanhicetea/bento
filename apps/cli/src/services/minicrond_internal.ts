import { isPhpApp, type AppState, type DesiredState } from "#/domain/state.ts";
import { validationError } from "#/domain/errors.ts";
import {
  formatSqliteVacuumSchedule,
  resolveSqliteVacuumSchedules,
  sqliteVacuumScheduleKey,
} from "#/services/sqlite_schedule.ts";

/** Only Bento-owned maintenance tasks belong in the bootstrap config. Never copy user jobs. */
export const INTERNAL_JOB_PREFIX = "bento-internal-";

const toml = (value: string): string => JSON.stringify(value);
const argv = (values: string[]): string => `[${values.map(toml).join(", ")}]`;

export function minicrondBootstrapConfig(internalJobs = ""): string {
  return `[server]
tcp_enabled = false
unix_socket = true

[scheduler]
timezone = "UTC"
${internalJobs ? `\n${internalJobs}` : ""}`;
}

export function appInternalJobs(state: DesiredState, app: AppState): string {
  if (!isPhpApp(app)) return "";
  const lines: string[] = [];
  if (app.deploy.enabled) {
    lines.push(
      `[[job]]\nname = "${INTERNAL_JOB_PREFIX}deploy-drain"\nschedule = "* * * * *"\nargv = ${argv([
        "/opt/bento/helpers/deploy-drain.sh",
        String(app.slug),
        `/run/php-fpm/${app.phpService}/${app.slug}.sock`,
      ])}\nworking_dir = ${toml(app.home)}\n`,
    );
  }
  const schedules = resolveSqliteVacuumSchedules(state);
  for (const binding of [...app.databases].sort((a, b) =>
    (a.engine === "sqlite" ? a.file.id : "").localeCompare(b.engine === "sqlite" ? b.file.id : ""),
  )) {
    if (binding.engine !== "sqlite") continue;
    const schedule = schedules.get(sqliteVacuumScheduleKey(String(app.slug), binding.file.id));
    if (!schedule) throw validationError("missing SQLite VACUUM schedule");
    lines.push(
      `[[job]]\nname = ${toml(`${INTERNAL_JOB_PREFIX}vacuum-${binding.file.id}`)}\nschedule = ${toml(formatSqliteVacuumSchedule(schedule))}\nargv = ${argv(
        ["/usr/bin/sqlite3", `/sqlite/${binding.file.id}/${app.slug}.db`, "PRAGMA busy_timeout=30000; VACUUM;"],
      )}\nworking_dir = ${toml(app.home)}\n`,
    );
  }
  return lines.join("\n");
}

export function rootInternalJobs(apps: AppState[]): string {
  return [...apps]
    .filter(isPhpApp)
    .sort((a, b) => a.slug.localeCompare(b.slug))
    .map(
      (app) =>
        `[[job]]\nname = ${toml(`${INTERNAL_JOB_PREFIX}logrotate-${app.slug}`)}\nschedule = "0 * * * *"\nargv = ${argv([
          "/usr/sbin/logrotate",
          "--state",
          `/var/lib/bento/minicron/logrotate-${app.slug}.status`,
          `/etc/bento/minicrond/logrotate/${app.slug}.conf`,
        ])}\n`,
    )
    .join("\n");
}
