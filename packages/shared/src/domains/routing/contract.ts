import { oc } from "@orpc/contract";
import { z } from "zod";
import { routingOverviewSchema } from "./schema.ts";

export const routingContract = oc.router({
  overview: oc.input(z.object({}).optional()).output(routingOverviewSchema),
});

export type RoutingContract = typeof routingContract;
