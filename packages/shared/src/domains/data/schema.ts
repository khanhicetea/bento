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
  engine: z.enum(["mysql", "postgres", "sqlite", "litestream"]),
  service: z.string(),
  resources: z.array(z.string()),
  backupVerifiedAt: z.string().optional(),
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
  sqliteBackup: z
    .object({
      enabled: z.boolean(),
      provider: z.literal("litestream"),
      destination: z.string(),
      syncInterval: z.string(),
      snapshotInterval: z.string(),
      snapshotRetention: z.string(),
    })
    .optional(),
});

export const databaseRuntimeSchema = z.object({
  service: z.string(),
  engine: z.enum(["mysql", "postgres"]),
  serverVersion: z.string(),
  error: z.string().optional(),
  databases: z.array(z.object({ name: z.string(), bytes: z.number().int().nonnegative() })),
  processes: z.array(
    z.object({
      id: z.string(),
      user: z.string(),
      database: z.string(),
      state: z.string(),
      query: z.string(),
    }),
  ),
});

export const databaseBackupResultSchema = z.object({
  artifacts: z.array(
    z.object({ name: z.string(), database: z.string(), bytes: z.number().int().nonnegative() }),
  ),
});

export const databaseRestoreResultSchema = z.object({ message: z.string() });

export type DataOverview = z.infer<typeof dataOverviewSchema>;
export type DatabaseRuntime = z.infer<typeof databaseRuntimeSchema>;
