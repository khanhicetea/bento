import { describe, expect, test } from "bun:test";
import {
  applicationListSchema,
  setApplicationEnabledInputSchema,
  webContract,
} from "@bento/shared";

describe("web API contract", () => {
  test("is composed from explicit domain routers", () => {
    expect(Object.keys(webContract)).toEqual(["system", "applications"]);
    expect("execute" in webContract).toBe(false);
  });

  test("validates the applications feature boundary", () => {
    expect(setApplicationEnabledInputSchema.parse({ slug: "demo", enabled: false })).toEqual({
      slug: "demo",
      enabled: false,
    });
    expect(() => setApplicationEnabledInputSchema.parse({ slug: "", enabled: true })).toThrow();
    expect(
      applicationListSchema.parse({
        initialized: false,
        stateExists: false,
        stackRoot: "/srv/bento",
        applications: [],
        phpVersions: [],
      }),
    ).toBeTruthy();
  });
});
