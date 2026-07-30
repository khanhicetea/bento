import { join } from "@std/path";
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
import { ensureState, handleError, pcDim } from "./shared.ts";

export async function sectionSqlite(ui: WizardUI, ctx: CliContext): Promise<void> {
  ui.header("Manage SQLite");
  if (!(await ensureState(ui, ctx))) return;

  while (true) {
    const state = await ctx.store.load();
    const apps = Object.values(state.apps)
      .filter((app) => app.database.engine === "litestream")
      .sort((a, b) => a.slug.localeCompare(b.slug));

    ui.clear();
    ui.header(
      "Manage SQLite",
      `${apps.length} app database${apps.length === 1 ? "" : "s"} · Litestream ${
        state.sqliteBackup?.enabled ? "enabled" : "disabled"
      }`,
    );
    ui.table(
      ["app", "file", "verified"],
      apps.map((app) => [
        app.slug,
        app.database.engine === "litestream"
          ? sqliteContainerPath(app.database.file.id, app.slug, "litestream")
          : "",
        app.database.engine === "litestream" ? app.database.backupVerifiedAt ?? "never" : "",
      ]),
    );
    ui.blank();

    const disabled = apps.length === 0;
    const action = await ui.menu("SQLite actions", [
      {
        label: state.sqliteBackup?.enabled
          ? "Reconfigure continuous backup"
          : "Enable continuous backup",
        value: "enable",
        hint: "stack-wide Litestream replication to S3",
        disabled,
      },
      { label: "Replication status", value: "status", disabled },
      { label: "Force S3 sync", value: "sync", disabled },
      {
        label: "Verify S3 restore",
        value: "verify",
        hint: "temporary full integrity check",
        disabled,
      },
      {
        label: "Export from S3 to database file",
        value: "export",
        hint: "safe restore-to-new",
        disabled,
      },
    ]);
    if (!action) return;

    const slug = await ui.menu(
      "SQLite application",
      apps.map((app) => ({ label: app.slug, value: app.slug })),
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
