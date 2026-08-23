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

export type DataOverview = z.infer<typeof dataOverviewSchema>;
