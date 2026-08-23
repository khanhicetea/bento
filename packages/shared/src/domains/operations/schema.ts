import { z } from "zod";

const roleSchema = z.object({
  name: z.string(),
  kind: z.enum(["nginx", "redis", "php-fpm", "php-runner", "mysql", "postgres", "litestream"]),
  state: z.enum(["running", "stopped", "unknown", "config-ready"]),
  uptimeSeconds: z.number().int().nonnegative().optional(),
  detail: z.string().optional(),
});

export const operationsOverviewSchema = z.object({
  initialized: z.boolean(),
  stackRoot: z.string(),
  error: z.string().optional(),
  stackName: z.string().optional(),
  roles: z.array(roleSchema),
  runtimes: z.array(
    z.object({
      version: z.string(),
      service: z.string(),
      runner: z.string(),
      processCap: z.number().int(),
      appCount: z.number().int().nonnegative(),
      poolMaxSum: z.number().int().nonnegative(),
      overCap: z.boolean(),
    }),
  ),
  generation: z
    .object({
      assetVersion: z.string().optional(),
      renderedAt: z.string().optional(),
    })
    .optional(),
  counts: z.object({
    applications: z.number().int().nonnegative(),
    cronJobs: z.number().int().nonnegative(),
    workers: z.number().int().nonnegative(),
    proxies: z.number().int().nonnegative(),
  }),
  warnings: z.array(z.string()),
  notes: z.array(z.string()),
});

export const operationResultSchema = z.object({
  message: z.string(),
  completedAt: z.string(),
});

export const stackActionInputSchema = z.object({
  action: z.enum(["start", "stop", "restart"]),
  confirmation: z.string().optional(),
});

export const operationsLogsSchema = z.object({
  lines: z.array(z.string()),
  truncated: z.boolean(),
});

export const operationsBackupResultSchema = operationResultSchema.extend({
  artifacts: z.array(
    z.object({
      engine: z.enum(["mysql", "postgres", "sqlite"]),
      database: z.string(),
      bytes: z.number().int().nonnegative(),
      path: z.string(),
    }),
  ),
});

export const operationsMaintenanceResultSchema = operationResultSchema.extend({
  removed: z.number().int().nonnegative(),
  notes: z.array(z.string()),
});

export type OperationsOverview = z.infer<typeof operationsOverviewSchema>;
