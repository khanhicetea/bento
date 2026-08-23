import { z } from "zod";

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
      tls: z.enum(["shared", "self-ca", "acme", "external"]),
    }),
  ),
  proxies: z.array(
    z.object({
      name: z.string(),
      domain: z.string(),
      aliases: z.array(z.string()),
      upstreams: z.array(z.string()),
      tls: z.enum(["shared", "self-ca", "acme", "external"]),
      accessLog: z.boolean(),
    }),
  ),
});

export type RoutingOverview = z.infer<typeof routingOverviewSchema>;
