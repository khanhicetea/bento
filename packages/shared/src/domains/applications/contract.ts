import { oc } from "@orpc/contract";
import { z } from "zod";
import {
  addApplicationDatabaseInputSchema,
  applicationListSchema,
  applicationSchema,
  removeApplicationInputSchema,
  saveApplicationInputSchema,
  setApplicationEnabledInputSchema,
} from "./schema.ts";

const emptyInputSchema = z.object({}).optional();

/** Browser-safe application procedures. Mutations are explicit domain operations, not CLI argv. */
export const applicationsContract = oc.router({
  list: oc.input(emptyInputSchema).output(applicationListSchema),
  save: oc.input(saveApplicationInputSchema).output(applicationSchema),
  addDatabase: oc.input(addApplicationDatabaseInputSchema).output(applicationSchema),
  setEnabled: oc.input(setApplicationEnabledInputSchema).output(applicationSchema),
  remove: oc.input(removeApplicationInputSchema).output(applicationSchema),
});

export type ApplicationsContract = typeof applicationsContract;
