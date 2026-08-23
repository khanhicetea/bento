import { z } from "zod";

export const cronJobSchema = z.object({
  name: z.string(),
  app: z.string(),
  schedule: z.string(),
  timezone: z.string(),
  command: z.string(),
  commandMode: z.enum(["argv", "shell"]),
  output: z.enum(["log", "null", "inherit"]),
  enabled: z.boolean(),
  timeoutSec: z.number().int().optional(),
});

export const workerSchema = z.object({
  name: z.string(),
  app: z.string(),
  command: z.string(),
  enabled: z.boolean(),
  autorestart: z.boolean(),
  stopsignal: z.string(),
  stopwaitsecs: z.number().int(),
});

export const jobsOverviewSchema = z.object({
  initialized: z.boolean(),
  stackRoot: z.string(),
  error: z.string().optional(),
  cronJobs: z.array(cronJobSchema),
  workers: z.array(workerSchema),
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

export const addCronJobInputSchema = z
  .object({
    app: z.string().trim().min(1).max(63),
    name: z.string().trim().min(1).max(128),
    schedule: z.string().trim().min(1).max(256),
    timezone: z.string().trim().min(1).max(128),
    command: z.array(z.string().min(1).max(4096)).min(1).max(128),
    commandMode: z.enum(["argv", "shell"]),
    output: z.enum(["log", "null", "inherit"]),
    timeoutSec: z.number().int().positive().optional(),
  })
  .superRefine((input, ctx) => {
    if (input.commandMode === "shell" && input.command.length !== 1) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["command"],
        message: "Shell commands must be supplied as one string",
      });
    }
  });

export const removeAppJobInputSchema = z.object({
  app: z.string().trim().min(1).max(63),
  name: z.string().trim().min(1).max(128),
});

export const addWorkerInputSchema = z.object({
  app: z.string().trim().min(1).max(63),
  name: z.string().trim().min(1).max(128),
  command: z.array(z.string().min(1).max(4096)).min(1).max(128),
  autorestart: z.boolean(),
  stopsignal: z.string().trim().min(1).max(32),
  stopwaitsecs: z.number().int().positive(),
});

export type AddCronJobInput = z.infer<typeof addCronJobInputSchema>;
export type AddWorkerInput = z.infer<typeof addWorkerInputSchema>;
export type JobsOverview = z.infer<typeof jobsOverviewSchema>;
export type RemoveAppJobInput = z.infer<typeof removeAppJobInputSchema>;
