import { implement } from "@orpc/server";
import { systemContract } from "@bento/shared";

const os = implement(systemContract);

export function createSystemRouter() {
  return os.router({
    health: os.health.handler(() => ({ ok: true as const, service: "bento" as const })),
  });
}
