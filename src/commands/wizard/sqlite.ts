import { join } from "@std/path";
import { databaseBindings, type DesiredState } from "../../domain/state.ts";
import { runDatabaseBackup } from "../../services/database_backup.ts";
import {
  enableSqliteBackup,
  exportSqliteBackup,
  getSqliteBackupStatus,
  requireSqliteApp,
  sqliteCompose,
  syncSqliteBackup,
  verifySqliteBackup,
} from "../../services/sqlite.ts";
import { sqliteContainerPath } from "../../services/sqlite_paths.ts";
import { WizardUI } from "../../ui/tui.ts";
import type { CliContext } from "../context.ts";
import { ensureState, handleError, pcDim, sqliteFileSize } from "./shared.ts";

export async function sectionSqlite(ui: WizardUI, ctx: CliContext): Promise<void> {
  ui.header("Manage SQLite");
  if (!(await ensureState(ui, ctx))) return;

  while (true) {
    const state = await ctx.store.load();
    const targets = await listSqliteTargets(ctx.platform, state);
    const localTargets = targets.filter((target) => target.engine === "sqlite");
    const litestreamTargets = targets.filter((target) => target.engine === "litestream");
    const litestreamApps = Object.values(state.apps)
      .filter((app) => databaseBindings(app, "litestream").length > 0)
      .sort((a, b) => a.slug.localeCompare(b.slug));

    ui.clear();
    ui.header(
      "Manage SQLite",
      `${localTargets.length} local file${
        localTargets.length === 1 ? "" : "s"
      } · ${litestreamApps.length} Litestream app${litestreamApps.length === 1 ? "" : "s"} · ${
        state.sqliteBackup?.enabled ? "continuous backup enabled" : "continuous backup disabled"
      }`,
    );
    ui.table(
      ["mode", "app", "file", "backup", "size"],
      [
        ...localTargets.map((target) => [
          "Local",
          target.slug,
          target.containerPath,
          target.backup,
          target.size,
        ]),
        ...litestreamTargets.map((target) => [
          "Litestream",
          target.slug,
          target.containerPath,
          target.backup,
          target.size,
        ]),
      ],
    );
    ui.blank();

    const hasLitestream = litestreamApps.length > 0;
    const action = await ui.menu("SQLite actions", [
      {
        label: "Back up local SQLite",
        value: "local",
        hint: "online .backup · Zstandard or gzip",
        disabled: localTargets.length === 0,
      },
      {
        label: state.sqliteBackup?.enabled
          ? "Reconfigure continuous backup"
          : "Enable continuous backup",
        value: "enable",
        hint: "stack-wide Litestream replication to S3",
        disabled: !hasLitestream,
      },
      { label: "Replication status", value: "status", disabled: !hasLitestream },
      { label: "Force S3 sync", value: "sync", disabled: !hasLitestream },
      {
        label: "Verify S3 restore",
        value: "verify",
        hint: "temporary full integrity check",
        disabled: !hasLitestream,
      },
      {
        label: "Export from S3 to database file",
        value: "export",
        hint: "safe restore-to-new",
        disabled: !hasLitestream,
      },
    ]);
    if (!action) return;

    if (action === "local") {
      const target = await ui.menu<SqliteTarget>(
        "Local SQLite database",
        localTargets.map((entry) => ({
          label: `${entry.slug} · ${entry.fileId}`,
          value: entry,
          hint: entry.containerPath,
        })),
      );
      if (!target) continue;
      try {
        await wizardLocalBackup(ui, ctx, state, target);
      } catch (err) {
        handleError(ui, err);
      }
      await ui.pause();
      continue;
    }

    const slug = await ui.menu(
      "SQLite application",
      litestreamApps.map((app) => ({ label: app.slug, value: app.slug })),
    );
    if (!slug) continue;

    try {
      if (action === "enable") {
        await wizardEnable(ui, ctx, slug);
      } else if (action === "status") {
        const status = await getSqliteBackupStatus(ctx.platform, state, slug);
        ui.table(
          ["field", "value"],
          Object.entries(status).map(([key, value]) => [key, String(value ?? "never")]),
        );
      } else if (action === "sync") {
        ui.message(pcDim(`scriptable: bento sqlite backup sync --app ${slug}`));
        const detail = await syncSqliteBackup(ctx.platform, state, slug);
        ui.success(`Remote sync confirmed for ${slug}`, detail || undefined);
      } else if (action === "verify") {
        ui.message(pcDim(`scriptable: bento sqlite backup verify --app ${slug}`));
        const detail = await verifySqliteBackup(ctx.platform, state, slug);
        await recordVerification(ctx, slug);
        ui.success(`Full restore verification succeeded for ${slug}`, detail || undefined);
      } else {
        const defaultOutput = join(ctx.stackRoot, "backups", "sqlite", `${slug}-s3-export.sqlite`);
        const output = await ui.prompt("New database file", {
          default: defaultOutput,
          required: true,
        });
        if (!output) continue;
        ui.warn(
          "The destination must not already exist",
          "The live application database is never replaced.",
        );
        ui.message(
          pcDim(`scriptable: bento sqlite backup export --app ${slug} --output ${output}`),
        );
        if (!(await ui.confirm("Export the S3 replica?", { defaultYes: true }))) continue;
        const exported = await exportSqliteBackup(ctx.platform, state, slug, output);
        ui.success("SQLite replica exported", exported);
      }
    } catch (err) {
      handleError(ui, err);
    }
    await ui.pause();
  }
}

