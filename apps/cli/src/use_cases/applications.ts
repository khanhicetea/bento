import { serviceError, safetyError } from "#/domain/errors.ts";
import { emptyReloadPlan } from "#/domain/reload.ts";
import { isProcessApp, type AppState, type DatabaseEngine, type DesiredState } from "#/domain/state.ts";
import type { Platform } from "#/platform/mod.ts";
import {
  applyAppDataPlane,
  deleteApp,
  getAppOrThrow,
  materializeAppHome,
  provisionApp,
  setAppEnabled,
  type AppDataPlaneResult,
  type AppLifecycleResult,
  type ProvisionAppInput,
  type ProvisionAppResult,
} from "#/services/app.ts";
import { writeAppPruneManifest } from "#/services/app_prune.ts";
import { composeArgs } from "#/services/compose.ts";
import {
  isProcessAppHealthy,
  recreateRunningProcessApp,
  removeProcessAppContainer,
  startProcessApp,
  stopProcessApp,
} from "#/services/process_app.ts";
import { RenderService } from "#/services/render.ts";
import { StateStore } from "#/services/state_store.ts";
import { loadRedisPassword } from "#/services/stack_env.ts";

export type ApplicationUseCaseDependencies = {
  platform: Platform;
  store: StateStore;
  render: RenderService;
};

export type ProvisionApplicationOptions = {
  apply?: boolean;
  skipValidate?: boolean;
};

export type ProvisionApplicationResult = ProvisionAppResult & {
  dataPlane: AppDataPlaneResult;
};

export type SetApplicationEnabledResult = AppLifecycleResult & {
  stopWarning?: string;
};

/** A durable state change succeeded, but its follow-up apply/reconcile step failed. */
export class ApplicationApplyError extends Error {
  constructor(cause: unknown) {
    super("Application settings were saved, but applying them failed", { cause });
  }
}

/**
 * Application workflows shared by CLI and web adapters.
 *
 * This layer owns lock/save/apply ordering. Adapters remain responsible only for
 * translating transport input and presenting the typed result.
 */
