import { oc } from "@orpc/contract";
import { z } from "zod";
import { operationsOverviewSchema } from "./schema.ts";

export const operationsContract = oc.router({
  overview: oc.input(z.object({}).optional()).output(operationsOverviewSchema),
});

export type OperationsContract = typeof operationsContract;
