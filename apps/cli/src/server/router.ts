import { implement } from "@orpc/server";
import { webContract } from "@bento/shared";
import type { CliContext } from "../commands/context.ts";
import { createApplicationsRouter } from "./domains/applications/router.ts";
import { createSystemRouter } from "./domains/system/router.ts";

const os = implement(webContract);

/** Compose independently contracted domain routers into the public web API. */
export function createWebRouter(ctx: CliContext) {
  return os.router({
    system: createSystemRouter(),
    applications: createApplicationsRouter(ctx),
  });
}

export type AppRouter = ReturnType<typeof createWebRouter>;
