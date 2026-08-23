import { oc } from "@orpc/contract";
import { z } from "zod";
import {
  removeRoutingProxyInputSchema,
  routingOverviewSchema,
  routingProxySchema,
  saveRoutingProxyInputSchema,
  setRoutingProxyEnabledInputSchema,
} from "./schema.ts";

export const routingContract = oc.router({
  overview: oc.input(z.object({}).optional()).output(routingOverviewSchema),
  saveProxy: oc.input(saveRoutingProxyInputSchema).output(routingProxySchema),
  setProxyEnabled: oc.input(setRoutingProxyEnabledInputSchema).output(routingProxySchema),
  removeProxy: oc.input(removeRoutingProxyInputSchema).output(routingProxySchema),
});

export type RoutingContract = typeof routingContract;
