/** Targeted lifecycle helpers for dedicated Node.js, Bun, and Python app services. */

import { isProcessApp, type AppState, type DesiredState } from "#/domain/state.ts";
import type { Platform, RunResult } from "#/platform/mod.ts";
import { composeArgs } from "#/services/compose.ts";

async function runCompose(
  platform: Platform,
  state: DesiredState,
  command: string[],
  timeoutMs: number,
): Promise<RunResult> {
  return await platform.process.run(await composeArgs(platform, state, command), {
    cwd: platform.paths.paths.root,
    timeoutMs,
  });
}

export async function isProcessAppHealthy(
  platform: Platform,
  state: DesiredState,
  app: AppState,
): Promise<boolean> {
  if (!isProcessApp(app)) return false;
  const container = await runCompose(platform, state, ["ps", "-q", app.runtime.service], 30_000);
  const containerId = container.code === 0 ? container.stdout.trim().split(/\s+/)[0] : undefined;
  if (!containerId) return false;
  const inspected = await platform.process.run(
    [
      "docker",
      "inspect",
      "--format={{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}",
      containerId,
    ],
    { cwd: platform.paths.paths.root, timeoutMs: 30_000 },
  );
  return inspected.code === 0 && inspected.stdout.trim() === "healthy";
}

export async function isProcessAppRunning(
  platform: Platform,
  state: DesiredState,
  app: AppState,
): Promise<boolean> {
  if (!isProcessApp(app)) return false;
  const result = await runCompose(
    platform,
    state,
    ["ps", "--services", "--status", "running", app.runtime.service],
    30_000,
  );
  return result.code === 0 && result.stdout.split(/\s+/).includes(app.runtime.service);
}

/** Recreate only when the process app is enabled and already running. */
export async function recreateRunningProcessApp(
  platform: Platform,
  state: DesiredState,
  app: AppState,
): Promise<RunResult | null> {
  if (!isProcessApp(app) || !app.enabled) return null;
  if (!(await isProcessAppRunning(platform, state, app))) return null;
  return await runCompose(
    platform,
    state,
    ["up", "-d", "--build", "--no-deps", "--wait", "--wait-timeout", "60", app.runtime.service],
    30 * 60_000,
  );
}

export async function startProcessApp(
  platform: Platform,
  state: DesiredState,
  app: AppState,
): Promise<RunResult | null> {
  if (!isProcessApp(app)) return null;
  return await runCompose(
    platform,
    state,
    ["up", "-d", "--build", "--wait", "--wait-timeout", "60", app.runtime.service],
    30 * 60_000,
  );
}

export async function stopProcessApp(
  platform: Platform,
  state: DesiredState,
  app: AppState,
): Promise<RunResult | null> {
  if (!isProcessApp(app)) return null;
  return await runCompose(platform, state, ["stop", app.runtime.service], 60_000);
}

/** Remove only the stopped process container; app homes and data volumes are untouched. */
export async function removeProcessAppContainer(
  platform: Platform,
  state: DesiredState,
  app: AppState,
): Promise<RunResult | null> {
  if (!isProcessApp(app)) return null;
  return await runCompose(platform, state, ["rm", "-f", "-s", app.runtime.service], 60_000);
}
