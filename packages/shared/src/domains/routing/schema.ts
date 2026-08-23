import { z } from "zod";

export const routingTlsKindSchema = z.enum(["shared", "self-ca", "acme", "external"]);

export const routingProxySchema = z.object({
  name: z.string(),
  enabled: z.boolean(),
  domain: z.string(),
  aliases: z.array(z.string()),
  upstreams: z.array(z.string()),
  tls: routingTlsKindSchema,
  tlsCertificatePath: z.string().optional(),
  tlsKeyPath: z.string().optional(),
  accessLog: z.boolean(),
});

export const routingOverviewSchema = z.object({
  initialized: z.boolean(),
  stackRoot: z.string(),
  error: z.string().optional(),
  ingress: z
    .object({
      mode: z.enum(["host", "bridge"]),
      httpPort: z.number().int().optional(),
      httpsPort: z.number().int().optional(),
      http3: z.boolean(),
    })
    .optional(),
  domains: z.array(
    z.object({
      domain: z.string(),
      ownerKind: z.enum(["application", "proxy"]),
      owner: z.string(),
      primary: z.boolean(),
      tls: routingTlsKindSchema,
    }),
  ),
  proxies: z.array(routingProxySchema),
});

export const saveRoutingProxyInputSchema = z
  .object({
    operation: z.enum(["create", "update"]),
    name: z.string().trim().min(1).max(63),
    domain: z.string().trim().min(1).max(253),
    aliases: z.array(z.string().trim().min(1).max(253)).default([]),
    upstreams: z.array(z.string().trim().min(1).max(2048)).min(1).max(32),
    tls: routingTlsKindSchema,
    tlsCertificatePath: z.string().trim().min(1).max(4096).optional(),
    tlsKeyPath: z.string().trim().min(1).max(4096).optional(),
    accessLog: z.boolean(),
  })
  .superRefine((input, ctx) => {
    if (input.tls !== "external") return;
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
  });

export const setRoutingProxyEnabledInputSchema = z.object({
  name: z.string().trim().min(1).max(63),
  enabled: z.boolean(),
});

export const removeRoutingProxyInputSchema = z.object({
  name: z.string().trim().min(1).max(63),
  confirmation: z.string().min(1).max(256),
});

export type RoutingOverview = z.infer<typeof routingOverviewSchema>;
export type RoutingProxy = z.infer<typeof routingProxySchema>;
export type SaveRoutingProxyInput = z.infer<typeof saveRoutingProxyInputSchema>;
