import type { TlsMode } from "../../domain/state.ts";
import { FPM_PROFILES } from "../../domain/types.ts";
import {
  applyAppDataPlane,
  capacityWarnings,
  deleteApp,
  materializeAppHome,
  provisionApp,
  setAppEnabled,
} from "../../services/app.ts";
import { buildMysqlShellPlan, createAppDatabaseLive } from "../../services/mysql.ts";
import { buildPostgresShellPlan, createPostgresAppDatabaseLive } from "../../services/postgres.ts";
import { enableSqliteBackup, sqliteCompose } from "../../services/sqlite.ts";
import { sqliteContainerPath } from "../../services/sqlite_paths.ts";
import {
  loadRedisPassword,
  requireMysqlRootPassword,
  requirePostgresRootPassword,
} from "../../services/stack_env.ts";
import { type MenuChoice, WizardUI } from "../../ui/tui.ts";
import type { CliContext } from "../context.ts";
import { runCliExec } from "../subcommands/exec.ts";
import { sectionCron } from "./cron.ts";
import { sectionLogs } from "./logs.ts";
import { ensureState, handleError, openMysqlShell, openPostgresShell, pcDim } from "./shared.ts";
import { sectionTemplate } from "./templates.ts";
import { sectionWorker } from "./workers.ts";

export async function sectionApps(ui: WizardUI, ctx: CliContext): Promise<void> {
  ui.header("Manage app");
  if (!(await ensureState(ui, ctx))) return;

  while (true) {
    const state = await ctx.store.load();
    const choices: MenuChoice<string>[] = Object.values(state.apps)
      .sort((a, b) => a.slug.localeCompare(b.slug))
      .map((app) => ({
        label: app.slug,
        value: app.slug,
        hint: `${app.enabled ? "enabled" : "disabled"} · ${app.mainDomain} · php ${app.phpVersion}`,
      }));
    choices.push({ label: "Create application…", value: "__create" });

    const slug = await ui.menu("Select application", choices);
    if (!slug) return;
    if (slug === "__create") {
      await wizardAppCreate(ui, ctx);
      continue;
    }

    while (true) {
      const current = (await ctx.store.load()).apps[slug];
      if (!current) break;
      ui.clear();
      ui.header(`App: ${slug}`, `${current.mainDomain} · php ${current.phpVersion}`);
      const action = await ui.menu("Manage application", [
        { label: "Shell", value: "shell", hint: "enter app CLI shell" },
        { label: "Databases", value: "databases", hint: "list · create · shell" },
        { label: "Cron jobs", value: "cron", hint: "list · add · edit · remove" },
        { label: "Workers", value: "workers", hint: "list · add · control" },
        { label: "Domains", value: "domains", hint: "primary · aliases · TLS" },
        { label: "Access logs", value: "logs", hint: "enable · rotate · report" },
        { label: "Templates", value: "templates", hint: "vhost · FPM pool · drift" },
        current.enabled
          ? {
            label: "Disable application",
            value: "disable",
            hint: "stop pool, runner jobs, and vhost",
          }
          : { label: "Enable application", value: "enable", hint: "restore runtime configuration" },
        { label: "Remove application", value: "remove", hint: "durable data is retained" },
      ]);
      if (!action) break;

      if (action === "shell") await openAppShell(ui, ctx, slug);
      else if (action === "databases") await sectionAppDatabases(ui, ctx, slug);
      else if (action === "cron") await sectionCron(ui, ctx, slug);
      else if (action === "workers") await sectionWorker(ui, ctx, slug);
      else if (action === "domains") await sectionAppDomains(ui, ctx, slug);
      else if (action === "logs") await sectionLogs(ui, ctx, slug);
      else if (action === "templates") await sectionTemplate(ui, ctx, slug);
      else if (action === "enable" || action === "disable") {
        await wizardSetAppEnabled(ui, ctx, slug, action === "enable");
      } else if (action === "remove") {
        if (await wizardRemoveApp(ui, ctx, slug)) break;
      }
    }
  }
}

