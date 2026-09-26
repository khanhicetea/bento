import { notFoundError, platformError, safetyError } from "#/domain/errors.ts";
import { emptyReloadPlan } from "#/domain/reload.ts";
import type { Platform, RunResult } from "#/platform/mod.ts";
import { validateCloudflareTunnelToken, writeCloudflareTunnelToken } from "#/services/cloudflare_tunnel.ts";
import { composeArgs } from "#/services/compose.ts";
import { RenderService, type ApplyOptions, type RenderResult } from "#/services/render.ts";
import { StateStore } from "#/services/state_store.ts";
import { buildStatus } from "#/services/status.ts";

export function createOperationsUseCases(deps: { platform: Platform; store: StateStore; render: RenderService }) {
  const { platform, store, render } = deps;

  async function stackAction(action: "start" | "stop" | "restart", confirmation?: string): Promise<void> {
    const state = await store.load();
    const status = await buildStatus(platform, state);
    if (action !== "start" && confirmation !== status.stackName) {
      throw safetyError(`confirmation must exactly match stack name: ${status.stackName}`);
    }
    const subcommand = action === "start" ? ["up", "-d"] : action === "stop" ? ["stop"] : ["restart"];
    await runCompose(state, subcommand, 120_000);
  }

  async function restartService(service: string, confirmation?: string): Promise<void> {
    if (confirmation !== service) throw safetyError("confirmation must exactly match the service name");
    const state = await store.load();
    await requireService(state, service);
    await runCompose(state, ["restart", service], 120_000);
  }

  async function apply(options: ApplyOptions = {}): Promise<RenderResult> {
    return await render.apply(await store.load(), options);
  }

  async function configureCloudflareTunnel(value: string): Promise<void> {
    const token = validateCloudflareTunnelToken(value);
    const state = await store.load();
    const tokenPath = platform.paths.paths.cloudflareTunnelTokenFile;
    const release = await platform.lock.exclusive(platform.paths.paths.renderLock);
    try {
      const previousToken = (await platform.fs.exists(tokenPath)) ? await platform.fs.readText(tokenPath) : undefined;
      await writeCloudflareTunnelToken(platform, token);
      try {
        await render.apply(state, {
          alreadyLocked: true,
          renderOnly: true,
          reloadPlan: emptyReloadPlan(),
        });
      } catch (error) {
        if (previousToken === undefined) await platform.fs.remove(tokenPath);
        else await platform.fs.atomicWriteText(tokenPath, previousToken, 0o600);
        throw error;
      }
      await runCompose(state, ["up", "-d", "--force-recreate", "cloudflared"], 120_000);
    } finally {
      await release();
    }
  }

  async function logs(input: { service?: string; tail: number }): Promise<{ lines: string[]; truncated: boolean }> {
    const state = await store.load();
    if (input.service) await requireService(state, input.service);
    const args = ["logs", "--no-color", "--tail", String(input.tail)];
    if (input.service) args.push(input.service);
    const result = await runCompose(state, args, 15_000);
    const allLines = result.stdout
      .split("\n")
      .filter(Boolean)
      .map((line) => line.slice(0, 2_000));
    return { lines: allLines.slice(-input.tail), truncated: allLines.length > input.tail };
  }

  async function requireService(state: Awaited<ReturnType<StateStore["load"]>>, service: string): Promise<void> {
    const result = await runCompose(state, ["config", "--services"], 15_000);
    const services = result.stdout
      .split("\n")
      .map((entry) => entry.trim())
      .filter(Boolean);
    if (!services.includes(service)) throw notFoundError(`unknown service: ${service}`);
  }

  async function runCompose(
    state: Awaited<ReturnType<StateStore["load"]>>,
    args: string[],
    timeoutMs: number,
  ): Promise<RunResult> {
    const result = await platform.process.run(await composeArgs(platform, state, args), {
      cwd: platform.paths.paths.root,
      timeoutMs,
    });
    if (result.code !== 0) {
      throw platformError(`docker compose ${args[0] ?? "command"} failed: ${diagnostic(result)}`);
    }
    return result;
  }

  return { stackAction, restartService, apply, configureCloudflareTunnel, logs };
}

export type OperationsUseCases = ReturnType<typeof createOperationsUseCases>;

function diagnostic(result: RunResult): string {
  return (result.stderr || result.stdout || `exit ${result.code}`).trim().slice(0, 4_000);
}
