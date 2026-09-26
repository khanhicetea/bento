import { describe, expect, test } from "bun:test";
import { createRouterClient } from "@orpc/server";
import {
  addApplicationDatabaseInputSchema,
  applicationListSchema,
  removeApplicationInputSchema,
  saveApplicationInputSchema,
  schedulerAccessSchema,
  setApplicationEnabledInputSchema,
  setApplicationRunningInputSchema,
  webContract,
} from "@bento/shared";
import { createApplicationsRouter, toApplication } from "../../src/server/domains/applications/router.ts";
import { createEmptyState } from "../../src/domain/state.ts";
import { provisionApp } from "../../src/services/app.ts";
import { createPlatform } from "../../src/platform/mod.ts";
import {
  acceptsWebAuthorization,
  matchesBasicAuthorization,
  SchedulerSessions,
  schedulerAppFromPath,
  schedulerSessionCookie,
} from "../../src/server/server.ts";
import { createContext } from "../../src/commands/context.ts";
import { RenderService } from "../../src/services/render.ts";
import { createApplicationUseCases } from "../../src/use_cases/applications.ts";
import { createRecordingProcessRunner } from "../../src/platform/mod.ts";
import { runtime } from "../runtime.ts";

describe("web server authentication", () => {
  test("accepts only the exact Basic authorization value", () => {
    const expected = `Basic ${Buffer.from("operator:secret").toString("base64")}`;

    expect(matchesBasicAuthorization(expected, expected)).toBe(true);
    expect(matchesBasicAuthorization(null, expected)).toBe(false);
    expect(matchesBasicAuthorization(`Basic ${Buffer.from("operator:wrong").toString("base64")}`, expected)).toBe(
      false,
    );
    expect(matchesBasicAuthorization(`basic ${Buffer.from("operator:secret").toString("base64")}`, expected)).toBe(
      false,
    );
  });

  test("issues, scopes, and revokes scheduler sessions", () => {
    const sessions = new SchedulerSessions(() => "ab".repeat(32));
    const setCookie = sessions.issue();
    const cookie = setCookie.split(";", 1)[0]!;

    expect(setCookie).not.toContain("Domain=");
    expect(setCookie).toContain("HttpOnly");
    expect(setCookie).toContain("SameSite=Strict");
    expect(sessions.authorizes(cookie, "demo")).toBe(true);
    expect(sessions.authorizes(cookie, "../other")).toBe(false);
    expect(sessions.authorizes("bento_scheduler_session=wrong", "demo")).toBe(false);

    sessions.revoke(cookie);
    expect(sessions.authorizes(cookie, "demo")).toBe(false);
  });

  test("establishes sessions on normal pages and direct scheduler navigations", () => {
    const sessions = new SchedulerSessions(() => "ab".repeat(32));
    const setCookie = schedulerSessionCookie(sessions, null, "/");
    expect(setCookie).toStartWith("bento_scheduler_session=");
    const cookie = setCookie!.split(";", 1)[0]!;
    expect(schedulerSessionCookie(sessions, cookie, "/")).toBeUndefined();
    expect(sessions.authorizes(cookie, "demo")).toBe(true);
    expect(schedulerSessionCookie(sessions, null, "/scheduler/apps/demo/")).toBeDefined();
    expect(schedulerSessionCookie(sessions, null, "/scheduler/apps")).toBeUndefined();
    expect(sessions.authorizes(null, "demo")).toBe(false);
  });

  test("a scheduler session authenticates only app-scoped routes", () => {
    const expected = `Basic ${Buffer.from("operator:secret").toString("base64")}`;
    expect(acceptsWebAuthorization(null, expected, "demo", true)).toBe(true);
    expect(acceptsWebAuthorization(null, expected, "demo", false)).toBe(false);
    expect(acceptsWebAuthorization(null, expected, undefined, true)).toBe(false);
    expect(acceptsWebAuthorization("Basic wrong", expected, undefined, true)).toBe(false);
    expect(acceptsWebAuthorization(expected, expected, "demo", false)).toBe(true);
  });

  test("recognizes only app scheduler path segments", () => {
    expect(schedulerAppFromPath("/scheduler/apps/demo/api/v1/jobs")).toBe("demo");
    expect(schedulerAppFromPath("/scheduler/apps/demo/")).toBe("demo");
    expect(schedulerAppFromPath("/scheduler/apps/demo%2fother/")).toBeUndefined();
    expect(schedulerAppFromPath("/scheduler/apps/demo.evil/")).toBeUndefined();
    expect(schedulerAppFromPath("/apps/demo/")).toBeUndefined();
  });
});