type SqliteTarget = {
  slug: string;
  fileId: string;
  engine: "sqlite" | "litestream";
  containerPath: string;
  backup: string;
  size: string;
};

type UnsizedSqliteTarget = Omit<SqliteTarget, "size">;

async function listSqliteTargets(
  platform: CliContext["platform"],
  state: DesiredState,
): Promise<SqliteTarget[]> {
  const targets: UnsizedSqliteTarget[] = Object.values(state.apps)
    .flatMap((app) => [
      ...databaseBindings(app, "sqlite").map((database) => ({
        slug: app.slug,
        fileId: database.file.id,
        engine: "sqlite" as const,
        containerPath: sqliteContainerPath(database.file.id, app.slug, "sqlite"),
        backup: "logical .backup",
      })),
      ...databaseBindings(app, "litestream").map((database) => ({
        slug: app.slug,
        fileId: database.file.id,
        engine: "litestream" as const,
        containerPath: sqliteContainerPath(database.file.id, app.slug, "litestream"),
        backup: database.backupVerifiedAt ?? "never",
      })),
    ])
    .sort((a, b) => a.slug.localeCompare(b.slug) || a.fileId.localeCompare(b.fileId));

  return await Promise.all(
    targets.map(async (target) => ({
      ...target,
      size: await sqliteFileSize(platform, target.fileId, target.slug, target.engine),
    })),
  );
}

async function wizardLocalBackup(
  ui: WizardUI,
  ctx: CliContext,
  state: DesiredState,
  target: SqliteTarget,
): Promise<void> {
  const compress = await ui.menu<"zstd" | "gzip" | "none">("Compression", [
    { label: "Zstandard", value: "zstd", hint: "recommended" },
    { label: "gzip", value: "gzip" },
    { label: "None", value: "none" },
  ]);
  if (!compress) return;

  const flag = compress === "gzip" ? " --gzip" : compress === "none" ? " --none" : "";
  ui.message(
    pcDim(
      `scriptable: bento sqlite backup local ${target.slug} --file ${target.fileId}${flag}`,
    ),
  );
  ui.warn(
    "A consistent online SQLite copy will be written under the stack backups directory",
    "The live database is not replaced.",
  );
  if (!(await ui.confirm("Start local SQLite backup?", { defaultYes: true }))) return;

  const artifacts = await runDatabaseBackup(ctx.platform, state, {
    scope: "database",
    slug: target.slug,
    database: target.fileId,
    compress,
  });
  ui.success("Local SQLite backup completed");
  ui.table(
    ["file", "bytes", "path"],
    artifacts.map((artifact) => [artifact.database, String(artifact.bytes), artifact.path]),
  );
}

async function wizardEnable(ui: WizardUI, ctx: CliContext, slug: string): Promise<void> {
  ui.message(pcDim(`scriptable: bento sqlite backup enable ${slug}`));
  ui.warn(
    "This enables or updates the stack-wide SQLite watcher",
    "All managed SQLite files are covered.",
  );
  if (
    !(await ui.confirm("Enable Litestream replication and verify S3 restore?", {
      defaultYes: true,
    }))
  ) {
    return;
  }

  const next = await ctx.store.withExclusive(async (current) => {
    const changed = await enableSqliteBackup(ctx.platform, current, slug);
    await ctx.store.save(changed);
    await ctx.render.apply(changed, { alreadyLocked: true, skipValidate: false });
    return changed;
  });
  const up = await sqliteCompose(ctx.platform, next, [
    "up",
    "-d",
    "--force-recreate",
    "litestream",
  ]);
  if (up.code !== 0) throw new Error(`Litestream container failed to start: ${up.stderr.trim()}`);
  const detail = await verifySqliteBackup(ctx.platform, next, slug);
  await recordVerification(ctx, slug);
  ui.success("SQLite continuous backup enabled and verified", detail || slug);
}

async function recordVerification(ctx: CliContext, slug: string): Promise<void> {
  await ctx.store.withExclusive(async (current) => {
    const { database } = requireSqliteApp(current, slug);
    database.backupVerifiedAt = ctx.platform.clock.nowIso();
    await ctx.store.save(current);
  });
}