async function wizardSetAppEnabled(
  ui: WizardUI,
  ctx: CliContext,
  slug: string,
  enabled: boolean,
): Promise<void> {
  const verb = enabled ? "Enable" : "Disable";
  if (!(await ui.confirm(`${verb} application ${slug}?`, { defaultYes: false }))) return;
  try {
    await ctx.store.withExclusive(async (state) => {
      const changed = setAppEnabled(state, slug, enabled, ctx.platform.clock.nowIso());
      await ctx.store.save(changed.state);
      await ctx.render.apply(changed.state, {
        reloadPlan: changed.reloadPlan,
        skipValidate: false,
        alreadyLocked: true,
      });
      return changed;
    });
    ui.success(`${enabled ? "Enabled" : "Disabled"} app ${slug}`);
  } catch (err) {
    handleError(ui, err);
  }
  await ui.pause();
}

async function wizardRemoveApp(
  ui: WizardUI,
  ctx: CliContext,
  slug: string,
): Promise<boolean> {
  const expected = `delete ${slug}`;
  const confirmation = await ui.prompt(`Type '${expected}' to confirm removal`, {
    required: true,
  });
  if (confirmation === null) return false;
  try {
    await ctx.store.withExclusive(async (state) => {
      const removed = deleteApp(state, slug, confirmation, ctx.platform.clock.nowIso());
      await ctx.store.save(removed.state);
      await ctx.render.apply(removed.state, {
        reloadPlan: removed.reloadPlan,
        skipValidate: false,
        alreadyLocked: true,
      });
      return removed;
    });
    ui.success(`Removed app ${slug}`, "Durable home and database data were retained.");
    await ui.pause();
    return true;
  } catch (err) {
    handleError(ui, err);
    await ui.pause();
    return false;
  }
}

async function openAppShell(ui: WizardUI, ctx: CliContext, slug: string): Promise<void> {
  ui.blank();
  ui.info(`Attaching app shell as ${slug}`);
  ui.message(pcDim(`scriptable: bento app shell ${slug}`));
  ui.message(pcDim("Exit the shell to return to the wizard."));
  ui.blank();

  try {
    const exitCode = await runCliExec(ctx, {
      slug,
      argv: [],
      printOnly: false,
    });
    ui.blank();
    if (exitCode === 0) ui.success("App shell closed", slug);
    else ui.warn(`App shell exited ${exitCode}`, slug);
  } catch (err) {
    handleError(ui, err);
  }
  await ui.pause();
}

