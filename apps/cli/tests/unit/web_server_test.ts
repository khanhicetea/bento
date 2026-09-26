import { describe, expect, test } from "bun:test";
import { createRouterClient } from "@orpc/server";
import {
  addApplicationDatabaseInputSchema,
  backupRunStatusSchema,
  applicationListSchema,
  removeApplicationInputSchema,
  saveApplicationInputSchema,
  schedulerAccessSchema,
  setApplicationEnabledInputSchema,
  setApplicationRunningInputSchema,
  webContract,
} from "@bento/shared";
import { createApplicationsRouter, toApplication } from "../../src/server/domains/applications/router.ts";
import { createOperationsRouter } from "../../src/server/domains/operations/router.ts";
import { createDataRouter } from "../../src/server/domains/data/router.ts";
import { runScheduledBackup } from "../../src/services/backup_schedule.ts";
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

  test("backup run status is available without host crontab access and follows the shared contract", async () => {
    const root = await runtime.makeTempDir({ prefix: "bento-web-backup-status-" });
    try {
      const ctx = createContext({ stackRoot: root, repoRoot: runtime.cwd() });
      const runner = createRecordingProcessRunner(() => {
        throw new Error("web process must not access the host crontab");
      });
      ctx.platform.process = runner;
      const client = createRouterClient(createOperationsRouter(ctx));
      expect(backupRunStatusSchema.parse(await client.backupRunStatus({}))).toEqual({
        lastRun: null,
        lastOperation: null,
      });
      await runScheduledBackup(ctx.platform, createEmptyState());
      const result = backupRunStatusSchema.parse(await client.backupRunStatus({}));
      expect(result.lastRun?.status).toBe("succeeded");
      expect(result.lastOperation?.steps).toEqual([{ name: "backup", status: "succeeded" }]);
      expect(result.lastRun?.operationId).toBe(result.lastOperation?.id);
      expect(runner.calls).toHaveLength(0);
    } finally {
      await runtime.remove(root, { recursive: true });
    }
  });

  test("web restore refuses existing targets and artifacts from another binding before database side effects", async () => {
    const root = await runtime.makeTempDir({ prefix: "bento-web-restore-guard-" });
    try {
      const ctx = createContext({ stackRoot: root, repoRoot: runtime.cwd() });
      const state = await ctx.store.init();
      const next = provisionApp(ctx.platform, state, { slug: "demo", domain: "demo.test", createDatabase: true }).state;
      await ctx.store.save(next);
      const dir = `${ctx.platform.paths.paths.backupsDir}/mysql84/demo`;
      await ctx.platform.fs.mkdirp(dir);
      await ctx.platform.fs.writeText(`${dir}/dump.sql.zst`, "SELECT 1;", 0o600);
      const runner = createRecordingProcessRunner(() => {
        throw new Error("database must not be touched");
      });
      ctx.platform.process = runner;
      const client = createRouterClient(createDataRouter(ctx));
      await expect(
        client.restore({
          app: "demo",
          engine: "mysql",
          artifact: "mysql84/demo/dump.sql.zst",
          targetDatabase: "demo",
          confirmation: "demo",
        }),
      ).rejects.toThrow("unused target");
      await expect(
        client.restore({
          app: "demo",
          engine: "mysql",
          artifact: "mysql84/other/dump.sql.zst",
          targetDatabase: "demo_verify",
          confirmation: "demo_verify",
        }),
      ).rejects.toThrow("recorded database binding");
      expect(runner.calls).toHaveLength(0);
      const release = await ctx.platform.lock.tryExclusive(`${ctx.platform.paths.paths.lockDir}/database-backup.lock`);
      try {
        await expect(
          client.restore({
            app: "demo",
            engine: "mysql",
            artifact: "mysql84/demo/dump.sql.zst",
            targetDatabase: "demo_verify",
            confirmation: "demo_verify",
          }),
        ).rejects.toThrow("logical backup is running");
      } finally {
        await release!();
      }
      expect(runner.calls).toHaveLength(0);
      const started = await client.startBackup({ app: "demo" });
      expect(started.id).toMatch(/^op_[a-f0-9]{16}$/);
      let backupStatus = (await client.backupRuns({})).runs.find((run) => run.id === started.id)?.status;
      for (let n = 0; n < 30 && backupStatus === "running"; n++) {
        await Bun.sleep(5);
        backupStatus = (await client.backupRuns({})).runs.find((run) => run.id === started.id)?.status;
      }
      expect(backupStatus).toBe("failed");
    } finally {
      await runtime.remove(root, { recursive: true });
    }
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

test("application public key is fetched only for an existing app and never includes the private key", async () => {
  const root = await runtime.makeTempDir();
  try {
    const ctx = createContext({ stackRoot: root, repoRoot: runtime.cwd() });
    await ctx.store.init();
    const state = await ctx.store.load();
    const provisioned = provisionApp(ctx.platform, state, { slug: "demo", domain: "demo.test" });
    await ctx.store.save(provisioned.state);
    const sshDir = `${ctx.platform.paths.appHome("demo")}/.ssh`;
    await ctx.platform.fs.mkdirp(sshDir);
    await ctx.platform.fs.writeText(`${sshDir}/id_ed25519.pub`, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA demo\n");
    await ctx.platform.fs.writeText(`${sshDir}/id_ed25519`, "PRIVATE KEY");
    const client = createRouterClient(createApplicationsRouter(ctx));
    expect(await client.publicKey({ slug: "demo" })).toEqual({
      publicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA demo",
    });
    expect(JSON.stringify(await client.list({}))).not.toContain("AAAAC3");
    await expect(client.publicKey({ slug: "missing" })).rejects.toMatchObject({ code: "NOT_FOUND" });
    await ctx.platform.fs.remove(`${sshDir}/id_ed25519.pub`);
    await expect(client.publicKey({ slug: "demo" })).rejects.toMatchObject({ code: "NOT_FOUND" });
  } finally {
    await runtime.remove(root, { recursive: true });
  }
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
