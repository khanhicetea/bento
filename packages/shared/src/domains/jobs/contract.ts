import { oc } from "@orpc/contract";
import { z } from "zod";
import { jobsOverviewSchema } from "./schema.ts";

export const jobsContract = oc.router({
  overview: oc.input(z.object({}).optional()).output(jobsOverviewSchema),
});

export type JobsContract = typeof jobsContract;