/** Interactively create or update an application. */
async function wizardAppCreate(ui: WizardUI, ctx: CliContext): Promise<void> {
  ui.blank();
  ui.message(pcDim("Create or update an application (same identity on update)."));
  const slug = await ui.prompt("App slug", { required: true });
  if (slug === null) return;
  const domain = await ui.prompt("Primary domain", { required: true });
  if (domain === null) return;
  const aliasRaw = await ui.prompt("Domain aliases (comma-separated)", { default: "" });
  if (aliasRaw === null) return;
  const aliases = aliasRaw.split(",").map((s) => s.trim()).filter(Boolean);

  const state = await ctx.store.load();
  const phpChoices: MenuChoice<string>[] = state.phpVersions.map((v) => ({
    label: v.version,
    value: v.version,
    hint: v.version === state.defaults.phpVersion ? "default" : v.image,
  }));
  phpChoices.push({ label: `Use default (${state.defaults.phpVersion})`, value: "" });
  const php = await ui.menu("PHP version", phpChoices);
  if (php === null) return;

  const fpmChoices: MenuChoice<string>[] = Object.keys(FPM_PROFILES).map((p) => ({
    label: p,
    value: p,
    hint: p === state.defaults.fpmProfile ? "default" : undefined,
  }));
  fpmChoices.push({ label: `Use default (${state.defaults.fpmProfile})`, value: "" });
  const fpm = await ui.menu("FPM profile", fpmChoices);
  if (fpm === null) return;

  const docroot = await ui.prompt("Document root (relative to app home)", {
    default: "public",
  });
  if (docroot === null) return;

  const entry = await ui.menu<"front-controller" | "legacy" | "">("Entrypoint mode", [
    { label: "Front controller (recommended)", value: "front-controller" },
    { label: "Legacy (direct PHP file execution)", value: "legacy" },
    { label: "Keep existing / default", value: "" },
  ]);
  if (entry === null) return;

  const existing = state.apps[slug];
  const databaseChoices: MenuChoice<string>[] = state.databaseServices.map((service) => ({
    label: `${service.engine}: ${service.version}`,
    value: `${service.engine}:${service.service}`,
    hint: existing?.database.engine !== "sqlite" &&
        existing?.database.service === service.service
      ? "current"
      : state.defaults.database.service === service.service
      ? "default"
      : service.service,
  }));
  databaseChoices.push({
    label: "SQLite",
    value: "sqlite",
    hint: existing?.database.engine === "sqlite"
      ? "current · Litestream backup"
      : "local file · Litestream backup",
  });
  if (existing) {
    databaseChoices.unshift({
      label: existing.database.engine === "sqlite"
        ? "Keep current (SQLite)"
        : `Keep current (${existing.database.engine}: ${existing.database.service})`,
      value: "",
    });
  }
  const databaseSelection = await ui.menu("Database", databaseChoices);
  if (databaseSelection === null) return;
  const [databaseEngine, databaseService] = databaseSelection
    ? databaseSelection.split(":", 2)
    : [undefined, undefined];
  const effectiveDatabaseEngine = databaseEngine ?? existing?.database.engine ??
    state.defaults.database.engine;
  const useSqlite = effectiveDatabaseEngine === "sqlite";

  const createDb = useSqlite
    ? false
    : await ui.confirm("Create a namespaced database for this app?");
  const databaseName = createDb
    ? await ui.prompt("Database name (blank = auto)", { default: "" })
    : "";
  if (createDb && databaseName === null) return;
  if (useSqlite) {
    ui.info("SQLite continuous backup will be enabled with Litestream.");
    ui.message(
      state.sqliteBackup?.enabled
        ? "The existing stack-wide policy will automatically cover this database."
        : "Requires BENTO_LITESTREAM_ENABLED=true and S3 settings in the stack environment.",
    );
  }

  const accessLog = await ui.confirm("Enable per-app access logs?", { defaultYes: false });
  const noApply = useSqlite
    ? false
    : !(await ui.confirm("Render & apply after save?", { defaultYes: true }));

  ui.blank();
  ui.table(
    ["field", "value"],
    [
      ["slug", slug],
      ["domain", domain],
      ["aliases", aliases.join(", ") || "-"],
      ["php", php || `(default ${state.defaults.phpVersion})`],
      ["fpm", fpm || `(default ${state.defaults.fpmProfile})`],
      ["docroot", docroot || "public"],
      ["entry", entry || "default"],
      [
        "database",
        `${useSqlite ? "SQLite" : databaseSelection || "keep current"}${
          createDb ? ` · create ${databaseName || "auto"}` : ""
        }`,
      ],
      ...(useSqlite ? [["sqlite-backup", "Litestream · enabled"]] : []),
      ["access-log", accessLog ? "yes" : "no"],
      ["apply", noApply ? "skip" : "yes"],
    ],
  );

  if (!(await ui.confirm("Proceed?", { defaultYes: true }))) {
    ui.info("Cancelled.");
    await ui.pause();
    return;
  }

  try {
    const result = await ctx.store.withExclusive(async (s) => {
      const provisioned = provisionApp(ctx.platform, s, {
        slug,
        domain,
        aliases,
        documentRoot: docroot || undefined,
        entrypointMode: entry || undefined,
        phpVersion: php || undefined,
        fpmProfile: fpm || undefined,
        databaseEngine,
        mysqlVersion: databaseEngine === "mysql" ? databaseService : undefined,
        postgresVersion: databaseEngine === "postgres" ? databaseService : undefined,
        createDatabase: createDb,
        databaseName: databaseName || undefined,
        accessLog,
      });
      const plane = await applyAppDataPlane(ctx.platform, provisioned.app, {
        explicitDatabase: createDb,
      });
      const redisShared = await loadRedisPassword(ctx.platform);
      await materializeAppHome(ctx.platform, provisioned.app, {
        recursivePerms: true,
        redisSharedPassword: redisShared,
      });

      let nextState = provisioned.state;
      let sqliteBackupEnabledNow = false;
      if (provisioned.app.database.engine === "sqlite" && !nextState.sqliteBackup?.enabled) {
        nextState = await enableSqliteBackup(ctx.platform, nextState, slug);
        sqliteBackupEnabledNow = true;
      }

      await ctx.store.save(nextState);
      if (!noApply) {
        await ctx.render.apply(nextState, {
          reloadPlan: provisioned.reloadPlan,
          skipValidate: false,
          alreadyLocked: true,
        });
      }
      return { provisioned, plane, state: nextState, sqliteBackupEnabledNow };
    });

    if (result.provisioned.app.database.engine === "sqlite") {
      if (result.sqliteBackupEnabledNow) {
        const up = await sqliteCompose(ctx.platform, result.state, [
          "up",
          "-d",
          "--force-recreate",
          "litestream",
        ]);
        if (up.code !== 0) {
          throw new Error(`Litestream container failed to start: ${up.stderr.trim()}`);
        }
      }
      ui.success(
        "SQLite Litestream backup enabled",
        "The watcher will discover the database after the application initializes it.",
      );
    }

    ui.success(
      `${result.provisioned.created ? "Created" : "Updated"} app ${result.provisioned.app.slug}`,
      `uid=${result.provisioned.app.uid} domain=${result.provisioned.app.mainDomain}`,
    );
    for (const note of result.plane.deferredNotes) ui.warn(note);
    for (const w of capacityWarnings(result.state)) ui.warn(w);
  } catch (err) {
    handleError(ui, err);
  }
  await ui.pause();
}

