import type { TlsMode } from "#/domain/state.ts";
import type { Platform } from "#/platform/mod.ts";
import {
  createProxy,
  deleteProxy,
  setProxyEnabled,
  updateProxy,
  type CreateProxyInput,
  type ProxyMutationResult,
} from "#/services/proxy.ts";
import { RenderService } from "#/services/render.ts";
import { StateStore } from "#/services/state_store.ts";

export type SaveProxyInput = {
  operation: "create" | "update";
  name: string;
  domain: string;
  aliases?: string[];
  upstreams: string[];
  tls?: TlsMode;
  accessLog?: boolean;
};

export function createRoutingUseCases(deps: { platform: Platform; store: StateStore; render: RenderService }) {
  const { platform, store, render } = deps;

  async function saveProxy(
    input: SaveProxyInput,
    options: { apply?: boolean; skipValidate?: boolean } = {},
  ): Promise<ProxyMutationResult> {
    return await mutate(async (state) => {
      const mutation = input.operation === "create" ? createProxy : updateProxy;
      return mutation(state, input as CreateProxyInput, platform.clock.nowIso());
    }, options);
  }

  async function setEnabled(
    name: string,
    enabled: boolean,
    options: { apply?: boolean; skipValidate?: boolean } = {},
  ): Promise<ProxyMutationResult> {
    return await mutate((state) => setProxyEnabled(state, name, enabled, platform.clock.nowIso()), options);
  }

  async function removeProxy(
    name: string,
    confirmation: string | undefined,
    options: { apply?: boolean; skipValidate?: boolean } = {},
  ): Promise<ProxyMutationResult> {
    return await mutate((state) => deleteProxy(state, name, confirmation, platform.clock.nowIso()), options);
  }

  async function mutate(
    transition: (state: Awaited<ReturnType<StateStore["load"]>>) => ProxyMutationResult | Promise<ProxyMutationResult>,
    options: { apply?: boolean; skipValidate?: boolean },
  ): Promise<ProxyMutationResult> {
    return await store.withExclusive(async (state) => {
      const result = await transition(state);
      await store.save(result.state);
      if (options.apply ?? true) {
        await render.apply(result.state, {
          reloadPlan: result.reloadPlan,
          skipValidate: options.skipValidate ?? false,
          alreadyLocked: true,
        });
      }
      return result;
    });
  }

  return { saveProxy, setEnabled, removeProxy };
}

export type RoutingUseCases = ReturnType<typeof createRoutingUseCases>;
