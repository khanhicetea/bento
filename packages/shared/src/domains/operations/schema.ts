import { z } from "zod";

const roleSchema = z.object({
  name: z.string(),
  kind: z.enum(["nginx", "redis", "php-fpm", "php-runner", "process-app", "mysql", "postgres", "cloudflare-tunnel"]),
  state: z.enum(["running", "stopped", "unknown", "config-ready"]),
  uptimeSeconds: z.number().int().nonnegative().optional(),
  detail: z.string().optional(),
});

export const operationsOverviewSchema = z.object({
  initialized: z.boolean(),
  stackRoot: z.string(),
  error: z.string().optional(),
  stackName: z.string().optional(),
  cloudflareTunnel: z.object({ configured: z.boolean() }),
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
    proxies: z.number().int().nonnegative(),
  }),
  warnings: z.array(z.string()),
  notes: z.array(z.string()),
});

export const cloudflareTunnelInputSchema = z.object({
  token: z
    .string()
    .trim()
    .min(20)
    .max(4096)
    .regex(/^[A-Za-z0-9._~+/=-]+$/, "invalid Cloudflare tunnel token"),
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

export const operationsDoctorSchema = z.object({
  generatedAt: z.string(),
  stackRoot: z.string(),
  ok: z.boolean(),
  checks: z.array(
    z.object({
      id: z.string(),
      category: z.string(),
      status: z.enum(["pass", "warn", "fail"]),
      detail: z.string(),
    }),
  ),
  summary: z.object({
    pass: z.number().int().nonnegative(),
    warn: z.number().int().nonnegative(),
    fail: z.number().int().nonnegative(),
  }),
});

const backupOperationStatusSchema = z.enum(["running", "succeeded", "failed", "interrupted"]);

export const backupRunStatusSchema = z.object({
  lastRun: z
    .object({
      operationId: z
        .string()
        .regex(/^op_[a-f0-9]{16}$/)
        .optional(),
      status: backupOperationStatusSchema,
      startedAt: z.string().datetime(),
      finishedAt: z.string().datetime().nullable(),
      artifactCount: z.number().int().nonnegative(),
      artifactBytes: z.number().int().nonnegative(),
      error: z.string().max(2048).optional(),
    })
    .nullable(),
  lastOperation: z
    .object({
      id: z.string().regex(/^op_[a-f0-9]{16}$/),
      status: backupOperationStatusSchema,
      steps: z.array(
        z.object({
          name: z.enum(["backup", "upload"]),
          status: backupOperationStatusSchema,
          error: z.string().max(1024).optional(),
        }),
      ),
    })
    .nullable(),
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
export type OperationsDoctor = z.infer<typeof operationsDoctorSchema>;
