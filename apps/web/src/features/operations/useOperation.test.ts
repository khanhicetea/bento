import { describe, expect, test } from "bun:test";
import type { QueryClient } from "@tanstack/react-query";
import type { T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { invalidateFor, justFinished } from "./useOperation.ts";

const op = (state: string, targetKind = "app"): T.Operation => ({ state, targetKind, kind: "x.y" }) as T.Operation;

describe("justFinished", () => {
  test("is false while the operation is still active", () => {
    expect(justFinished(undefined, op("running"))).toBe(false);
  });
  test("is true the first time a final state is seen", () => {
    expect(justFinished(undefined, op("succeeded"))).toBe(true);
    expect(justFinished(op("running"), op("failed"))).toBe(true);
  });
  test("is false once the final state was already cached", () => {
    expect(justFinished(op("succeeded"), op("succeeded"))).toBe(false);
  });
});

describe("invalidateFor", () => {
  test("refreshes a service's domains but never the observing detail query", () => {
    const calls: Array<{ queryKey?: readonly unknown[] }> = [];
    const client = { invalidateQueries: (arg: { queryKey?: readonly unknown[] }) => calls.push(arg) };
    invalidateFor(client as unknown as QueryClient, op("succeeded", "service"));
    const recorded = calls.map((call) => JSON.stringify(call.queryKey));
    for (const key of [keys.services, keys.operations.lists, keys.system, keys.reconcile]) {
      expect(recorded).toContain(JSON.stringify(key));
    }
    expect(recorded).not.toContain(JSON.stringify(keys.operations.all));
  });
});