export function createApplicationUseCases({ platform, store, render }: ApplicationUseCaseDependencies) {
  async function provision(
    input: ProvisionAppInput,
    options: ProvisionApplicationOptions = {},
  ): Promise<ProvisionApplicationResult> {
    return await provisionResolved(() => input, options);
  }

  async function addDatabase(input: {
    slug: string;
    engine: DatabaseEngine;
    service?: string;
    databaseName?: string;
  }): Promise<ProvisionApplicationResult> {
    return await provisionResolved((state) => {
      const current = getAppOrThrow(state, input.slug);
      return {
        slug: current.slug,
        domain: current.mainDomain,
        aliases: current.aliases,
        kind: current.kind,
        ...(current.kind === "process"
          ? {
              processLanguage: current.runtime.language,
              processVersion: current.runtime.version,
              processCommand: current.runtime.command,
              processWorkdir: current.runtime.workdir,
              processPort: current.runtime.internalPort,
              processHealthPath: current.runtime.healthPath,
            }
          : {}),
        databaseEngine: input.engine,
        mysqlVersion: input.engine === "mysql" ? input.service : undefined,
        postgresVersion: input.engine === "postgres" ? input.service : undefined,
        createDatabase: true,
        databaseName: input.databaseName,
      };
    });
  }

  async function provisionResolved(
    resolveInput: (state: DesiredState) => ProvisionAppInput,
    options: ProvisionApplicationOptions = {},
  ): Promise<ProvisionApplicationResult> {
    const apply = options.apply ?? true;
    const result = await store.withExclusive(async (state) => {
      const input = resolveInput(state);
      const provisioned = provisionApp(platform, state, input);
      const selectedEngine = requestedDatabaseEngine(input);
      const selectedToken = selectedEngine === "mysql" ? input.mysqlVersion : input.postgresVersion;
      const selectedService = selectedToken
        ? state.databaseServices.find(
            (service) =>
              service.engine === selectedEngine &&
              (service.service === selectedToken || service.version === selectedToken),
          )?.service
        : undefined;
      const dataPlane = await applyAppDataPlane(platform, provisioned.app, {
        explicitDatabase: input.createDatabase === true,
        databaseEngine: selectedEngine,
        databaseService: selectedService,
        databaseName: input.databaseName ?? (input.createDatabase ? input.slug : undefined),
      });
      await materializeAppHome(platform, provisioned.app, {
        recursivePerms: true,
        redisSharedPassword: await loadRedisPassword(platform),
      });

      const nextState = provisioned.state;
      await store.save(nextState);
      if (apply) {
        try {
          await render.apply(nextState, {
            reloadPlan: provisioned.reloadPlan,
            skipValidate: options.skipValidate ?? false,
            alreadyLocked: true,
          });
        } catch (cause) {
          throw new ApplicationApplyError(cause);
        }
      }
      return { provisioned, state: nextState, dataPlane };
    });
    if (apply && !result.provisioned.created) {
      const recreated = await recreateRunningProcessApp(platform, result.state, result.provisioned.app);
      if (recreated && recreated.code !== 0) {
        throw new ApplicationApplyError(
          serviceError(
            `the process container could not be recreated: ${diagnostic(recreated)}`,
            "Fix Docker or the process application configuration, then run `bento apply`.",
          ),
        );
      }
    }

    return { ...result.provisioned, state: result.state, dataPlane: result.dataPlane };
  }

  async function setEnabled(
    slug: string,
    enabled: boolean,
    options: { apply?: boolean } = {},
  ): Promise<SetApplicationEnabledResult> {
    const apply = options.apply ?? true;
    const changed = await store.withExclusive(async (state) => {
      const current = getAppOrThrow(state, slug);
      if (enabled && apply && isProcessApp(current) && !(await isProcessAppHealthy(platform, state, current))) {
        throw safetyError(
          `refusing to publish process app ${current.slug} before it is running`,
          `Run 'bento app start ${current.slug}', verify health, then enable it.`,
        );
      }
      const mutation = setAppEnabled(state, slug, enabled, platform.clock.nowIso());
      await store.save(mutation.state);
      if (apply) {
        await render.apply(mutation.state, {
          reloadPlan: mutation.reloadPlan,
          skipValidate: false,
          alreadyLocked: true,
        });
      }
      return mutation;
    });

    if (!enabled && apply && isProcessApp(changed.app)) {
      const stopped = await platform.process.run(
        await composeArgs(platform, changed.state, ["stop", changed.app.runtime.service]),
        { cwd: platform.paths.paths.root, timeoutMs: 60_000 },
      );
      if (stopped.code !== 0) {
        return {
          ...changed,
          stopWarning: `public route is disabled, but the private process container did not stop: ${diagnostic(stopped)}`,
        };
      }
    }
    return changed;
  }

  async function setRunning(slug: string, action: "start" | "stop"): Promise<AppState> {
    const state = await store.load();
    const app = getAppOrThrow(state, slug);
    if (!isProcessApp(app)) throw safetyError("start/stop is only available for process applications");
    if (action === "start") {
      await render.apply(state, { reloadPlan: emptyReloadPlan(), skipValidate: false });
    }
    const result =
      action === "start" ? await startProcessApp(platform, state, app) : await stopProcessApp(platform, state, app);
    if (!result || result.code !== 0) {
      throw serviceError(`process application ${action} failed${result ? `: ${diagnostic(result)}` : ""}`);
    }
    return app;
  }

  async function remove(
    slug: string,
    confirmation: string | undefined,
    options: { apply?: boolean } = {},
  ): Promise<AppLifecycleResult> {
    const apply = options.apply ?? true;
    return await store.withExclusive(async (state) => {
      const result = deleteApp(state, slug, confirmation, platform.clock.nowIso());
      if (isProcessApp(result.app) && apply) {
        const stopped = await removeProcessAppContainer(platform, state, result.app);
        if (stopped && stopped.code !== 0) {
          throw safetyError(
            `refusing to remove process app ${result.app.slug} while its container cannot be stopped`,
            "Restore Docker/Compose access and retry; durable data remains unchanged.",
          );
        }
      }
      await writeAppPruneManifest(platform, result.app);
      await store.save(result.state);
      if (apply) {
        await render.apply(result.state, {
          reloadPlan: result.reloadPlan,
          skipValidate: false,
          alreadyLocked: true,
        });
      }
      return result;
    });
  }

  return { provision, addDatabase, setEnabled, setRunning, remove };
}

export type ApplicationUseCases = ReturnType<typeof createApplicationUseCases>;

function requestedDatabaseEngine(input: ProvisionAppInput): DatabaseEngine | undefined {
  return (input.databaseEngine ?? (input.mysqlVersion ? "mysql" : input.postgresVersion ? "postgres" : undefined)) as
    | DatabaseEngine
    | undefined;
}

function diagnostic(result: { stdout: string; stderr: string; code: number }): string {
  return (result.stderr || result.stdout || `exit ${result.code}`).trim().slice(0, 2_000);
}
