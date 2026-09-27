/** Explicit React Query key factories, one namespace per domain. */
export const keys = {
  session: ["session"] as const,
  system: ["system"] as const,
  catalog: ["catalog"] as const,
  apps: {
    all: ["apps"] as const,
    list: () => ["apps", "list"] as const,
    detail: (id: string) => ["apps", "detail", id] as const,
  },
  operations: {
    all: ["operations"] as const,
    lists: ["operations", "list"] as const,
    list: (target?: string) => ["operations", "list", target ?? "all"] as const,
    detail: (id: string) => ["operations", "detail", id] as const,
  },
  services: ["services"] as const,
  edge: ["edge"] as const,
  tunnel: ["tunnel"] as const,
  proxies: ["proxies"] as const,
  retired: ["retired"] as const,
  backups: {
    all: ["backups"] as const,
    artifacts: ["backups", "artifacts"] as const,
    runs: ["backups", "runs"] as const,
    schedule: ["backups", "schedule"] as const,
  },
};
