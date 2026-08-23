import { oc } from "@orpc/contract";
import { z } from "zod";
import { healthSchema } from "./schema.ts";

export const systemContract = oc.router({
  health: oc.input(z.object({}).optional()).output(healthSchema),
});

export type SystemContract = typeof systemContract;
