import { implement } from "@orpc/server";
import { routingContract, type RoutingOverview } from "@bento/shared";
import type { CliContext } from "../../../commands/context.ts";
import { loadStackComposeEnvironment } from "../../../services/stack_env.ts";
import { redact } from "../../../ui/output.ts";

const os = implement(routingContract);

export function createRoutingRouter(ctx: CliContext) {
  return os.router({ overview: os.overview.handler(async () => await routingOverview(ctx)) });
}

async function routingOverview(ctx: CliContext): Promise<RoutingOverview> {
  if (!(await ctx.store.exists())) return empty(ctx.stackRoot);
  try {
    const state = await ctx.store.load();
    const environment = await loadStackComposeEnvironment(ctx.platform);
    return {
      initialized: true,
      stackRoot: ctx.stackRoot,
      ingress: {
        mode: environment.nginx.hostNetwork ? "host" : "bridge",
        ...(environment.nginx.httpPort !== undefined
          ? { httpPort: environment.nginx.httpPort }
          : {}),
        ...(environment.nginx.httpsPort !== undefined
          ? { httpsPort: environment.nginx.httpsPort }
          : {}),
        http3: environment.nginx.http3,
      },
      domains: Object.entries(state.domains)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([domain, owner]) => {
          if (owner.kind === "app") {
            const app = state.apps[owner.slug];
            if (!app) throw new Error(`domain ${domain} has a missing application owner`);
            return {
              domain,
              ownerKind: "application" as const,
              owner: String(owner.slug),
              primary: owner.primary,
              tls: app.tls.kind,
            };
          }
          const proxy = state.proxies[owner.name];
          if (!proxy) throw new Error(`domain ${domain} has a missing proxy owner`);
          return {
            domain,
            ownerKind: "proxy" as const,
            owner: String(owner.name),
            primary: owner.primary,
            tls: proxy.tls.kind,
          };
        }),
      proxies: Object.values(state.proxies)
        .sort((left, right) => left.name.localeCompare(right.name))
        .map((proxy) => ({
          name: String(proxy.name),
          domain: String(proxy.mainDomain),
          aliases: proxy.aliases.map(String),
          upstreams: [...proxy.upstreams],
          tls: proxy.tls.kind,
          accessLog: proxy.accessLog,
        })),
    };
  } catch (error) {
    return empty(ctx.stackRoot, redact(error instanceof Error ? error.message : String(error)));
  }
}

function empty(stackRoot: string, error?: string): RoutingOverview {
  return {
    initialized: false,
    stackRoot,
    domains: [],
    proxies: [],
    ...(error ? { error } : {}),
  };
}
