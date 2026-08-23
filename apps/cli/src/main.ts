/**
 * Bento control plane entrypoint.
 * Supports direct `bun run` and Bun-compiled standalone distributions.
 */

import { runCli } from "./commands/router.ts";

if (import.meta.main) {
  const code = await runCli(process.argv.slice(2));
  process.exit(code);
}

export { runCli };
