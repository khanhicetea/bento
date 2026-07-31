import {
  addPostgresVersion,
  buildPostgresShellPlan,
  listPostgresVersions,
  queryPostgresActivity,
  queryPostgresDatabaseSizes,
  resolvePostgresServices,
} from "../../services/postgres.ts";
import { parsePostgresVersion } from "../../schemas/validators.ts";
import { requirePostgresRootPassword } from "../../services/stack_env.ts";
import { WizardUI } from "../../ui/tui.ts";
import type { CliContext } from "../context.ts";
import { ensureState, fieldValidator, handleError, openPostgresShell } from "./shared.ts";
import { wizardDatabaseBackup, wizardDatabaseRestore } from "./mysql.ts";

export async function sectionPostgres(ui: WizardUI, ctx: CliContext): Promise<void> {
  ui.header("Manage PostgreSQL");
  if (!(await ensureState(ui, ctx))) return;

  while (true) {
    const state = await ctx.store.load();
    const versions = listPostgresVersions(state);
    ui.clear();
    ui.header(
      "Manage PostgreSQL",
      `${versions.length} managed version${versions.length === 1 ? "" : "s"}`,
    );
    ui.table(
      ["version", "service", "image"],
      versions.map((entry) => [entry.version, entry.service, entry.image]),
    );
    ui.blank();
    const action = await ui.menu("PostgreSQL actions", [
      { label: "Open shell", value: "shell", disabled: versions.length === 0 },
      { label: "Add version", value: "add", hint: "official major tag, e.g. 17" },
      { label: "Database sizes", value: "size", disabled: versions.length === 0 },
      { label: "Active processes", value: "processlist", disabled: versions.length === 0 },
      { label: "Logical backup", value: "backup", hint: "database · app · mixed-engine all" },
      { label: "Logical restore", value: "restore", hint: "recent backup or file" },
    ]);
    if (!action) return;

    try {
      if (action === "add") {
        const version = await ui.prompt("PostgreSQL major version", {
          required: true,
          format: "an official major-only tag, for example 17",
          validate: fieldValidator(parsePostgresVersion),
        });
        if (!version) continue;
        await ctx.store.withExclusive(async (current) => {
          const next = addPostgresVersion(current, version);
          await ctx.store.save(next);
          await ctx.render.apply(next, { skipValidate: true, alreadyLocked: true });
          return next;
        });
        ui.success(`Added PostgreSQL ${version}`);
      } else if (action === "shell") {
        const services = resolvePostgresServices(state);
        const service = services.length === 1 ? services[0]! : await ui.menu(
          "PostgreSQL service",
          services.map((value) => ({ label: value, value })),
        );
        if (!service) continue;
        await openPostgresShell(
          ui,
          ctx,
          buildPostgresShellPlan(ctx.platform, { kind: "root", service }),
          `bento postgres shell --root --service ${service}`,
        );
      } else if (action === "backup") {
        await wizardDatabaseBackup(ui, ctx);
      } else if (action === "restore") {
        await wizardDatabaseRestore(ui, ctx);
      } else {
        const password = await requirePostgresRootPassword(ctx.platform);
        const rows: string[][] = [];
        for (const service of resolvePostgresServices(state)) {
          if (action === "size") {
            for (const row of await queryPostgresDatabaseSizes(ctx.platform, service, password)) {
              rows.push([service, row.database, row.bytes, row.size]);
            }
          } else {
            for (const row of await queryPostgresActivity(ctx.platform, service, password)) {
              rows.push([service, row.pid, row.user, row.database, row.state, row.client]);
            }
          }
        }
        ui.table(
          action === "size"
            ? ["service", "database", "bytes", "size"]
            : ["service", "pid", "user", "database", "state", "client"],
          rows,
        );
      }
    } catch (err) {
      handleError(ui, err);
    }
    await ui.pause();
  }
}
