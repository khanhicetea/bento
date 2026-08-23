import { implement } from "@orpc/server";
import { dataContract, type DataOverview } from "@bento/shared";
import type { CliContext } from "../../../commands/context.ts";
import { redact } from "../../../ui/output.ts";

const os = implement(dataContract);

export function createDataRouter(ctx: CliContext) {
  return os.router({ overview: os.overview.handler(async () => await dataOverview(ctx)) });
}

async function dataOverview(ctx: CliContext): Promise<DataOverview> {
  if (!(await ctx.store.exists())) return empty(ctx.stackRoot);
  try {
    const state = await ctx.store.load();
    const bindings = Object.values(state.apps)
      .sort((left, right) => left.slug.localeCompare(right.slug))
      .flatMap((app) =>
        app.databases.map((binding, index) => ({
          app: String(app.slug),
          primary: index === 0,
          engine: binding.engine,
          service:
            binding.engine === "sqlite"
              ? "local file"
              : binding.engine === "litestream"
                ? "Litestream"
                : String(binding.service),
          resources:
            binding.engine === "sqlite" || binding.engine === "litestream"
              ? [binding.file.path]
              : binding.databases.map((database) => String(database.name)),
          ...(binding.engine === "litestream" && binding.backupVerifiedAt
            ? { backupVerifiedAt: binding.backupVerifiedAt }
            : {}),
        })),
      );
    return {
      initialized: true,
      stackRoot: ctx.stackRoot,
      services: state.databaseServices.map((service) => ({
        engine: service.engine,
        version: String(service.version),
        service: String(service.service),
        image: service.image,
        volume: service.volume,
        appCount: new Set(
          bindings
            .filter(
              (binding) => binding.engine === service.engine && binding.service === service.service,
            )
            .map((binding) => binding.app),
        ).size,
      })),
      bindings,
      ...(state.sqliteBackup
        ? {
            sqliteBackup: {
              enabled: state.sqliteBackup.enabled,
              provider: state.sqliteBackup.provider,
              destination: state.sqliteBackup.destination,
              syncInterval: state.sqliteBackup.syncInterval,
              snapshotInterval: state.sqliteBackup.snapshotInterval,
              snapshotRetention: state.sqliteBackup.snapshotRetention,
            },
          }
        : {}),
    };
  } catch (error) {
    return empty(ctx.stackRoot, redact(error instanceof Error ? error.message : String(error)));
  }
}

function empty(stackRoot: string, error?: string): DataOverview {
  return { initialized: false, stackRoot, services: [], bindings: [], ...(error ? { error } : {}) };
}
