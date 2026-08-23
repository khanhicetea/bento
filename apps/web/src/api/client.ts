import { createORPCClient } from "@orpc/client";
import { RPCLink } from "@orpc/client/fetch";
import type { ContractRouterClient } from "@orpc/contract";
import type { WebContract } from "@bento/shared";

const link = new RPCLink({ url: `${window.location.origin}/rpc` });

/** Typed clients are nested by domain: api.applications.*, api.system.*, etc. */
export const api = createORPCClient<ContractRouterClient<WebContract>>(link);
