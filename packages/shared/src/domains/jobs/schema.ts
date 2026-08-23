import { z } from "zod";

export const jobsOverviewSchema = z.object({
  initialized: z.boolean(),
  stackRoot: z.string(),
  error: z.string().optional(),
  cronJobs: z.array(
    z.object({
      name: z.string(),
      app: z.string(),
      schedule: z.string(),
      timezone: z.string(),
      command: z.string(),
      commandMode: z.enum(["argv", "shell"]),
      output: z.enum(["log", "null", "inherit"]),
      enabled: z.boolean(),
      timeoutSec: z.number().int().optional(),
    }),
  ),
  workers: z.array(
    z.object({
      name: z.string(),
      app: z.string(),
      command: z.string(),
      enabled: z.boolean(),
      autorestart: z.boolean(),
      stopsignal: z.string(),
      stopwaitsecs: z.number().int(),
    }),
  ),
  deploys: z.array(
    z.object({
      app: z.string(),
      enabled: z.boolean(),
      queuePolicy: z.enum(["latest", "fifo"]),
      timeoutSec: z.number().int(),
      command: z.string(),
    }),
  ),
});

export type JobsOverview = z.infer<typeof jobsOverviewSchema>;
