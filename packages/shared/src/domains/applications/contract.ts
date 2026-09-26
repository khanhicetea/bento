import { oc } from "@orpc/contract";
import { z } from "zod";
import {
  addApplicationDatabaseInputSchema,
  applicationDatabaseCredentialsInputSchema,
  applicationDatabaseCredentialsSchema,
  applicationListSchema,
  applicationPublicKeyInputSchema,
  applicationPublicKeySchema,
  applicationSchema,
  removeApplicationInputSchema,
  saveApplicationInputSchema,
  setApplicationEnabledInputSchema,
  setApplicationRunningInputSchema,
} from "./schema.ts";

const emptyInputSchema = z.object({}).optional();

/** Browser-safe application procedures. Mutations are explicit domain operations, not CLI argv. */
export const applicationsContract = oc.router({
  list: oc.input(emptyInputSchema).output(applicationListSchema),
  publicKey: oc.input(applicationPublicKeyInputSchema).output(applicationPublicKeySchema),
  databaseCredentials: oc.input(applicationDatabaseCredentialsInputSchema).output(applicationDatabaseCredentialsSchema),
  save: oc.input(saveApplicationInputSchema).output(applicationSchema),
  addDatabase: oc.input(addApplicationDatabaseInputSchema).output(applicationSchema),
  setEnabled: oc.input(setApplicationEnabledInputSchema).output(applicationSchema),
  setRunning: oc.input(setApplicationRunningInputSchema).output(applicationSchema),
  remove: oc.input(removeApplicationInputSchema).output(applicationSchema),
});

export type ApplicationsContract = typeof applicationsContract;
