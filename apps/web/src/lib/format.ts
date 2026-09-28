import type { T } from "../api/client.ts";

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value >= 10 || unit === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`;
}

export function formatRelative(value?: string): string {
  if (!value) return "—";
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return value;
  const seconds = Math.round((timestamp - Date.now()) / 1000);
  const ranges: Array<[number, Intl.RelativeTimeFormatUnit]> = [
    [60, "second"],
    [60, "minute"],
    [24, "hour"],
    [7, "day"],
    [4.345, "week"],
    [12, "month"],
    [Number.POSITIVE_INFINITY, "year"],
  ];
  let amount = seconds;
  for (const [limit, unit] of ranges) {
    if (Math.abs(amount) < limit)
      return new Intl.RelativeTimeFormat(undefined, { numeric: "auto" }).format(amount, unit);
    amount = Math.round(amount / limit);
  }
  return value;
}

export function formatDuration(start?: string, finish?: string): string {
  if (!start) return "—";
  const ms = (finish ? Date.parse(finish) : Date.now()) - Date.parse(start);
  if (!Number.isFinite(ms) || ms < 0) return "—";
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)} s`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.floor((ms % 60_000) / 1000);
  return `${minutes}m ${seconds}s`;
}

export function formatCron(cron: string): string {
  const presets: Record<string, string> = {
    "0 * * * *": "Every hour",
    "0 0 * * *": "Every day at midnight",
    "0 2 * * *": "Every day at 02:00",
    "0 0 * * 0": "Every Sunday at midnight",
  };
  return presets[cron.trim()] ?? `Cron: ${cron || "not set"}`;
}

export function describeOp(op: Pick<T.Operation, "kind" | "targetId">, targetLabel?: string): string {
  const verbs: Record<string, string> = {
    "app.start": "Starting",
    "app.stop": "Stopping",
    "app.restart": "Restarting",
    "app.publish": "Publishing",
    "app.unpublish": "Unpublishing",
    "app.create": "Creating application",
    "app.update": "Updating",
    "app.remove": "Removing",
    "backup.run": "Running backup",
    "backup.restore": "Restoring backup",
    "backup.delete": "Deleting backup",
    "backup.rclone-test": "Testing rclone remote",
    "edge.update": "Updating edge",
    "proxy.upsert": "Saving proxy",
    "proxy.remove": "Removing proxy",
  };
  const verb = verbs[op.kind] ?? op.kind.replaceAll(".", " ");
  return targetLabel || op.targetId ? `${verb} ${targetLabel ?? op.targetId}` : verb;
}
