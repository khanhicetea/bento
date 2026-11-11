import { describe, expect, test } from "bun:test";
import { ApiError } from "../api/client.ts";
import { shouldRetryQuery } from "./retry.ts";

const err = (status: number) => new ApiError(status, null, "x");

describe("shouldRetryQuery", () => {
  test("an unreachable backend (status 0) is retried", () => {
    expect(shouldRetryQuery(0, err(0))).toBe(true);
    expect(shouldRetryQuery(1, err(0))).toBe(true);
    expect(shouldRetryQuery(2, err(0))).toBe(false);
  });

  test("client errors are final", () => {
    for (const status of [401, 404, 409]) expect(shouldRetryQuery(0, err(status))).toBe(false);
  });

  test("server errors are retried twice", () => {
    for (const status of [500, 503]) {
      expect(shouldRetryQuery(0, err(status))).toBe(true);
      expect(shouldRetryQuery(1, err(status))).toBe(true);
      expect(shouldRetryQuery(2, err(status))).toBe(false);
    }
  });

  test("non-API errors are retried twice", () => {
    expect(shouldRetryQuery(0, new Error("x"))).toBe(true);
    expect(shouldRetryQuery(2, new Error("x"))).toBe(false);
  });
});
