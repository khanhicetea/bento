import { oc } from "@orpc/contract";
import { z } from "zod";
import {
  applicationListSchema,
  applicationSchema,
  setApplicationEnabledInputSchema,
} from "./schema.ts";

const emptyInputSchema = z.object({}).optional();

/** Browser-safe application procedures. Mutations are explicit domain operations, not CLI argv. */
export const applicationsContract = oc.router({
  list: oc.input(emptyInputSchema).output(applicationListSchema),
  setEnabled: oc.input(setApplicationEnabledInputSchema).output(applicationSchema),
});

export type ApplicationsContract = typeof applicationsContract;
