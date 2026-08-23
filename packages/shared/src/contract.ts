import { oc } from "@orpc/contract";
import { applicationsContract } from "./domains/applications/contract.ts";
import { systemContract } from "./domains/system/contract.ts";

/** Root API contract. Add domain routers here as web features are implemented. */
export const webContract = oc.router({
  system: systemContract,
  applications: applicationsContract,
});

export type WebContract = typeof webContract;
