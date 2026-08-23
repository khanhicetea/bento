import { oc } from "@orpc/contract";
import { applicationsContract } from "./domains/applications/contract.ts";
import { dataContract } from "./domains/data/contract.ts";
import { jobsContract } from "./domains/jobs/contract.ts";
import { operationsContract } from "./domains/operations/contract.ts";
import { routingContract } from "./domains/routing/contract.ts";
import { systemContract } from "./domains/system/contract.ts";

/** Root API contract. Add domain routers here as web features are implemented. */
export const webContract = oc.router({
  system: systemContract,
  applications: applicationsContract,
  data: dataContract,
  routing: routingContract,
  jobs: jobsContract,
  operations: operationsContract,
});

export type WebContract = typeof webContract;
