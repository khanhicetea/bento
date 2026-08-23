import { z } from "zod";

export const databaseBindingSchema = z.object({
  engine: z.enum(["mysql", "postgres", "sqlite", "litestream"]),
  service: z.string().optional(),
  names: z.array(z.string()),
  file: z.string().optional(),
});

export const applicationSchema = z.object({
  slug: z.string(),
  enabled: z.boolean(),
  domain: z.string(),
  aliases: z.array(z.string()),
  phpVersion: z.string(),
  fpmProfile: z.string(),
  tls: z.enum(["shared", "self-ca", "acme", "external"]),
  accessLog: z.boolean(),
  deployEnabled: z.boolean(),
  databases: z.array(databaseBindingSchema),
});

export const applicationListSchema = z.object({
  initialized: z.boolean(),
  stateExists: z.boolean(),
  error: z.string().optional(),
  stackRoot: z.string(),
  applications: z.array(applicationSchema),
  phpVersions: z.array(z.string()),
});

export const setApplicationEnabledInputSchema = z.object({
  slug: z.string().min(1).max(128),
  enabled: z.boolean(),
});

export type Application = z.infer<typeof applicationSchema>;
export type ApplicationList = z.infer<typeof applicationListSchema>;
export type SetApplicationEnabledInput = z.infer<typeof setApplicationEnabledInputSchema>;
