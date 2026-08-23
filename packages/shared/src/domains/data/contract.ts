import { oc } from "@orpc/contract";
import { z } from "zod";
import {
  databaseBackupResultSchema,
  databaseRestoreResultSchema,
  databaseRuntimeSchema,
  dataOverviewSchema,
} from "./schema.ts";

const relationalEngine = z.enum(["mysql", "postgres"]);

export const dataContract = oc.router({
  overview: oc.input(z.object({}).optional()).output(dataOverviewSchema),
  runtime: oc
    .input(z.object({ service: z.string().min(1), engine: relationalEngine }))
    .output(databaseRuntimeSchema),
  backup: oc
    .input(
      z.object({ app: z.string().min(1), database: z.string().min(1), engine: relationalEngine }),
    )
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
