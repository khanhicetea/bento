import { z } from "zod";

export const healthSchema = z.object({
  ok: z.literal(true),
  service: z.literal("bento"),
});

export type Health = z.infer<typeof healthSchema>;