describe("web API contract", () => {
  test("is composed from explicit domain routers", () => {
    expect(Object.keys(webContract)).toEqual(["system", "applications", "data", "routing", "jobs", "operations"]);
    expect("execute" in webContract).toBe(false);
    expect(Object.keys(webContract.jobs)).toEqual(["schedulerAccess"]);
    expect("schedulerAccess" in webContract.jobs).toBe(true);
    expect(
      schedulerAccessSchema.parse({
        enabled: true,
        schedulers: [{ app: "demo", path: "/scheduler/apps/demo/" }],
      }),
    ).toEqual({
      enabled: true,
      schedulers: [{ app: "demo", path: "/scheduler/apps/demo/" }],
    });
    expect(() =>
      schedulerAccessSchema.parse({
        enabled: true,
        schedulers: [{ app: "demo", path: "/apps/demo/" }],
      }),
    ).toThrow();
  });

  test("application deploy summary stays in its domain and never exposes argv secrets", () => {
    const platform = createPlatform("/tmp/bento-web-deploy-summary", runtime.cwd());
    const { app } = provisionApp(platform, createEmptyState(), {
      slug: "demo",
      domain: "demo.test",
    });
    app.deploy.enabled = true;
    app.deploy.argv = ["/usr/bin/deploy", "--token", "private-value"];
    const response = toApplication(app);
    expect(response.deploySummary).toEqual({
      queuePolicy: app.deploy.queuePolicy,
      timeoutSec: app.deploy.timeoutSec,
      command: "deploy (+2 args)",
    });
    expect(JSON.stringify(response)).not.toContain("private-value");
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
    expect(() => addApplicationDatabaseInputSchema.parse({ slug: "demo", engine: "mysql" })).toThrow();
    expect(removeApplicationInputSchema.parse({ slug: "demo", confirmation: "delete demo" })).toBeTruthy();
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

test("application save reports an apply failure after persisting the app", async () => {
  const root = await runtime.makeTempDir();
  try {
    const ctx = createContext({ stackRoot: root, repoRoot: runtime.cwd() });
    ctx.platform.process = createRecordingProcessRunner();
    await ctx.store.init();
    const initialState = await ctx.store.load();
    ctx.render = new (class extends RenderService {
      override async apply(): Promise<never> {
        throw new Error("private validator detail");
      }
    })(ctx.platform);
    ctx.applications = createApplicationUseCases({
      platform: ctx.platform,
      store: ctx.store,
      render: ctx.render,
    });

    const client = createRouterClient(createApplicationsRouter(ctx));
    await expect(
      client.save({
        slug: "demo",
        kind: "php",
        domain: "demo.example.test",
        aliases: [],
        documentRoot: "public",
        entrypointMode: "front-controller",
        phpVersion: initialState.defaults.phpVersion,
        fpmProfile: initialState.defaults.fpmProfile,
        tls: "shared",
        accessLog: false,
        databaseEngine: "sqlite",
        createDatabase: false,
      }),
    ).rejects.toMatchObject({
      code: "INTERNAL_SERVER_ERROR",
      message: expect.stringContaining("settings were saved, but applying them failed"),
    });
    expect((await ctx.store.load()).apps.demo).toBeDefined();
    expect((await client.list({})).applications.map((app) => app.slug)).toContain("demo");
  } finally {
    await runtime.remove(root, { recursive: true });
  }
});
