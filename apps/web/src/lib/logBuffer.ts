export type LogLine = { id: number; ts: string; line: string };

/**
 * Append an incoming line to the buffer, keeping at most `max` lines.
 * A resume asks the backend for lines since the last timestamp, which is
 * inclusive: lines at exactly that timestamp arrive again and are dropped
 * here when an identical line with that timestamp is already buffered.
 * Lines older than the newest buffered line are dropped too.
 */
export function appendLine(
  buffer: LogLine[],
  incoming: { ts: string; line: string },
  id: number,
  max: number,
): LogLine[] {
  const last = buffer.at(-1);
  if (last) {
    if (incoming.ts < last.ts) return buffer;
    if (incoming.ts === last.ts && buffer.some((l) => l.ts === incoming.ts && l.line === incoming.line)) return buffer;
  }
  const next = [...buffer, { id, ts: incoming.ts, line: incoming.line }];
  return next.length > max ? next.slice(-max) : next;
}
