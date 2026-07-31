import type {
  AppDatabaseBinding,
  AppState,
  DatabaseEngine,
  DesiredState,
  TlsMode,
} from "../../domain/state.ts";
import { FPM_PROFILES } from "../../domain/types.ts";
import {
  parseAbsolutePath,
  parseAppSlug,
  parseDomainName,
  parseSafeRelativePath,
} from "../../schemas/validators.ts";
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
import {
  ensureState,
  fieldValidator,
  handleError,
  openMysqlShell,
  openPostgresShell,
  pcDim,
  sqliteFileSize,
} from "./shared.ts";
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
  const state = await ctx.store.load();
  const draft = await collectAppCreateDraft(ui, state);
  if (!draft) return;
  const {
    slug,
    domain,
    php,
    fpm,
    docroot,
    entry,
    createDb,
    databaseName,
    accessLog,
    noApply,
  } = draft;
  const aliases = parseAliases(draft.aliasRaw);
  const {
    databaseEngine,
    databaseService,
    effectiveDatabaseEngine,
    useLitestream,
    useFileDatabase,
  } = appCreateDatabaseDetails(state, draft);

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
        databaseEngine: effectiveDatabaseEngine,
        databaseService,
        databaseName: databaseName || (createDb && !useFileDatabase ? slug : undefined),
      });
      const redisShared = await loadRedisPassword(ctx.platform);
      await materializeAppHome(ctx.platform, provisioned.app, {
        recursivePerms: true,
        redisSharedPassword: redisShared,
      });

      let nextState = provisioned.state;
      let sqliteBackupEnabledNow = false;
      if (useLitestream && !nextState.sqliteBackup?.enabled) {
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

    if (useLitestream) {
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

export type AppCreateDraft = {
  slug: string;
  domain: string;
  aliasRaw: string;
  php: string;
  fpm: string;
  docroot: string;
  entry: "front-controller" | "legacy" | "";
  databaseSelection: string;
  createDb: boolean;
  databaseName: string;
  accessLog: boolean;
  noApply: boolean;
};

type AppCreateField = keyof AppCreateDraft;
type AppCreateNavigation = "next" | "back" | "cancel";

const APP_CREATE_STEP_NAMES = ["Identity", "Runtime", "Database", "Options", "Review"];
const APP_CREATE_STEP_FIELDS: AppCreateField[][] = [
  ["slug", "domain", "aliasRaw"],
  ["php", "fpm", "docroot", "entry"],
  ["databaseSelection", "createDb", "databaseName"],
  ["accessLog", "noApply"],
];

export async function collectAppCreateDraft(
  ui: WizardUI,
  state: DesiredState,
): Promise<AppCreateDraft | null> {
  const draft: AppCreateDraft = {
    slug: "",
    domain: "",
    aliasRaw: "",
    php: "",
    fpm: "",
    docroot: "public",
    entry: "front-controller",
    databaseSelection: "",
    createDb: false,
    databaseName: "",
    accessLog: false,
    noApply: false,
  };
  let step = 0;
  let enterAtEnd = false;

  while (true) {
    if (step < 4) {
      const navigation = await collectAppCreateStep(ui, state, draft, step, enterAtEnd);
      if (navigation === "cancel") return null;
      if (navigation === "back") {
        if (step === 0) {
          ui.info("Application creation cancelled.");
          return null;
        }
        step--;
        enterAtEnd = true;
        continue;
      }
      step++;
      enterAtEnd = false;
      continue;
    }

    renderAppCreateScreen(ui, state, draft, 4);
    const reviewAction = await ui.menu<"apply" | "change" | "cancel">(
      "Review application",
      [
        { label: "Apply", value: "apply", hint: "save and reconcile the application" },
        { label: "Change a field", value: "change", hint: "edit one answer, then review again" },
        { label: "Cancel", value: "cancel", hint: "discard this draft" },
      ],
      { cancelLabel: "Back", quitValue: "cancel", initialValue: "apply" },
    );
    if (reviewAction === null) {
      step = 3;
      enterAtEnd = true;
      continue;
    }
    if (reviewAction === "cancel") {
      ui.info("Application creation cancelled.");
      return null;
    }
    if (reviewAction === "apply") return draft;

    const field = await chooseAppCreateField(ui, state, draft);
    if (field === "cancel") return null;
    if (field !== null) {
      const navigation = await editAppCreateField(ui, state, draft, field);
      if (navigation === "cancel") return null;
    }
  }
}

async function collectAppCreateStep(
  ui: WizardUI,
  state: DesiredState,
  draft: AppCreateDraft,
  step: number,
  enterAtEnd: boolean,
): Promise<AppCreateNavigation> {
  let fields = visibleAppCreateFields(state, draft, step);
  let index = enterAtEnd ? fields.length - 1 : 0;
  while (index < fields.length) {
    renderAppCreateScreen(ui, state, draft, step);
    const navigation = await editAppCreateField(ui, state, draft, fields[index]!, false);
    if (navigation === "cancel") return "cancel";
    if (navigation === "back") {
      if (index === 0) return "back";
      index--;
      continue;
    }
    fields = visibleAppCreateFields(state, draft, step);
    index++;
  }
  return "next";
}

function visibleAppCreateFields(
  state: DesiredState,
  draft: AppCreateDraft,
  step: number,
): AppCreateField[] {
  const fields = APP_CREATE_STEP_FIELDS[step] ?? [];
  const { useFileDatabase } = appCreateDatabaseDetails(state, draft);
  if (step === 3 && useFileDatabase) return ["accessLog"];
  if (step !== 2) return fields;
  if (useFileDatabase) return ["databaseSelection"];
  return draft.createDb ? fields : ["databaseSelection", "createDb"];
}

async function chooseAppCreateField(
  ui: WizardUI,
  state: DesiredState,
  draft: AppCreateDraft,
): Promise<AppCreateField | "cancel" | null> {
  const details = appCreateDatabaseDetails(state, draft);
  return await ui.menu<AppCreateField | "cancel">(
    "Change a field",
    [
      { label: "App slug", value: "slug", hint: draft.slug },
      { label: "Primary domain", value: "domain", hint: draft.domain },
      { label: "Domain aliases", value: "aliasRaw", hint: draft.aliasRaw || "none" },
      { label: "PHP version", value: "php", hint: draft.php || state.defaults.phpVersion },
      { label: "FPM profile", value: "fpm", hint: draft.fpm || state.defaults.fpmProfile },
      { label: "Document root", value: "docroot", hint: draft.docroot },
      { label: "Entrypoint mode", value: "entry", hint: draft.entry || "default" },
      {
        label: "Database",
        value: "databaseSelection",
        hint: draft.databaseSelection || `default (${details.effectiveDatabaseEngine})`,
      },
      ...(!details.useFileDatabase
        ? [{ label: "Create database", value: "createDb" as const, hint: yesNo(draft.createDb) }]
        : []),
      ...(!details.useFileDatabase && draft.createDb
        ? [{
          label: "Database name",
          value: "databaseName" as const,
          hint: draft.databaseName || "auto",
        }]
        : []),
      { label: "Access logs", value: "accessLog", hint: yesNo(draft.accessLog) },
      ...(!details.useFileDatabase
        ? [{
          label: "Render & apply",
          value: "noApply" as const,
          hint: draft.noApply ? "skip" : "yes",
        }]
        : []),
    ],
    { cancelLabel: "Back to review", quitValue: "cancel" },
  );
}

async function editAppCreateField(
  ui: WizardUI,
  state: DesiredState,
  draft: AppCreateDraft,
  field: AppCreateField,
  render = true,
): Promise<"next" | "back" | "cancel"> {
  if (render) {
    const step = APP_CREATE_STEP_FIELDS.findIndex((fields) => fields.includes(field));
    renderAppCreateScreen(ui, state, draft, step);
  }

  if (
    field === "slug" || field === "domain" || field === "aliasRaw" ||
    field === "docroot" || field === "databaseName"
  ) {
    const prompt = appCreatePrompt(field, draft);
    const value = await ui.prompt(prompt.label, prompt.options);
    if (value === null) return "back";
    draft[field] = value;
    return "next";
  }

  let selected: string | boolean | null;
  const navigation = { cancelLabel: "Back", quitValue: "__cancel" as const };
  if (field === "php") {
    selected = await ui.menu("PHP version", appCreatePhpChoices(state), {
      ...navigation,
      initialValue: draft.php,
    });
  } else if (field === "fpm") {
    selected = await ui.menu("FPM profile", appCreateFpmChoices(state), {
      ...navigation,
      initialValue: draft.fpm,
    });
  } else if (field === "entry") {
    selected = await ui.menu("Entrypoint mode", [
      { label: "Front controller (recommended)", value: "front-controller" },
      { label: "Legacy (direct PHP file execution)", value: "legacy" },
      { label: "Keep existing / default", value: "" },
    ], { ...navigation, initialValue: draft.entry });
  } else if (field === "databaseSelection") {
    selected = await ui.menu("Database", appCreateDatabaseChoices(state, draft), {
      ...navigation,
      initialValue: draft.databaseSelection,
    });
  } else if (field === "createDb") {
    selected = await ui.menu<boolean | "__cancel">("Create a namespaced database for this app?", [
      { label: "No", value: false },
      { label: "Yes", value: true },
    ], { ...navigation, initialValue: draft.createDb });
  } else if (field === "accessLog") {
    selected = await ui.menu<boolean | "__cancel">("Enable per-app access logs?", [
      { label: "No", value: false },
      { label: "Yes", value: true },
    ], { ...navigation, initialValue: draft.accessLog });
  } else {
    selected = await ui.menu<boolean | "__cancel">("Render & apply after save?", [
      { label: "Yes", value: false },
      { label: "Skip", value: true },
    ], { ...navigation, initialValue: draft.noApply });
  }

  if (selected === null) return "back";
  if (selected === "__cancel") return "cancel";
  if (field === "php") draft.php = String(selected);
  else if (field === "fpm") draft.fpm = String(selected);
  else if (field === "entry") draft.entry = selected as AppCreateDraft["entry"];
  else if (field === "databaseSelection") {
    draft.databaseSelection = String(selected);
    if (appCreateDatabaseDetails(state, draft).useFileDatabase) {
      draft.createDb = false;
      draft.databaseName = "";
      draft.noApply = false;
    }
  } else if (field === "createDb") draft.createDb = selected as boolean;
  else if (field === "accessLog") draft.accessLog = selected as boolean;
  else draft.noApply = selected as boolean;
  return "next";
}

function appCreatePrompt(
  field: "slug" | "domain" | "aliasRaw" | "docroot" | "databaseName",
  draft: AppCreateDraft,
): { label: string; options: Parameters<WizardUI["prompt"]>[1] } {
  if (field === "slug") {
    return {
      label: "App slug",
      options: {
        required: true,
        default: draft.slug || undefined,
        format: "2-32 lowercase letters, digits, or hyphens; start with a letter",
        validate: fieldValidator(parseAppSlug),
      },
    };
  }
  if (field === "domain") {
    return {
      label: "Primary domain",
      options: {
        required: true,
        default: draft.domain || undefined,
        format: "a DNS name such as app.example.com, localhost, or a local label",
        validate: fieldValidator(parseDomainName),
      },
    };
  }
  if (field === "aliasRaw") {
    return {
      label: "Domain aliases (comma-separated)",
      options: {
        default: draft.aliasRaw,
        format: "comma-separated DNS names, or blank",
        validate: validateDomainAliases,
      },
    };
  }
  if (field === "docroot") {
    return {
      label: "Document root (relative to app home)",
      options: {
        required: true,
        default: draft.docroot,
        format: "a relative path without '..', for example public",
        validate: fieldValidator(parseSafeRelativePath),
      },
    };
  }
  return {
    label: "Database name (blank = auto)",
    options: {
      default: draft.databaseName,
      format: `blank, ${draft.slug}, or a name beginning ${draft.slug}_`,
      validate: (value) => validateAppDatabaseName(value, draft.slug),
    },
  };
}

function appCreatePhpChoices(state: DesiredState): MenuChoice<string>[] {
  return [
    ...state.phpVersions.map((version) => ({
      label: version.version,
      value: version.version,
      hint: version.version === state.defaults.phpVersion ? "default" : version.image,
    })),
    { label: `Use default (${state.defaults.phpVersion})`, value: "" },
  ];
}

function appCreateFpmChoices(state: DesiredState): MenuChoice<string>[] {
  return [
    ...Object.keys(FPM_PROFILES).map((profile) => ({
      label: profile,
      value: profile,
      hint: profile === state.defaults.fpmProfile ? "default" : undefined,
    })),
    { label: `Use default (${state.defaults.fpmProfile})`, value: "" },
  ];
}

function appCreateDatabaseChoices(
  state: DesiredState,
  draft: AppCreateDraft,
): MenuChoice<string>[] {
  const existing = state.apps[draft.slug];
  const choices: MenuChoice<string>[] = state.databaseServices.map((service) => ({
    label: `${service.engine}: ${service.version}`,
    value: `${service.engine}:${service.service}`,
    hint: existing?.database.engine !== "sqlite" &&
        existing?.database.engine !== "litestream" &&
        existing?.database.service === service.service
      ? "current"
      : state.defaults.database.service === service.service
      ? "default"
      : service.service,
  }));
  choices.push({
    label: "SQLite",
    value: "sqlite",
    hint: existing?.database.engine === "sqlite" ? "current" : "local file · weekly VACUUM",
  });
  choices.push({
    label: "Litestream",
    value: "litestream",
    hint: existing?.database.engine === "litestream"
      ? "current"
      : "SQLite · continuous S3 replication",
  });
  if (existing) {
    choices.unshift({
      label: existing.database.engine === "sqlite" || existing.database.engine === "litestream"
        ? `Keep current (${existing.database.engine})`
        : `Keep current (${existing.database.engine}: ${existing.database.service})`,
      value: "",
    });
  } else {
    choices.unshift({
      label: `Use default (${state.defaults.database.engine}: ${state.defaults.database.version})`,
      value: "",
    });
  }
  return choices;
}

function appCreateDatabaseDetails(state: DesiredState, draft: AppCreateDraft) {
  const existing = state.apps[draft.slug];
  const [rawEngine, databaseService] = draft.databaseSelection
    ? draft.databaseSelection.split(":", 2)
    : [undefined, undefined];
  const databaseEngine = rawEngine as DatabaseEngine | undefined;
  const effectiveDatabaseEngine = (databaseEngine ?? existing?.database.engine ??
    state.defaults.database.engine) as DatabaseEngine;
  const useSqlite = effectiveDatabaseEngine === "sqlite";
  const useLitestream = effectiveDatabaseEngine === "litestream";
  return {
    databaseEngine,
    databaseService,
    effectiveDatabaseEngine,
    useSqlite,
    useLitestream,
    useFileDatabase: useSqlite || useLitestream,
  };
}

function renderAppCreateScreen(
  ui: WizardUI,
  state: DesiredState,
  draft: AppCreateDraft,
  step: number,
): void {
  ui.clear();
  ui.header("Apps › Create", `Step ${step + 1} of 5 · ${APP_CREATE_STEP_NAMES[step]}`);
  ui.table(["field", "value"], appCreateSummaryRows(state, draft, step));
  ui.blank();
  ui.info(
    step === 4 ? "Esc Back · Enter Select · q Cancel" : "Esc Previous field · Enter Continue",
  );
  ui.blank();
}

function appCreateSummaryRows(
  state: DesiredState,
  draft: AppCreateDraft,
  step?: number,
): string[][] {
  const details = appCreateDatabaseDetails(state, draft);
  const database = `${
    details.useFileDatabase
      ? details.effectiveDatabaseEngine
      : draft.databaseSelection || `default (${details.effectiveDatabaseEngine})`
  }${draft.createDb ? ` · create ${draft.databaseName || "auto"}` : ""}`;
  const groups: string[][][] = [
    [
      ["App slug", draft.slug || "—"],
      ["Primary domain", draft.domain || "—"],
      ["Domain aliases", parseAliases(draft.aliasRaw).join(", ") || "—"],
    ],
    [
      ["PHP version", draft.php || `(default ${state.defaults.phpVersion})`],
      ["FPM profile", draft.fpm || `(default ${state.defaults.fpmProfile})`],
      ["Document root", draft.docroot],
      ["Entrypoint", draft.entry || "default"],
    ],
    [
      ["Database", database],
      ...(details.useLitestream ? [["SQLite backup", "Litestream · enabled"]] : []),
      ...(details.useSqlite ? [["Maintenance", "weekly VACUUM · runner Supercronic"]] : []),
    ],
    [
      ["Access logs", yesNo(draft.accessLog)],
      ["Render & apply", draft.noApply ? "skip" : "yes"],
    ],
  ];
  return step === undefined || step === 4 ? groups.flat() : groups[step] ?? [];
}

function parseAliases(raw: string): string[] {
  return raw.split(",").map((value) => value.trim()).filter(Boolean);
}

function validateDomainAliases(raw: string): string | null {
  for (const [index, alias] of parseAliases(raw).entries()) {
    const result = parseDomainName(alias, `alias ${index + 1}`);
    if (!result.ok) return result.errors.join("; ");
  }
  return null;
}

function validateAppDatabaseName(value: string, slug: string): string | null {
  if (value.trim() === "") return null;
  if (!/^[a-zA-Z0-9_]+$/.test(value)) {
    return "must contain only letters, digits, or underscores";
  }
  if (value !== slug && !value.startsWith(`${slug}_`)) {
    return `must be ${slug} or begin with ${slug}_`;
  }
  return null;
}

function yesNo(value: boolean): string {
  return value ? "yes" : "no";
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
    ui.header(
      `Databases: ${slug}`,
      `${app.databases.length} binding${
        app.databases.length === 1 ? "" : "s"
      } · multiple kinds allowed`,
    );
    ui.table(
      ["kind", "service / files", "databases"],
      appDatabaseSummaryRows(app),
    );
    ui.blank();
    const action = await ui.menu("Select a binding", [
      ...appDatabaseMenuChoices(app),
      {
        label: "Add database binding…",
        value: "__add",
        hint: "MySQL · PostgreSQL · SQLite · Litestream",
      },
    ]);
    if (!action) return;

    if (action === "__add") {
      await wizardAddAppDatabaseBinding(ui, ctx, slug);
      continue;
    }
    if (action === SQLITE_DATABASE_GROUP) {
      await sectionAppSqliteDatabases(ui, ctx, slug);
      continue;
    }

    const binding = app.databases.find((entry) => databaseBindingKey(entry) === action);
    if (binding) await sectionAppDatabaseBinding(ui, ctx, slug, binding);
  }
}

const SQLITE_DATABASE_GROUP = "__sqlite";

export function appDatabaseMenuChoices(app: AppState): MenuChoice<string>[] {
  const choices: MenuChoice<string>[] = [];
  const sqliteBindings = app.databases.filter(isSqliteBinding);
  let sqliteGroupAdded = false;

  for (const binding of app.databases) {
    if (isSqliteBinding(binding)) {
      if (!sqliteGroupAdded) {
        choices.push({
          label: "SQLite",
          value: SQLITE_DATABASE_GROUP,
          hint: sqliteBindingSummary(sqliteBindings),
        });
        sqliteGroupAdded = true;
      }
      continue;
    }
    choices.push({
      label: databaseBindingLabel(binding),
      value: databaseBindingKey(binding),
      hint: `${binding.databases.length} logical database${
        binding.databases.length === 1 ? "" : "s"
      }`,
    });
  }

  return choices;
}

function databaseBindingKey(binding: AppDatabaseBinding): string {
  return binding.engine === "mysql" || binding.engine === "postgres"
    ? `${binding.engine}:${binding.service}`
    : `${binding.engine}:${binding.file.id}`;
}

function databaseBindingLabel(binding: AppDatabaseBinding): string {
  if (binding.engine === "mysql") return `MySQL · ${binding.service}`;
  if (binding.engine === "postgres") return `PostgreSQL · ${binding.service}`;
  return `${binding.engine === "litestream" ? "Litestream" : "SQLite"} · ${binding.file.id}`;
}

function appDatabaseSummaryRows(app: AppState): string[][] {
  const rows: string[][] = [];
  const sqliteBindings = app.databases.filter(isSqliteBinding);
  let sqliteGroupAdded = false;

  for (const binding of app.databases) {
    if (isSqliteBinding(binding)) {
      if (!sqliteGroupAdded) {
        rows.push([
          "SQLite",
          sqliteBindingCount(sqliteBindings),
          sqliteModeSummary(sqliteBindings),
        ]);
        sqliteGroupAdded = true;
      }
      continue;
    }
    rows.push([
      binding.engine === "postgres" ? "PostgreSQL" : "MySQL",
      binding.service,
      binding.databases.map((database) => database.name).join(", ") || "-",
    ]);
  }

  return rows;
}

type SqliteBinding = Extract<AppDatabaseBinding, { engine: "sqlite" | "litestream" }>;

function isSqliteBinding(binding: AppDatabaseBinding): binding is SqliteBinding {
  return binding.engine === "sqlite" || binding.engine === "litestream";
}

function sqliteBindingCount(bindings: SqliteBinding[]): string {
  return `${bindings.length} file${bindings.length === 1 ? "" : "s"}`;
}

function sqliteModeSummary(bindings: SqliteBinding[]): string {
  const local = bindings.filter((binding) => binding.engine === "sqlite").length;
  const replicated = bindings.length - local;
  return [
    ...(local > 0 ? [`${local} local`] : []),
    ...(replicated > 0 ? [`${replicated} Litestream`] : []),
  ].join(" · ");
}

function sqliteBindingSummary(bindings: SqliteBinding[]): string {
  return `${sqliteBindingCount(bindings)} · ${sqliteModeSummary(bindings)}`;
}

export function sqliteDatabaseMenuChoices(
  bindings: SqliteBinding[],
): MenuChoice<string>[] {
  return bindings.map((binding) => ({
    label: `${binding.engine === "litestream" ? "Litestream" : "Local"} · ${binding.file.id}`,
    value: databaseBindingKey(binding),
    hint: binding.file.path,
  }));
}

async function sectionAppSqliteDatabases(
  ui: WizardUI,
  ctx: CliContext,
  slug: string,
): Promise<void> {
  while (true) {
    const state = await ctx.store.load();
    const app = state.apps[slug];
    if (!app) return;
    const bindings = app.databases.filter(isSqliteBinding);
    if (bindings.length === 0) return;

    ui.clear();
    ui.header(`SQLite: ${slug}`, sqliteBindingSummary(bindings));
    const rows = await Promise.all(
      bindings.map(async (binding) => [
        binding.engine === "litestream" ? "Litestream" : "Local",
        sqliteContainerPath(binding.file.id, app.slug, binding.engine),
        binding.engine === "litestream"
          ? state.sqliteBackup?.enabled ? "continuous S3" : "disabled"
          : "weekly VACUUM + logical .backup",
        await sqliteFileSize(ctx.platform, binding.file.id, app.slug, binding.engine),
      ]),
    );
    ui.table(["mode", "file", "backup", "size"], rows);
    ui.blank();
    const action = await ui.menu("Select a SQLite file", sqliteDatabaseMenuChoices(bindings));
    if (!action) return;

    const binding = bindings.find((entry) => databaseBindingKey(entry) === action);
    if (binding) await sectionAppDatabaseBinding(ui, ctx, slug, binding);
  }
}

async function sectionAppDatabaseBinding(
  ui: WizardUI,
  ctx: CliContext,
  slug: string,
  selected: AppDatabaseBinding,
): Promise<void> {
  while (true) {
    const state = await ctx.store.load();
    const app = state.apps[slug];
    if (!app) return;
    const binding = app.databases.find((entry) =>
      databaseBindingKey(entry) === databaseBindingKey(selected)
    );
    if (!binding) return;

    ui.clear();
    if (binding.engine === "sqlite" || binding.engine === "litestream") {
      ui.header(
        `${binding.engine === "litestream" ? "Litestream" : "SQLite"}: ${slug}`,
        binding.file.id,
      );
      ui.table(
        ["field", "value"],
        [
          ["file", sqliteContainerPath(binding.file.id, app.slug, binding.engine)],
          ["created", binding.file.createdAt],
          [
            "backup",
            binding.engine === "litestream"
              ? state.sqliteBackup?.enabled ? "continuous S3" : "disabled"
              : "logical .backup via bento backup",
          ],
          ...(binding.engine === "litestream"
            ? [["verified", binding.backupVerifiedAt ?? "never"]]
            : [["maintenance", "weekly VACUUM · randomized 00:00–04:59 local"]]),
        ],
      );
      await ui.pause();
      return;
    }

    ui.header(
      `${binding.engine === "postgres" ? "PostgreSQL" : "MySQL"}: ${slug}`,
      `${binding.service} · ${binding.databases.length} database${
        binding.databases.length === 1 ? "" : "s"
      }`,
    );
    ui.table(
      ["database", "created"],
      binding.databases.map((database) => [database.name, database.createdAt]),
    );
    ui.blank();
    const action = await ui.menu("Binding actions", [
      { label: "Create logical database", value: "create" },
      {
        label: binding.engine === "postgres" ? "Open PostgreSQL shell" : "Open MySQL shell",
        value: "shell",
        hint: `${binding.service} · as app ${slug}`,
      },
    ]);
    if (!action) return;

    try {
      if (action === "create") {
        const dbName = await ui.prompt("Database name", {
          required: true,
          default: slug,
          format: `${slug} or a name beginning ${slug}_; letters, digits, and underscores only`,
          validate: (value) => validateAppDatabaseName(value, slug),
        });
        if (!dbName) continue;
        await ctx.store.withExclusive(async (currentState) => {
          const next = binding.engine === "postgres"
            ? await createPostgresAppDatabaseLive(
              ctx.platform,
              currentState,
              slug,
              dbName,
              await requirePostgresRootPassword(ctx.platform),
              binding.service,
            )
            : await createAppDatabaseLive(
              ctx.platform,
              currentState,
              slug,
              dbName,
              await requireMysqlRootPassword(ctx.platform),
              binding.service,
            );
          const nextApp = next.apps[slug]!;
          const redisShared = await loadRedisPassword(ctx.platform);
          await materializeAppHome(ctx.platform, nextApp, {
            recursivePerms: false,
            redisSharedPassword: redisShared,
          });
          await ctx.store.save(next);
          return next;
        });
        ui.success(`Created database ${dbName}`, `${binding.engine} · ${binding.service}`);
      } else if (binding.engine === "postgres") {
        await openPostgresShell(
          ui,
          ctx,
          buildPostgresShellPlan(ctx.platform, { kind: "app", app }, {
            service: binding.service,
          }),
          `bento postgres shell --app ${slug}`,
        );
      } else {
        await openMysqlShell(
          ui,
          ctx,
          buildMysqlShellPlan(ctx.platform, { kind: "app", app }, {
            service: binding.service,
          }),
          `bento mysql shell --app ${slug}`,
        );
      }
    } catch (err) {
      handleError(ui, err);
    }
    await ui.pause();
  }
}

async function wizardAddAppDatabaseBinding(
  ui: WizardUI,
  ctx: CliContext,
  slug: string,
): Promise<void> {
  const state = await ctx.store.load();
  const app = state.apps[slug];
  if (!app) return;

  const existing = new Set(app.databases.map(databaseBindingKey));
  const choices: MenuChoice<string>[] = state.databaseServices
    .filter((service) => !existing.has(`${service.engine}:${service.service}`))
    .map((service) => ({
      label: `${service.engine === "postgres" ? "PostgreSQL" : "MySQL"} · ${service.version}`,
      value: `${service.engine}:${service.service}`,
      hint: service.service,
    }));
  choices.push(
    { label: "SQLite", value: "sqlite", hint: "new local database file · weekly VACUUM" },
    {
      label: "Litestream",
      value: "litestream",
      hint: "new SQLite file · continuous S3 replication",
    },
  );

  const selection = await ui.menu("Add database binding", choices);
  if (!selection) return;
  const [engine, service] = selection.split(":", 2) as [
    "mysql" | "postgres" | "sqlite" | "litestream",
    string | undefined,
  ];
  const fileDatabase = engine === "sqlite" || engine === "litestream";
  const createDatabase = fileDatabase ||
    await ui.confirm("Create an initial namespaced logical database?", { defaultYes: true });
  const databaseName = !fileDatabase && createDatabase
    ? await ui.prompt("Database name (blank = app slug)", {
      default: "",
      format: `${slug} or a name beginning ${slug}_; letters, digits, and underscores only`,
      validate: (value) => validateAppDatabaseName(value, slug),
    })
    : "";
  if (databaseName === null) return;

  const target = service ? `${engine} · ${service}` : engine;
  if (!(await ui.confirm(`Add ${target} to ${slug}?`, { defaultYes: true }))) return;

  try {
    const result = await ctx.store.withExclusive(async (currentState) => {
      const current = currentState.apps[slug];
      if (!current) throw new Error(`app not found: ${slug}`);
      const provisioned = provisionApp(ctx.platform, currentState, {
        slug,
        domain: current.mainDomain,
        aliases: current.aliases,
        databaseEngine: engine,
        mysqlVersion: engine === "mysql" ? service : undefined,
        postgresVersion: engine === "postgres" ? service : undefined,
        createDatabase,
        databaseName: databaseName || undefined,
      });
      const plane = await applyAppDataPlane(ctx.platform, provisioned.app, {
        explicitDatabase: createDatabase && !fileDatabase,
        databaseEngine: engine,
        databaseService: service,
        databaseName: databaseName || (createDatabase && !fileDatabase ? slug : undefined),
      });
      const redisShared = await loadRedisPassword(ctx.platform);
      await materializeAppHome(ctx.platform, provisioned.app, {
        recursivePerms: false,
        redisSharedPassword: redisShared,
      });

      let nextState = provisioned.state;
      let sqliteBackupEnabledNow = false;
      if (engine === "litestream" && !nextState.sqliteBackup?.enabled) {
        nextState = await enableSqliteBackup(ctx.platform, nextState, slug);
        sqliteBackupEnabledNow = true;
      }
      await ctx.store.save(nextState);
      await ctx.render.apply(nextState, {
        reloadPlan: provisioned.reloadPlan,
        skipValidate: false,
        alreadyLocked: true,
      });
      return { state: nextState, plane, sqliteBackupEnabledNow };
    });

    if (engine === "litestream" && result.sqliteBackupEnabledNow) {
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
    ui.success(`Added ${target}`, `app=${slug}`);
    for (const note of result.plane.deferredNotes) ui.warn(note);
  } catch (err) {
    handleError(ui, err);
  }
  await ui.pause();
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
          format: "a DNS name such as app.example.com, localhost, or a local label",
          validate: fieldValidator(parseDomainName),
        });
        if (!domain) continue;
        const aliasesRaw = await ui.prompt("Aliases (comma-separated)", {
          default: app.aliases.join(","),
          format: "comma-separated DNS names, or blank",
          validate: validateDomainAliases,
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
          const cert = await ui.prompt("Certificate path", {
            required: true,
            format: "an absolute host path",
            validate: fieldValidator(parseAbsolutePath),
          });
          if (!cert) continue;
          const key = await ui.prompt("Private key path", {
            required: true,
            format: "an absolute host path",
            validate: fieldValidator(parseAbsolutePath),
          });
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
