import { oc } from "@orpc/contract";
import { z } from "zod";
import { dataOverviewSchema } from "./schema.ts";

export const dataContract = oc.router({
  overview: oc.input(z.object({}).optional()).output(dataOverviewSchema),
});

export type DataContract = typeof dataContract;