async function sectionAppDatabases(
  ui: WizardUI,
  ctx: CliContext,
  slug: string,
): Promise<void> {
  while (true) {
    const state = await ctx.store.load();
    const app = state.apps[slug];
    if (!app) return;

    ui.clear();
    if (app.database.engine === "sqlite") {
      ui.header(`SQLite: ${slug}`, "local database · stack-wide Litestream backup");
      ui.table(
        ["field", "value"],
        [
          ["file", sqliteContainerPath(app.database.file.id, app.slug)],
          ["backup", state.sqliteBackup?.enabled ? "enabled" : "disabled"],
          ["verified", app.database.backupVerifiedAt ?? "never"],
        ],
      );
      await ui.pause();
      return;
    }

    ui.header(
      `Databases: ${slug}`,
      `${app.database.service} · ${app.database.databases.length} database${
        app.database.databases.length === 1 ? "" : "s"
      }`,
    );
    ui.table(
      ["database", "created"],
      app.database.databases.map((db) => [db.name, db.createdAt]),
    );
    ui.blank();
    const action = await ui.menu("Database actions", [
      { label: "Create database", value: "create" },
      {
        label: app.database.engine === "postgres" ? "Open PostgreSQL shell" : "Open MySQL shell",
        value: "shell",
        hint: `as app ${slug}`,
      },
    ]);
    if (!action) return;

    try {
      if (action === "create") {
        const dbName = await ui.prompt("Database name", {
          required: true,
          default: slug,
        });
        if (!dbName) continue;
        await ctx.store.withExclusive(async (state) => {
          const current = state.apps[slug];
          if (!current) throw new Error(`app not found: ${slug}`);
          const next = current.database.engine === "postgres"
            ? await createPostgresAppDatabaseLive(
              ctx.platform,
              state,
              slug,
              dbName,
              await requirePostgresRootPassword(ctx.platform),
            )
            : await createAppDatabaseLive(
              ctx.platform,
              state,
              slug,
              dbName,
              await requireMysqlRootPassword(ctx.platform),
            );
          const app = next.apps[slug]!;
          const redisShared = await loadRedisPassword(ctx.platform);
          await materializeAppHome(ctx.platform, app, {
            recursivePerms: false,
            redisSharedPassword: redisShared,
          });
          await ctx.store.save(next);
          return next;
        });
        ui.success(`Created database ${dbName}`, `app=${slug}`);
      } else {
        const state = await ctx.store.load();
        const app = state.apps[slug];
        if (!app) throw new Error(`app not found: ${slug}`);
        if (app.database.engine === "postgres") {
          await openPostgresShell(
            ui,
            ctx,
            buildPostgresShellPlan(ctx.platform, { kind: "app", app }),
            `bento postgres shell --app ${slug}`,
          );
        } else {
          await openMysqlShell(
            ui,
            ctx,
            buildMysqlShellPlan(ctx.platform, { kind: "app", app }),
            `bento mysql shell --app ${slug}`,
          );
        }
      }
    } catch (err) {
      handleError(ui, err);
    }
    await ui.pause();
  }
}

