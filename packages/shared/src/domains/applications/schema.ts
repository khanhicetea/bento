import { z } from "zod";

export const databaseEngineSchema = z.enum(["mysql", "postgres", "sqlite", "litestream"]);
export const entrypointModeSchema = z.enum(["front-controller", "legacy"]);
export const tlsKindSchema = z.enum(["shared", "self-ca", "acme", "external"]);

export const databaseBindingSchema = z.object({
  engine: databaseEngineSchema,
  service: z.string().optional(),
  names: z.array(z.string()),
  file: z.string().optional(),
});

export const databaseServiceSchema = z.object({
  engine: z.enum(["mysql", "postgres"]),
  version: z.string(),
  service: z.string(),
});

export const applicationSchema = z.object({
  slug: z.string(),
  enabled: z.boolean(),
  domain: z.string(),
  aliases: z.array(z.string()),
  documentRoot: z.string(),
  entrypointMode: entrypointModeSchema,
  phpVersion: z.string(),
  fpmProfile: z.string(),
  tls: tlsKindSchema,
  tlsCertificatePath: z.string().optional(),
  tlsKeyPath: z.string().optional(),
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
  fpmProfiles: z.array(z.string()),
  databaseServices: z.array(databaseServiceSchema),
  defaults: z
    .object({
      phpVersion: z.string(),
      fpmProfile: z.string(),
      databaseEngine: z.enum(["mysql", "postgres"]),
      databaseService: z.string(),
    })
    .optional(),
});

export const setApplicationEnabledInputSchema = z.object({
  slug: z.string().min(1).max(128),
  enabled: z.boolean(),
});

export const saveApplicationInputSchema = z
  .object({
    slug: z.string().trim().min(1).max(63),
    domain: z.string().trim().min(1).max(253),
    aliases: z.array(z.string().trim().min(1).max(253)).default([]),
    documentRoot: z.string().trim().min(1).max(512),
    entrypointMode: entrypointModeSchema,
    phpVersion: z.string().trim().min(1).max(32),
    fpmProfile: z.string().trim().min(1).max(32),
    tls: tlsKindSchema,
    tlsCertificatePath: z.string().trim().min(1).max(4096).optional(),
    tlsKeyPath: z.string().trim().min(1).max(4096).optional(),
    accessLog: z.boolean(),
    databaseEngine: databaseEngineSchema,
    databaseService: z.string().trim().min(1).max(128).optional(),
    createDatabase: z.boolean(),
    databaseName: z.string().trim().min(1).max(128).optional(),
  })
  .superRefine((input, ctx) => {
    if (input.tls === "external") {
      if (!input.tlsCertificatePath) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["tlsCertificatePath"],
          message: "Certificate path is required for external TLS",
        });
      }
      if (!input.tlsKeyPath) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["tlsKeyPath"],
          message: "Private key path is required for external TLS",
        });
      }
    }
    if (
      (input.databaseEngine === "mysql" || input.databaseEngine === "postgres") &&
      !input.databaseService
    ) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["databaseService"],
        message: "Select a managed database service",
      });
    }
  });

export const addApplicationDatabaseInputSchema = z
  .object({
    slug: z.string().min(1).max(128),
    engine: databaseEngineSchema,
    service: z.string().trim().min(1).max(128).optional(),
    databaseName: z.string().trim().min(1).max(128).optional(),
  })
  .superRefine((input, ctx) => {
    if (input.engine === "mysql" || input.engine === "postgres") {
      if (!input.service) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["service"],
          message: "Select a managed database service",
        });
      }
      if (!input.databaseName) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["databaseName"],
          message: "Database name is required",
        });
      }
    }
  });

export const removeApplicationInputSchema = z.object({
  slug: z.string().min(1).max(128),
  confirmation: z.string().min(1).max(256),
});

export type AddApplicationDatabaseInput = z.infer<typeof addApplicationDatabaseInputSchema>;
export type Application = z.infer<typeof applicationSchema>;
export type ApplicationList = z.infer<typeof applicationListSchema>;
export type SaveApplicationInput = z.infer<typeof saveApplicationInputSchema>;
export type SetApplicationEnabledInput = z.infer<typeof setApplicationEnabledInputSchema>;
export type RemoveApplicationInput = z.infer<typeof removeApplicationInputSchema>;
