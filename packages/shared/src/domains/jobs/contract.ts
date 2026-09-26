import { oc } from "@orpc/contract";
import { z } from "zod";
import { schedulerAccessSchema } from "./schema.ts";

export const jobsContract = oc.router({
  schedulerAccess: oc.input(z.object({}).optional()).output(schedulerAccessSchema),
});

export type JobsContract = typeof jobsContract;
