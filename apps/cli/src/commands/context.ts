import { resolve } from "node:path";
import { describeReloadPlan } from "#/domain/reload.ts";
import { createPlatform, type Platform } from "#/platform/mod.ts";
import { StateStore } from "#/services/state_store.ts";
import { RenderService } from "#/services/render.ts";
import { createLogger, type Logger } from "#/ui/output.ts";
import { createApplicationUseCases, type ApplicationUseCases } from "#/use_cases/applications.ts";
import { createDataUseCases, type DataUseCases } from "#/use_cases/data.ts";
import { createOperationsUseCases, type OperationsUseCases } from "#/use_cases/operations.ts";
import { createRoutingUseCases, type RoutingUseCases } from "#/use_cases/routing.ts";

export type CliContext = {
  platform: Platform;
  store: StateStore;
  render: RenderService;
  applications: ApplicationUseCases;
  data: DataUseCases;
  operations: OperationsUseCases;
  routing: RoutingUseCases;
  log: Logger;
  stackRoot: string;
  json: boolean;
};

export type GlobalFlags = {
  stackRoot: string;
  json?: boolean;
  repoRoot?: string;
};

export function defaultStackRoot(): string {
  return Bun.env.BENTO_STACK_ROOT ?? Bun.env.BENTO_ROOT ?? "./bento";
}

export function createContext(flags: GlobalFlags): CliContext {
  const stackRoot = resolve(flags.stackRoot);
  const platform = createPlatform(stackRoot, flags.repoRoot);
  const log = createLogger({ json: flags.json });
  const store = new StateStore(platform);
  const render = new RenderService(platform, (plan) => {
    log.success("Reload plan executed", describeReloadPlan(plan).join("\n"));
  });
  const dependencies = { platform, store, render };
  return {
    ...dependencies,
    applications: createApplicationUseCases(dependencies),
    data: createDataUseCases({ platform, store }),
    operations: createOperationsUseCases(dependencies),
    routing: createRoutingUseCases(dependencies),
    log,
    stackRoot,
    json: !!flags.json,
  };
}

/** Build a CliContext from yargs-parsed global options. */
export function contextFromArgv(argv: { stack: string; json: boolean; repoRoot?: string }): CliContext {
  return createContext({
    stackRoot: argv.stack,
    json: argv.json,
    ...(argv.repoRoot ? { repoRoot: argv.repoRoot } : {}),
  });
}
