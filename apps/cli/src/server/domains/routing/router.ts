import { implement, ORPCError } from "@orpc/server";
import { routingContract, type RoutingOverview, type RoutingProxy, type SaveRoutingProxyInput } from "@bento/shared";
import type { CliContext } from "#/commands/context.ts";
import { isBentoError, type BentoError } from "#/domain/errors.ts";
import type { ProxySite, TlsMode } from "#/domain/state.ts";
import { loadStackComposeEnvironment } from "#/services/stack_env.ts";
import { redact } from "#/ui/output.ts";

const os = implement(routingContract);

export function createRoutingRouter(ctx: CliContext) {
  return os.router({
    overview: os.overview.handler(async () => await routingOverview(ctx)),
    saveProxy: os.saveProxy.handler(async ({ input }) => {
      try {
        const saved = await ctx.routing.saveProxy({
          operation: input.operation,
          name: input.name,
          domain: input.domain,
          aliases: input.aliases,
          upstreams: input.upstreams,
          tls: tlsMode(input),
          accessLog: input.accessLog,
        });
        return toRoutingProxy(saved.proxy);
      } catch (error) {
        logRoutingError(ctx, "save proxy", error);
        throw asORPCError(error);
      }
    }),
    setProxyEnabled: os.setProxyEnabled.handler(async ({ input }) => {
      try {
        const changed = await ctx.routing.setEnabled(input.name, input.enabled);
        return toRoutingProxy(changed.proxy);
      } catch (error) {
        logRoutingError(ctx, "set proxy enabled", error);
        throw asORPCError(error);
      }
    }),
    removeProxy: os.removeProxy.handler(async ({ input }) => {
      try {
        const removed = await ctx.routing.removeProxy(input.name, input.confirmation);
        return toRoutingProxy(removed.proxy);
      } catch (error) {
        logRoutingError(ctx, "remove proxy", error);
        throw asORPCError(error);
      }
    }),
  });
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
        ...(environment.nginx.httpPort !== undefined ? { httpPort: environment.nginx.httpPort } : {}),
        ...(environment.nginx.httpsPort !== undefined ? { httpsPort: environment.nginx.httpsPort } : {}),
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
        .map(toRoutingProxy),
    };
  } catch (error) {
    return empty(ctx.stackRoot, redact(error instanceof Error ? error.message : String(error)));
  }
}

function toRoutingProxy(proxy: ProxySite): RoutingProxy {
  return {
    name: String(proxy.name),
    enabled: proxy.enabled,
    domain: String(proxy.mainDomain),
    aliases: proxy.aliases.map(String),
    upstreams: proxy.upstreams.map(publicUpstream),
    tls: proxy.tls.kind,
    tlsCertificatePath: proxy.tls.kind === "external" ? proxy.tls.certPath : undefined,
    tlsKeyPath: proxy.tls.kind === "external" ? proxy.tls.keyPath : undefined,
    accessLog: proxy.accessLog,
  };
}

function publicUpstream(upstream: string): string {
  try {
    const url = new URL(upstream);
    if (!url.username && !url.password) return upstream;
    url.username = "redacted";
    url.password = "";
    return url.toString();
  } catch {
    return redact(upstream);
  }
}

function tlsMode(input: SaveRoutingProxyInput): TlsMode {
  if (input.tls !== "external") return { kind: input.tls };
  return {
    kind: "external",
    certPath: input.tlsCertificatePath!,
    keyPath: input.tlsKeyPath!,
  };
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

function logRoutingError(ctx: CliContext, operation: string, error: unknown): void {
  const detail = error instanceof Error ? (error.stack ?? error.message) : String(error);
  ctx.log.error(`routing ${operation} failed: ${redact(detail)}`);
}

function asORPCError(error: unknown): ORPCError<string, unknown> {
  if (!isBentoError(error)) {
    return new ORPCError("INTERNAL_SERVER_ERROR", { message: "Routing operation failed" });
  }
  const code =
    error.code === "NOT_FOUND"
      ? "NOT_FOUND"
      : error.code === "CONFLICT"
        ? "CONFLICT"
        : error.code === "VALIDATION" || error.code === "SAFETY"
          ? "BAD_REQUEST"
          : "INTERNAL_SERVER_ERROR";
  return new ORPCError(code, {
    message: code === "INTERNAL_SERVER_ERROR" ? "Routing operation failed" : errorMessage(error),
  });
}

function errorMessage(error: BentoError): string {
  return error.recovery ? `${error.message} ${error.recovery}` : error.message;
}
