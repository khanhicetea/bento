import { describe, expect, test } from "bun:test";
import {
  addApplicationDatabaseInputSchema,
  addCronJobInputSchema,
  addWorkerInputSchema,
  applicationListSchema,
  jobsOverviewSchema,
  removeApplicationInputSchema,
  saveApplicationInputSchema,
  setApplicationEnabledInputSchema,
  setApplicationRunningInputSchema,
  webContract,
} from "@bento/shared";
import { commandDisplay } from "../../src/server/domains/jobs/router.ts";
import { matchesBasicAuthorization } from "../../src/server/server.ts";

describe("web server authentication", () => {
  test("accepts only the exact Basic authorization value", () => {
    const expected = `Basic ${Buffer.from("operator:secret").toString("base64")}`;

    expect(matchesBasicAuthorization(expected, expected)).toBe(true);
    expect(matchesBasicAuthorization(null, expected)).toBe(false);
    expect(
      matchesBasicAuthorization(
        `Basic ${Buffer.from("operator:wrong").toString("base64")}`,
        expected,
      ),
    ).toBe(false);
    expect(
      matchesBasicAuthorization(
        `basic ${Buffer.from("operator:secret").toString("base64")}`,
        expected,
      ),
    ).toBe(false);
  });
});

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
    expect("restartWorker" in webContract.jobs).toBe(true);
  });

  test("returns complete job commands as redacted display strings", () => {
    const overview = {
      initialized: true,
      stackRoot: "/srv/bento",
      cronJobs: [
        {
          name: "tick",
          app: "demo",
          schedule: "* * * * *",
          timezone: "UTC",
          command: "php artisan schedule:run",
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
    expect(commandDisplay(["php", "artisan", "queue:work"], "argv")).toBe("php artisan queue:work");
    expect(
      commandDisplay(["curl", "--token", "do-not-expose", "https://example.test"], "argv"),
    ).toBe("curl --token *** https://example.test");
    expect(commandDisplay(["curl --api-key='do-not-expose' example.test"], "shell")).toBe(
      "curl --api-key=*** example.test",
    );
  });

  test("validates app-scoped cron and worker mutations", () => {
    expect(
      addCronJobInputSchema.parse({
        app: "demo",
        name: "tick",
        schedule: "*/5 * * * *",
        timezone: "UTC",
        command: ["php", "artisan", "schedule:run"],
        commandMode: "argv",
        output: "log",
        timeoutSec: 60,
      }),
    ).toBeTruthy();
    expect(() =>
      addCronJobInputSchema.parse({
        app: "demo",
        name: "tick",
        schedule: "* * * * *",
        timezone: "UTC",
        command: ["echo one", "echo two"],
        commandMode: "shell",
        output: "log",
      }),
    ).toThrow();
    expect(
      addWorkerInputSchema.parse({
        app: "demo",
        name: "queue",
        command: ["php", "artisan", "queue:work"],
        autorestart: true,
        stopsignal: "TERM",
        stopwaitsecs: 10,
      }),
    ).toBeTruthy();
    expect(() =>
      addWorkerInputSchema.parse({
        app: "demo",
        name: "queue",
        command: [],
        autorestart: true,
        stopsignal: "TERM",
        stopwaitsecs: 0,
      }),
    ).toThrow();
  });

  test("validates the applications feature boundary", () => {
    expect(setApplicationEnabledInputSchema.parse({ slug: "demo", enabled: false })).toEqual({
      slug: "demo",
      enabled: false,
    });
    expect(() => setApplicationEnabledInputSchema.parse({ slug: "", enabled: true })).toThrow();
    expect(setApplicationRunningInputSchema.parse({ slug: "api", action: "start" })).toEqual({
      slug: "api",
      action: "start",
    });
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
    expect(
      saveApplicationInputSchema.parse({
        slug: "api",
        kind: "process",
        domain: "api.example.test",
        aliases: [],
        processLanguage: "node",
        processVersion: "24",
        processCommand: ["node", "server.js"],
        processPort: 8080,
        tls: "shared",
        accessLog: false,
        databaseEngine: "mysql",
        databaseService: "mysql84",
        createDatabase: false,
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
