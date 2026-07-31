import type { AppDatabaseBinding, DesiredState, SqliteVacuumSchedule } from "../domain/state.ts";
import type { Random } from "../platform/interfaces.ts";

/** The local-time maintenance window is midnight through 04:59. */
export const SQLITE_VACUUM_WINDOW_MINUTES = 5 * 60;
export const SQLITE_VACUUM_SLOT_COUNT = 7 * SQLITE_VACUUM_WINDOW_MINUTES;

type LocalSqliteBinding = Extract<AppDatabaseBinding, { engine: "sqlite" }>;

export function sqliteVacuumScheduleKey(appSlug: string, fileId: string): string {
  return `${appSlug}:${fileId}`;
}

export function formatSqliteVacuumSchedule(schedule: SqliteVacuumSchedule): string {
  return `${schedule.minute} ${schedule.hour} * * ${schedule.dayOfWeek}`;
}

export function sqliteVacuumScheduleSlot(schedule: SqliteVacuumSchedule): number {
  return schedule.dayOfWeek * SQLITE_VACUUM_WINDOW_MINUTES +
    schedule.hour * 60 + schedule.minute;
}

/**
 * Resolve schedules for every local SQLite binding. New bindings carry their
 * selected slot in state; older state files get a stable fallback so rendering
 * remains deterministic while their schedules move away from the old fixed slot.
 */
export function resolveSqliteVacuumSchedules(
  state: DesiredState,
): Map<string, SqliteVacuumSchedule> {
  const entries = Object.values(state.apps).flatMap((app) =>
    app.databases
      .filter((database): database is LocalSqliteBinding => database.engine === "sqlite")
      .map((binding) => ({
        key: sqliteVacuumScheduleKey(String(app.slug), binding.file.id),
        binding,
      }))
  ).sort((a, b) => a.key.localeCompare(b.key));

  const schedules = new Map<string, SqliteVacuumSchedule>();
  const occupied = new Set<number>();

  // Preserve explicitly selected slots first. They are allocated without
  // collisions when a new binding is created and must remain stable thereafter.
  for (const entry of entries) {
    const schedule = entry.binding.vacuumSchedule;
    if (!schedule) continue;
    schedules.set(entry.key, schedule);
    occupied.add(sqliteVacuumScheduleSlot(schedule));
  }

  for (const entry of entries) {
    if (schedules.has(entry.key)) continue;
    schedules.set(
      entry.key,
      stableSqliteVacuumSchedule(entry.key, occupied),
    );
  }

  return schedules;
}

/** Select a random unused weekly slot for a newly created local SQLite file. */
export function randomSqliteVacuumSchedule(
  random: Random,
  occupied: Set<number>,
): SqliteVacuumSchedule {
  const start = random.bytes(4).reduce(
    (value, byte) => value * 256 + byte,
    0,
  ) % SQLITE_VACUUM_SLOT_COUNT;
  const slot = firstAvailableSlot(start, occupied);
  occupied.add(slot);
  return scheduleFromSlot(slot);
}

function stableSqliteVacuumSchedule(
  identity: string,
  occupied: Set<number>,
): SqliteVacuumSchedule {
  let hash = 2166136261;
  for (const char of identity) {
    hash = Math.imul(hash ^ char.charCodeAt(0), 16777619);
  }
  const start = (hash >>> 0) % SQLITE_VACUUM_SLOT_COUNT;
  const slot = firstAvailableSlot(start, occupied);
  occupied.add(slot);
  return scheduleFromSlot(slot);
}

function firstAvailableSlot(start: number, occupied: Set<number>): number {
  for (let offset = 0; offset < SQLITE_VACUUM_SLOT_COUNT; offset++) {
    const slot = (start + offset) % SQLITE_VACUUM_SLOT_COUNT;
    if (!occupied.has(slot)) return slot;
  }
  // A stack with more than 2,100 local SQLite files cannot have a unique slot;
  // keep generation total and deterministic in that pathological case.
  return start;
}

function scheduleFromSlot(slot: number): SqliteVacuumSchedule {
  const dayOfWeek = Math.floor(slot / SQLITE_VACUUM_WINDOW_MINUTES);
  const minuteOfDay = slot % SQLITE_VACUUM_WINDOW_MINUTES;
  return {
    dayOfWeek,
    hour: Math.floor(minuteOfDay / 60),
    minute: minuteOfDay % 60,
  };
}
