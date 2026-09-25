/** Retired Bento-owned cron API. Definitions now live in each app's minicrond registry. */
import type { CronJob, DesiredState } from "../domain/state.ts";
import { validationError } from "../domain/errors.ts";
import type { Platform } from "../platform/mod.ts";
import type { ReloadPlan } from "../domain/reload.ts";

export type AddCronInput = {
  name: string;
  app: string;
  schedule: string;
  command: string[];
  commandMode?: "argv" | "shell";
  timezone?: string;
  workdir?: string;
  output?: "log" | "null" | "inherit";
  timeoutSec?: number;
  lock?: string;
};

export type EditCronInput = Partial<Omit<AddCronInput, "app" | "name">> & {
  app: string;
  name: string;
};

function retired(): never {
  throw validationError("Bento cron jobs are retired; use bento app minicrond <app> -- <args>");
}

export function addCronJob(
  _state: DesiredState,
  _input: AddCronInput,
  _platform: Platform,
): { state: DesiredState; job: CronJob; reloadPlan: ReloadPlan } {
  return retired();
}

export function editCronJob(
  _state: DesiredState,
  _input: EditCronInput,
  _platform: Platform,
): { state: DesiredState; job: CronJob; reloadPlan: ReloadPlan } {
  return retired();
}

export function removeCronJob(
  _state: DesiredState,
  _appSlug: string,
  _name: string,
  _now: string,
): { state: DesiredState; reloadPlan: ReloadPlan } {
  return retired();
}

export function buildCronReloadCommand(_state: DesiredState, _appSlug: string): string[] {
  return retired();
}

export async function reloadCronScheduler(
  _platform: Platform,
  _state: DesiredState,
  _appSlug: string,
): Promise<{ code: number; stdout: string; stderr: string }> {
  return retired();
}

export function listCronJobs(state: DesiredState, appSlug?: string): CronJob[] {
  return state.cronJobs
    .filter((job) => !appSlug || job.app === appSlug)
    .sort((a, b) => `${a.app}:${a.name}`.localeCompare(`${b.app}:${b.name}`));
}
