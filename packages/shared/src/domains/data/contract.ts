import { oc } from "@orpc/contract";
import { z } from "zod";
import {
  backupRunsSchema,
  startBackupResultSchema,
  databaseActivitySchema,
  databaseBackupResultSchema,
  databaseRestoreResultSchema,
  databaseRuntimeSchema,
  dataOverviewSchema,
} from "./schema.ts";

const relationalEngine = z.enum(["mysql", "postgres"]);
const backupEngine = z.enum(["mysql", "postgres", "sqlite"]);

export const dataContract = oc.router({
  overview: oc.input(z.object({}).optional()).output(dataOverviewSchema),
  backupRuns: oc.input(z.object({}).optional()).output(backupRunsSchema),
  startBackup: oc.input(z.object({ app: z.string().min(1).optional() })).output(startBackupResultSchema),
  runtime: oc.input(z.object({ service: z.string().min(1), engine: relationalEngine })).output(databaseRuntimeSchema),
  activity: oc.input(z.object({ service: z.string().min(1), engine: relationalEngine })).output(databaseActivitySchema),
  backup: oc
    .input(z.object({ app: z.string().min(1), database: z.string().min(1), engine: backupEngine }))
    .output(databaseBackupResultSchema),
  restore: oc
    .input(
      z.object({
        app: z.string().min(1),
        engine: relationalEngine,
        artifact: z.string().min(1),
        targetDatabase: z.string().min(1),
        confirmation: z.string(),
      }),
    )
    .output(databaseRestoreResultSchema),
});

export type DataContract = typeof dataContract;
