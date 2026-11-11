import { describe, expect, test } from "bun:test";
import { appendLine, type LogLine } from "./logBuffer.ts";

const t = (s: number) => `2026-01-01T00:00:0${s}.000000000Z`;

describe("appendLine", () => {
  test("appends in order and assigns the given ids", () => {
    let buf: LogLine[] = [];
    buf = appendLine(buf, { ts: t(1), line: "a" }, 1, 10);
    buf = appendLine(buf, { ts: t(2), line: "b" }, 2, 10);
    expect(buf).toEqual([
      { id: 1, ts: t(1), line: "a" },
      { id: 2, ts: t(2), line: "b" },
    ]);
  });

  test("drops a line older than the newest buffered line and returns the same array", () => {
    const buf = appendLine([], { ts: t(2), line: "b" }, 1, 10);
    expect(appendLine(buf, { ts: t(1), line: "a" }, 2, 10)).toBe(buf);
  });

  test("drops an exact duplicate but keeps a different line with the same ts", () => {
    const buf = appendLine([], { ts: t(1), line: "a" }, 1, 10);
    expect(appendLine(buf, { ts: t(1), line: "a" }, 2, 10)).toBe(buf);
    const next = appendLine(buf, { ts: t(1), line: "b" }, 2, 10);
    expect(next.map((l) => l.line)).toEqual(["a", "b"]);
  });

  test("trims to max keeping the newest, with original ids", () => {
    let buf: LogLine[] = [];
    for (let i = 1; i <= 5; i++) buf = appendLine(buf, { ts: t(i), line: `l${i}` }, i, 3);
    expect(buf.map((l) => l.id)).toEqual([3, 4, 5]);
    expect(buf.map((l) => l.line)).toEqual(["l3", "l4", "l5"]);
  });
});
