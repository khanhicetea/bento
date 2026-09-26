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

export const processLanguageSchema = z.enum(["node", "bun", "python"]);
export const processRuntimeSchema = z.object({
  language: processLanguageSchema,
  version: z.string(),
  image: z.string(),
  service: z.string(),
  internalPort: z.number().int().min(1024).max(65535),
  command: z.array(z.string()),
  workdir: z.string(),
  healthPath: z.string().optional(),
});

export const applicationSchema = z.object({
  slug: z.string(),
  kind: z.enum(["php", "process"]),
  enabled: z.boolean(),
  domain: z.string(),
  aliases: z.array(z.string()),
  documentRoot: z.string().optional(),
  entrypointMode: entrypointModeSchema.optional(),
  phpVersion: z.string().optional(),
  fpmProfile: z.string().optional(),
  processRuntime: processRuntimeSchema.optional(),
  tls: tlsKindSchema,
  tlsCertificatePath: z.string().optional(),
  tlsKeyPath: z.string().optional(),
  accessLog: z.boolean(),
  deployEnabled: z.boolean(),
  deploySummary: z
    .object({
      queuePolicy: z.enum(["latest", "fifo"]),
      timeoutSec: z.number().int().positive(),
      command: z.string(),
    })
    .optional(),
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

export const applicationPublicKeyInputSchema = z.object({
  slug: z.string().min(1).max(128),
});

export const applicationPublicKeySchema = z.object({
  publicKey: z.string().startsWith("ssh-ed25519 "),
});

export const applicationDatabaseCredentialsInputSchema = z.object({
  slug: z.string().min(1).max(128),
  engine: z.enum(["mysql", "postgres"]),
  service: z.string().trim().min(1).max(128),
});

export const applicationDatabaseCredentialsSchema = z.object({
  engine: z.enum(["mysql", "postgres"]),
  host: z.string(),
  port: z.number().int().positive(),
  user: z.string(),
  password: z.string(),
  databases: z.array(z.string()),
});

export const setApplicationEnabledInputSchema = z.object({
  slug: z.string().min(1).max(128),
  enabled: z.boolean(),
});

export const setApplicationRunningInputSchema = z.object({
  slug: z.string().min(1).max(128),
  action: z.enum(["start", "stop"]),
});

export const saveApplicationInputSchema = z
  .object({
    slug: z.string().trim().min(1).max(63),
    kind: z.enum(["php", "process"]).default("php"),
    domain: z.string().trim().min(1).max(253),
    aliases: z.array(z.string().trim().min(1).max(253)).default([]),
    documentRoot: z.string().trim().min(1).max(512).optional(),
    entrypointMode: entrypointModeSchema.optional(),
    phpVersion: z.string().trim().min(1).max(32).optional(),
    fpmProfile: z.string().trim().min(1).max(32).optional(),
    processLanguage: processLanguageSchema.optional(),
    processVersion: z
      .string()
      .trim()
      .regex(/^[0-9]+(?:\.[0-9]+){0,2}$/)
      .optional(),
    processCommand: z.array(z.string().min(1).max(4096)).min(1).optional(),
    processWorkdir: z.string().trim().min(1).max(512).optional(),
    processPort: z.number().int().min(1024).max(65535).optional(),
    processHealthPath: z
      .string()
      .trim()
      .regex(/^\/[^\r\n]*$/)
      .optional(),
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
    if (input.kind === "php") {
      if (
        input.processLanguage ||
        input.processVersion ||
        input.processCommand ||
        input.processWorkdir ||
        input.processPort !== undefined ||
        input.processHealthPath
      ) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["kind"],
          message: "Process runtime fields cannot be combined with PHP",
        });
      }
      for (const [field, value] of [
        ["documentRoot", input.documentRoot],
        ["entrypointMode", input.entrypointMode],
        ["phpVersion", input.phpVersion],
        ["fpmProfile", input.fpmProfile],
      ] as const) {
        if (!value) {
          ctx.addIssue({ code: z.ZodIssueCode.custom, path: [field], message: "Required" });
        }
      }
    } else {
      if (input.documentRoot || input.entrypointMode || input.phpVersion || input.fpmProfile) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["kind"],
          message: "PHP runtime fields cannot be combined with a process runtime",
        });
      }
      for (const [field, value] of [
        ["processLanguage", input.processLanguage],
        ["processVersion", input.processVersion],
        ["processCommand", input.processCommand],
      ] as const) {
        if (!value) {
          ctx.addIssue({ code: z.ZodIssueCode.custom, path: [field], message: "Required" });
        }
      }
    }
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
    if ((input.databaseEngine === "mysql" || input.databaseEngine === "postgres") && !input.databaseService) {
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
export type ApplicationDatabaseCredentials = z.infer<typeof applicationDatabaseCredentialsSchema>;
export type ApplicationPublicKey = z.infer<typeof applicationPublicKeySchema>;
export type Application = z.infer<typeof applicationSchema>;
export type ApplicationList = z.infer<typeof applicationListSchema>;
export type SaveApplicationInput = z.infer<typeof saveApplicationInputSchema>;
export type SetApplicationEnabledInput = z.infer<typeof setApplicationEnabledInputSchema>;
export type SetApplicationRunningInput = z.infer<typeof setApplicationRunningInputSchema>;
export type RemoveApplicationInput = z.infer<typeof removeApplicationInputSchema>;
