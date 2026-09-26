import { z } from "zod";

export const schedulerAccessSchema = z.object({
  enabled: z.boolean(),
  reason: z.string().optional(),
  schedulers: z.array(
    z.object({
      app: z.string(),
      path: z.string().regex(/^\/scheduler\/apps\/[a-z0-9][a-z0-9-]{0,62}\/$/),
    }),
  ),
});

export type SchedulerAccessInfo = z.infer<typeof schedulerAccessSchema>;
