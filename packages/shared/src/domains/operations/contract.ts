import { oc } from "@orpc/contract";
import { z } from "zod";
import {
  operationResultSchema,
  operationsBackupResultSchema,
  operationsLogsSchema,
  operationsMaintenanceResultSchema,
  operationsOverviewSchema,
  stackActionInputSchema,
} from "./schema.ts";

export const operationsContract = oc.router({
  overview: oc.input(z.object({}).optional()).output(operationsOverviewSchema),
  stackAction: oc.input(stackActionInputSchema).output(operationResultSchema),
  restartService: oc
    .input(z.object({ service: z.string().min(1).max(100), confirmation: z.string() }))
    .output(operationResultSchema),
  apply: oc.input(z.object({})).output(operationResultSchema),
  backup: oc.input(z.object({})).output(operationsBackupResultSchema),
  logs: oc
    .input(
      z.object({
        tail: z.number().int().min(1).max(500).default(100),
        service: z.string().min(1).max(100).optional(),
      }),
    )
    .output(operationsLogsSchema),
  maintenance: oc
    .input(z.object({ retainDays: z.number().int().min(1).max(365).default(14) }))
    .output(operationsMaintenanceResultSchema),
  drainDeploy: oc
    .input(z.object({ app: z.string().min(1), confirmation: z.string() }))
    .output(operationResultSchema),
});

export type OperationsContract = typeof operationsContract;