async function sectionAppDomains(
  ui: WizardUI,
  ctx: CliContext,
  slug: string,
): Promise<void> {
  while (true) {
    const app = (await ctx.store.load()).apps[slug];
    if (!app) return;
    ui.clear();
    ui.header(`Domains: ${slug}`, `${app.mainDomain} · TLS ${app.tls.kind}`);
    ui.table(
      ["kind", "domain"],
      [["primary", app.mainDomain], ...app.aliases.map((alias) => ["alias", alias])],
    );
    ui.blank();
    const action = await ui.menu("Domain actions", [
      { label: "Update primary domain / aliases", value: "update" },
      { label: "Configure TLS", value: "tls" },
    ]);
    if (!action) return;

    try {
      if (action === "update") {
        const domain = await ui.prompt("Primary domain", {
          required: true,
          default: app.mainDomain,
        });
        if (!domain) continue;
        const aliasesRaw = await ui.prompt("Aliases (comma-separated)", {
          default: app.aliases.join(","),
        });
        if (aliasesRaw === null) continue;
        const aliases = aliasesRaw.split(",").map((value) => value.trim()).filter(Boolean);
        await ctx.store.withExclusive(async (state) => {
          const result = provisionApp(ctx.platform, state, { slug, domain, aliases });
          await ctx.store.save(result.state);
          await ctx.render.apply(result.state, {
            reloadPlan: { nginx: true, phpFpm: new Set(), phpRunner: new Set() },
            skipValidate: false,
            alreadyLocked: true,
          });
          return result;
        });
        ui.success(`Updated domains for ${slug}`, [domain, ...aliases].join(", "));
      } else {
        const mode = await ui.menu<"self-ca" | "shared" | "acme" | "external">(
          "TLS mode",
          [
            { label: "Self-CA (private CA, per-site certificate)", value: "self-ca" },
            { label: "Shared self-signed starter", value: "shared" },
            { label: "ACME (Let's Encrypt)", value: "acme" },
            { label: "External certificate files", value: "external" },
          ],
        );
        if (!mode) continue;

        let tls: TlsMode;
        if (mode === "self-ca") {
          tls = { kind: "self-ca" };
        } else if (mode === "shared") {
          tls = { kind: "shared" };
        } else if (mode === "acme") {
          tls = { kind: "acme" };
        } else {
          const cert = await ui.prompt("Certificate path", { required: true });
          if (!cert) continue;
          const key = await ui.prompt("Private key path", { required: true });
          if (!key) continue;
          tls = { kind: "external", certPath: cert, keyPath: key };
        }

        await ctx.store.withExclusive(async (state) => {
          const current = state.apps[slug];
          if (!current) throw new Error(`app not found: ${slug}`);
          const now = ctx.platform.clock.nowIso();
          const next = {
            ...state,
            apps: { ...state.apps, [slug]: { ...current, tls, updatedAt: now } },
            updatedAt: now,
          };
          await ctx.store.save(next);
          await ctx.render.apply(next, {
            reloadPlan: { nginx: true, phpFpm: new Set(), phpRunner: new Set() },
            skipValidate: true,
            alreadyLocked: true,
          });
          return next;
        });
        ui.success(`TLS mode set to ${mode}`, `app:${slug}`);
      }
    } catch (err) {
      handleError(ui, err);
    }
    await ui.pause();
  }
}
