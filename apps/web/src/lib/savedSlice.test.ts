import { describe, expect, test } from "bun:test";
import { pick, sliceKey } from "./savedSlice.ts";

describe("sliceKey", () => {
  test("ignores object key order, including nested objects", () => {
    expect(sliceKey({ a: 1, b: { x: 1, y: 2 } })).toBe(sliceKey({ b: { y: 2, x: 1 }, a: 1 }));
  });

  test("differs for different values", () => {
    expect(sliceKey({ a: 1 })).not.toBe(sliceKey({ a: 2 }));
    expect(sliceKey({ a: 1 })).not.toBe(sliceKey({ b: 1 }));
  });

  test("keeps array order significant", () => {
    expect(sliceKey([1, 2])).not.toBe(sliceKey([2, 1]));
    expect(sliceKey({ argv: ["a", "b"] })).not.toBe(sliceKey({ argv: ["b", "a"] }));
  });
});

describe("pick", () => {
  test("returns only the listed keys", () => {
    expect(pick({ a: 1, b: 2, c: 3 }, ["a", "c"])).toEqual({ a: 1, c: 3 });
  });
});
