import type { DesiredState } from "#/domain/state.ts";
import { isPhpApp } from "#/domain/state.ts";
import { notFoundError, validationError } from "#/domain/errors.ts";
import { appSlugSchema } from "#/schemas/validators.ts";

/** An app-scoped invocation of the *running* PHP runner's private Unix socket. */
export function minicrondComposeCommand(state: DesiredState, slug: string, args: string[]): string[] {
  if (!appSlugSchema.safeParse(slug).success) throw validationError("invalid app slug");
  const app = Object.hasOwn(state.apps, slug) ? state.apps[slug] : undefined;
  if (!app) throw notFoundError(`app not found: ${slug}`);
  if (!isPhpApp(app) || !app.enabled) {
    throw validationError("minicrond requires an enabled PHP application");
  }
  if (args.length === 0) throw validationError("provide minicrond arguments after --");
  if (args.some((arg) => arg.includes("\0"))) throw validationError("invalid minicrond argument");

  return [
    "exec",
    "-T",
    "--user",
    `${app.uid}:${app.gid}`,
    "-w",
    app.home,
    "-e",
    `MINICRON_DATA=${app.home}/.local/share/minicron`,
    "-e",
    `BASE_PATH=/scheduler/apps/${app.slug}/`,
    "-e",
    `HOME=${app.home}`,
    "-e",
    `USER=${app.slug}`,
    "-e",
    "PATH=/usr/local/bin:/usr/bin:/bin",
    "-e",
    "TZ=UTC",
    `${app.phpService}-runner`,
    "minicrond",
    ...args,
  ];
}
