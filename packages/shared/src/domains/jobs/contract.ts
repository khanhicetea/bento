import { oc } from "@orpc/contract";
import { z } from "zod";
import {
  addCronJobInputSchema,
  addWorkerInputSchema,
  jobLogsInputSchema,
  jobLogsSchema,
  jobsOverviewSchema,
  removeAppJobInputSchema,
  workerRestartResultSchema,
} from "./schema.ts";

export const jobsContract = oc.router({
  overview: oc.input(z.object({}).optional()).output(jobsOverviewSchema),
  logs: oc.input(jobLogsInputSchema).output(jobLogsSchema),
  addCron: oc.input(addCronJobInputSchema).output(jobsOverviewSchema),
  removeCron: oc.input(removeAppJobInputSchema).output(jobsOverviewSchema),
  addWorker: oc.input(addWorkerInputSchema).output(jobsOverviewSchema),
  removeWorker: oc.input(removeAppJobInputSchema).output(jobsOverviewSchema),
  restartWorker: oc.input(removeAppJobInputSchema).output(workerRestartResultSchema),
});

export type JobsContract = typeof jobsContract;
