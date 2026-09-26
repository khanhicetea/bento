import { z } from "zod";

const databaseServiceSchema = z.object({
  engine: z.enum(["mysql", "postgres"]),
  version: z.string(),
  service: z.string(),
  image: z.string(),
  volume: z.string(),
  appCount: z.number().int().nonnegative(),
});

const databaseBindingSchema = z.object({
  app: z.string(),
  primary: z.boolean(),
  engine: z.enum(["mysql", "postgres", "sqlite"]),
  service: z.string(),
  resources: z.array(z.string()),
});

export const dataOverviewSchema = z.object({
  initialized: z.boolean(),
  stackRoot: z.string(),
  error: z.string().optional(),
  services: z.array(databaseServiceSchema),
  bindings: z.array(databaseBindingSchema),
  backups: z
    .array(
      z.object({
        name: z.string(),
        bytes: z.number().int().nonnegative(),
        modifiedAt: z.string().optional(),
      }),
    )
    .default([]),
});

const databaseProcessSchema = z.object({
  id: z.string(),
  user: z.string(),
  database: z.string(),
  state: z.string(),
  query: z.string(),
});

const databaseActivityBaseSchema = z.object({
  service: z.string(),
  engine: z.enum(["mysql", "postgres"]),
  error: z.string().optional(),
  processes: z.array(databaseProcessSchema),
});

export const databaseRuntimeSchema = databaseActivityBaseSchema.extend({
  serverVersion: z.string(),
  databases: z.array(z.object({ name: z.string(), bytes: z.number().int().nonnegative() })),
});

export const databaseActivitySchema = databaseActivityBaseSchema;

export const backupRunsSchema = z.object({
  runs: z.array(
    z.object({
      id: z.string().regex(/^op_[a-f0-9]{16}$/),
      status: z.enum(["running", "succeeded", "failed", "interrupted"]),
      startedAt: z.string().datetime(),
      finishedAt: z.string().datetime().nullable(),
      progress: z
        .object({ completed: z.number().int().nonnegative(), total: z.number().int().nonnegative() })
        .optional(),
      steps: z.array(
        z.object({
          name: z.enum(["backup", "upload"]),
          status: z.enum(["running", "succeeded", "failed", "interrupted"]),
          error: z.string().optional(),
        }),
      ),
    }),
  ),
});

export const startBackupResultSchema = z.object({ id: z.string().regex(/^op_[a-f0-9]{16}$/) });

export const databaseBackupResultSchema = z.object({
  artifacts: z.array(z.object({ name: z.string(), database: z.string(), bytes: z.number().int().nonnegative() })),
});

export const databaseRestoreResultSchema = z.object({ message: z.string() });

export type DataOverview = z.infer<typeof dataOverviewSchema>;
export type DatabaseRuntime = z.infer<typeof databaseRuntimeSchema>;
export type DatabaseActivity = z.infer<typeof databaseActivitySchema>;
