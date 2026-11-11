import { afterEach, describe, expect, setSystemTime, test } from "bun:test";
import { describeOp, formatBytes, formatCron, formatDuration, formatRelative } from "./format.ts";

afterEach(() => {
  setSystemTime();
});

describe("formatBytes", () => {
  test("uses whole bytes below 1 KiB", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1023)).toBe("1023 B");
  });

  test("keeps one decimal below 10 and none from 10 up", () => {
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatBytes(10 * 1024)).toBe("10 KiB");
    expect(formatBytes(5 * 1024 * 1024 * 1024)).toBe("5.0 GiB");
  });

  test("caps at TiB", () => {
    expect(formatBytes(2048 * 1024 ** 4)).toBe("2048 TiB");
  });

  test("renders invalid input as a dash", () => {
    expect(formatBytes(-1)).toBe("—");
    expect(formatBytes(Number.NaN)).toBe("—");
    expect(formatBytes(Number.POSITIVE_INFINITY)).toBe("—");
  });
});

describe("formatRelative", () => {
  const now = "2026-01-01T12:00:00Z";

  test("renders a missing or invalid value", () => {
    expect(formatRelative()).toBe("—");
    expect(formatRelative("not a date")).toBe("not a date");
  });

  test("says now within ten seconds", () => {
    setSystemTime(new Date(now));
    expect(formatRelative("2026-01-01T12:00:05Z")).toBe("now");
    expect(formatRelative("2026-01-01T11:59:55Z")).toBe("now");
  });

  test("renders the past with ago and singular or plural units", () => {
    setSystemTime(new Date(now));
    expect(formatRelative("2026-01-01T11:59:30Z")).toBe("30 secs ago");
    expect(formatRelative("2026-01-01T11:59:00Z")).toBe("1 min ago");
    expect(formatRelative("2026-01-01T11:55:00Z")).toBe("5 mins ago");
    expect(formatRelative("2026-01-01T09:00:00Z")).toBe("3 hrs ago");
    expect(formatRelative("2025-12-30T12:00:00Z")).toBe("2 days ago");
  });

  test("renders the future with in", () => {
    setSystemTime(new Date(now));
    expect(formatRelative("2026-01-01T14:00:00Z")).toBe("in 2 hrs");
  });
});

describe("formatDuration", () => {
  test("renders a missing start as a dash", () => {
    expect(formatDuration()).toBe("—");
  });

  test("uses ms, seconds and minutes", () => {
    expect(formatDuration("2026-01-01T00:00:00.000Z", "2026-01-01T00:00:00.250Z")).toBe("250 ms");
    expect(formatDuration("2026-01-01T00:00:00Z", "2026-01-01T00:00:02.500Z")).toBe("2.5 s");
    expect(formatDuration("2026-01-01T00:00:00Z", "2026-01-01T00:00:42Z")).toBe("42 s");
    expect(formatDuration("2026-01-01T00:00:00Z", "2026-01-01T00:03:05Z")).toBe("3m 5s");
  });

  test("measures an unfinished run against now", () => {
    setSystemTime(new Date("2026-01-01T00:00:30Z"));
    expect(formatDuration("2026-01-01T00:00:00Z")).toBe("30 s");
  });

  test("renders a finish before the start as a dash", () => {
    expect(formatDuration("2026-01-01T00:00:10Z", "2026-01-01T00:00:00Z")).toBe("—");
    expect(formatDuration("garbage", "2026-01-01T00:00:00Z")).toBe("—");
  });
});

describe("formatCron", () => {
  test("names the presets, trimming whitespace", () => {
    expect(formatCron("0 * * * *")).toBe("Every hour");
    expect(formatCron(" 0 0 * * * ")).toBe("Every day at midnight");
  });

  test("falls back to the raw expression or not set", () => {
    expect(formatCron("*/5 * * * *")).toBe("Cron: */5 * * * *");
    expect(formatCron("")).toBe("Cron: not set");
  });
});

describe("describeOp", () => {
  test("uses the verb of a known kind with the target label", () => {
    expect(describeOp({ kind: "app.start", targetId: "app_1" }, "blog")).toBe("Starting blog");
  });

  test("labels operation kinds that exist in the backend", () => {
    expect(describeOp({ kind: "app.deploy", targetId: "app_1" }, "shop")).toBe("Deploying shop");
  });

  test("falls back to the target id when there is no label", () => {
    expect(describeOp({ kind: "app.start", targetId: "app_1" })).toBe("Starting app_1");
  });

  test("replaces dots with spaces for an unknown kind", () => {
    expect(describeOp({ kind: "image.sweep", targetId: "" })).toBe("image sweep");
    expect(describeOp({ kind: "image.sweep", targetId: "k" }, "runtime")).toBe("image sweep runtime");
  });

  test("renders only the verb without a target", () => {
    expect(describeOp({ kind: "edge.update", targetId: "" })).toBe("Updating edge");
  });
});
