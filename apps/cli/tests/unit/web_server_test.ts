import { describe, expect, test } from "bun:test";
import {
  addApplicationDatabaseInputSchema,
  applicationListSchema,
  jobsOverviewSchema,
  removeApplicationInputSchema,
  saveApplicationInputSchema,
  setApplicationEnabledInputSchema,
  webContract,
} from "@bento/shared";

describe("web API contract", () => {
  test("is composed from explicit domain routers", () => {
    expect(Object.keys(webContract)).toEqual([
      "system",
      "applications",
      "data",
      "routing",
      "jobs",
      "operations",
    ]);
    expect("execute" in webContract).toBe(false);
  });

  test("keeps configured command arguments out of the jobs response", () => {
    const overview = {
      initialized: true,
      stackRoot: "/srv/bento",
      cronJobs: [
        {
          name: "tick",
          app: "demo",
          schedule: "* * * * *",
          timezone: "UTC",
          command: "php (+2 args)",
          commandMode: "argv" as const,
          output: "log" as const,
          enabled: true,
        },
      ],
      workers: [],
      deploys: [],
    };
    expect(jobsOverviewSchema.parse(overview)).toEqual(overview);
    expect(() =>
      jobsOverviewSchema.parse({
        ...overview,
        cronJobs: [{ ...overview.cronJobs[0], command: ["php", "secret"] }],
      }),
    ).toThrow();
  });

  test("validates the applications feature boundary", () => {
    expect(setApplicationEnabledInputSchema.parse({ slug: "demo", enabled: false })).toEqual({
      slug: "demo",
      enabled: false,
    });
    expect(() => setApplicationEnabledInputSchema.parse({ slug: "", enabled: true })).toThrow();
    expect(
      saveApplicationInputSchema.parse({
        slug: "demo",
        domain: "demo.example.test",
        aliases: [],
        documentRoot: "public",
        entrypointMode: "front-controller",
        phpVersion: "8.4",
        fpmProfile: "small",
        tls: "shared",
        accessLog: false,
        databaseEngine: "mysql",
        databaseService: "mysql84",
        createDatabase: true,
      }),
    ).toBeTruthy();
    expect(() =>
      saveApplicationInputSchema.parse({
        slug: "demo",
        domain: "demo.example.test",
        aliases: [],
        documentRoot: "public",
        entrypointMode: "front-controller",
        phpVersion: "8.4",
        fpmProfile: "small",
        tls: "external",
        accessLog: false,
        databaseEngine: "mysql",
        databaseService: "mysql84",
        createDatabase: false,
      }),
    ).toThrow();
    expect(
      addApplicationDatabaseInputSchema.parse({
        slug: "demo",
        engine: "postgres",
        service: "postgres17",
        databaseName: "demo_reporting",
      }),
    ).toBeTruthy();
    expect(() =>
      addApplicationDatabaseInputSchema.parse({ slug: "demo", engine: "mysql" }),
    ).toThrow();
    expect(
      removeApplicationInputSchema.parse({ slug: "demo", confirmation: "delete demo" }),
    ).toBeTruthy();
    expect(
      applicationListSchema.parse({
        initialized: false,
        stateExists: false,
        stackRoot: "/srv/bento",
        applications: [],
        phpVersions: [],
        fpmProfiles: [],
        databaseServices: [],
      }),
    ).toBeTruthy();
  });
});
