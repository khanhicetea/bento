/**
 * Typed REST transport for /api/v1. Request/response shapes come from the
 * tygo-generated DTOs (./generated/types.ts); runtime validation happens in
 * the Go backend. Errors, credentials, CSRF, and idempotency are handled here
 * centrally so feature code never touches fetch directly.
 */
import type * as T from "./generated/types.ts";

export type { T };

export class ApiError extends Error {
  readonly status: number;
  readonly code: T.ErrorCode | "network";
  readonly fields: T.FieldError[];

  constructor(status: number, body: T.ErrorBody | null, fallback: string) {
    super(body?.message ?? fallback);
    this.status = status;
    this.code = body?.code ?? "network";
    this.fields = body?.fields ?? [];
  }
}

// The CSRF token is a transport credential issued with the session; it is
// kept here rather than in component state.
let csrfToken = "";

export function setCsrfToken(token: string | undefined) {
  csrfToken = token ?? "";
}

function idempotencyKey(): string {
  return `web-${crypto.randomUUID()}`;
}

type RequestOptions = { signal?: AbortSignal; idempotent?: boolean };

async function request<R>(method: string, path: string, body?: unknown, options: RequestOptions = {}): Promise<R> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (method !== "GET") {
    headers["X-CSRF-Token"] = csrfToken;
    if (options.idempotent) headers["Idempotency-Key"] = idempotencyKey();
  }
  let response: Response;
  try {
    response = await fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: "same-origin",
      signal: options.signal,
    });
  } catch (cause) {
    if (cause instanceof DOMException && cause.name === "AbortError") throw cause;
    throw new ApiError(0, null, "The Bento backend is unreachable.");
  }
  if (response.status === 204) return undefined as R;
  const text = await response.text();
  let parsed: unknown = null;
  try {
    parsed = text ? JSON.parse(text) : null;
  } catch {
    parsed = null;
  }
  if (!response.ok) {
    const envelope = parsed as T.ErrorResponse | null;
    throw new ApiError(response.status, envelope?.error ?? null, `Request failed (HTTP ${response.status})`);
  }
  return parsed as R;
}

const get = <R>(path: string, signal?: AbortSignal) => request<R>("GET", path, undefined, { signal });
const mutate = <R = T.Accepted>(method: string, path: string, body: unknown = {}) =>
  request<R>(method, path, body, { idempotent: true });

const enc = encodeURIComponent;

export const api = {
  session: {
    get: (signal?: AbortSignal) => get<T.Session>("/api/v1/session", signal),
    login: (password: string) => request<T.Session>("POST", "/api/v1/session", { password } satisfies T.LoginRequest),
    logout: () => request<void>("DELETE", "/api/v1/session"),
  },
  system: {
    status: (signal?: AbortSignal) => get<T.SystemStatus>("/api/v1/system", signal),
    catalog: (signal?: AbortSignal) => get<T.Catalog>("/api/v1/catalog", signal),
    docker: (signal?: AbortSignal) => get<T.DockerInventory>("/api/v1/system/docker", signal),
    reconcile: (signal?: AbortSignal) => get<T.ReconcileStatus>("/api/v1/reconcile", signal),
    pruneImage: (id: string, confirm: string) =>
      mutate("POST", `/api/v1/system/docker/images/${enc(id.replace(/^sha256:/, ""))}/prune`, {
        confirm,
      } satisfies T.ConfirmRequest),
  },
  apps: {
    list: (signal?: AbortSignal) => get<T.AppList>("/api/v1/apps", signal),
    get: (id: string, signal?: AbortSignal) => get<T.App>(`/api/v1/apps/${enc(id)}`, signal),
    create: (body: T.CreateAppRequest) => mutate("POST", "/api/v1/apps", body),
    update: (id: string, body: T.UpdateAppRequest) => mutate("PATCH", `/api/v1/apps/${enc(id)}`, body),
    action: (id: string, action: "start" | "stop" | "restart" | "publish" | "unpublish") =>
      mutate("POST", `/api/v1/apps/${enc(id)}/${action}`),
    remove: (id: string, confirm: string) =>
      mutate("DELETE", `/api/v1/apps/${enc(id)}`, { confirm } satisfies T.ConfirmRequest),
    addBinding: (id: string, body: T.BindingRequest) => mutate("POST", `/api/v1/apps/${enc(id)}/bindings`, body),
    addDatabase: (id: string, bindingId: string, name: string) =>
      mutate("POST", `/api/v1/apps/${enc(id)}/bindings/${enc(bindingId)}/databases`, {
        name,
      } satisfies T.AddDatabaseRequest),
    permissions: (id: string, mode: string) =>
      mutate("POST", `/api/v1/apps/${enc(id)}/permissions`, { mode } satisfies T.PermissionsRequest),
    metrics: (id: string, signal?: AbortSignal) => get<T.AppMetrics>(`/api/v1/apps/${enc(id)}/metrics`, signal),
    git: (id: string, signal?: AbortSignal) => get<T.GitSource>(`/api/v1/apps/${enc(id)}/git`, signal),
    setGit: (id: string, body: T.GitSourceRequest) => request<T.GitSource>("PUT", `/api/v1/apps/${enc(id)}/git`, body),
    removeGit: (id: string) => request<T.GitSource>("DELETE", `/api/v1/apps/${enc(id)}/git`),
    deploy: (id: string) => mutate("POST", `/api/v1/apps/${enc(id)}/deploy`),
    webhook: (id: string, signal?: AbortSignal) => get<T.Webhook>(`/api/v1/apps/${enc(id)}/webhook`, signal),
    enableWebhook: (id: string) => request<T.WebhookSecret>("POST", `/api/v1/apps/${enc(id)}/webhook`, {}),
    disableWebhook: (id: string) => request<T.Webhook>("DELETE", `/api/v1/apps/${enc(id)}/webhook`),
    logsUrl: (id: string, tail: number, follow: boolean) =>
      `/api/v1/apps/${enc(id)}/logs?tail=${tail}${follow ? "&follow=1" : ""}`,
    terminalPath: (id: string, mode: "tool" | "running") => `/api/v1/apps/${enc(id)}/terminal?mode=${mode}`,
  },
  operations: {
    list: (target?: string, signal?: AbortSignal) =>
      get<T.OperationList>(`/api/v1/operations?limit=100${target ? `&target=${enc(target)}` : ""}`, signal),
    get: (id: string, signal?: AbortSignal) => get<T.Operation>(`/api/v1/operations/${enc(id)}`, signal),
    cancel: (id: string) => request<T.Operation>("POST", `/api/v1/operations/${enc(id)}/cancel`, {}),
  },
  services: {
    list: (signal?: AbortSignal) => get<T.ServiceList>("/api/v1/services", signal),
    create: (body: T.CreateServiceRequest) => mutate("POST", "/api/v1/services", body),
  },
  edge: {
    get: (signal?: AbortSignal) => get<T.EdgeStatus>("/api/v1/edge", signal),
    set: (body: T.EdgeSettings) => mutate("PUT", "/api/v1/edge", body),
    metrics: (signal?: AbortSignal) => get<T.EdgeMetrics>("/api/v1/edge/metrics", signal),
  },
  utils: {
    get: (signal?: AbortSignal) => get<T.UtilsSettings>("/api/v1/utils", signal),
    set: (body: T.UtilsSettingsRequest) => request<T.UtilsSettings>("PUT", "/api/v1/utils", body),
  },
  dbadmin: {
    get: (signal?: AbortSignal) => get<T.DBAdminStatus>("/api/v1/dbadmin", signal),
    set: (enabled: boolean) => mutate("PUT", "/api/v1/dbadmin", { enabled } satisfies T.DBAdminSettingsRequest),
    ticket: (appId: string, bindingId: string) =>
      request<T.DBAdminTicket>("POST", `/api/v1/apps/${enc(appId)}/bindings/${enc(bindingId)}/dbadmin`, {}),
  },
  tunnel: {
    get: (signal?: AbortSignal) => get<T.TunnelStatus>("/api/v1/tunnel", signal),
    setToken: (token: string) => mutate("PUT", "/api/v1/tunnel/token", { token } satisfies T.SetTunnelTokenRequest),
  },
  proxies: {
    list: (signal?: AbortSignal) => get<T.ProxyList>("/api/v1/proxies", signal),
    upsert: (body: T.ProxyRequest) => mutate("POST", "/api/v1/proxies", body),
    remove: (name: string, confirm: string) =>
      mutate("DELETE", `/api/v1/proxies/${enc(name)}`, { confirm } satisfies T.ConfirmRequest),
  },
  retired: {
    list: (signal?: AbortSignal) => get<T.RetiredList>("/api/v1/retired", signal),
    prune: (appId: string, confirm: string) =>
      mutate("POST", `/api/v1/retired/${enc(appId)}/prune`, { confirm } satisfies T.ConfirmRequest),
  },
  backups: {
    artifacts: (signal?: AbortSignal) => get<T.BackupArtifactList>("/api/v1/backups/artifacts", signal),
    runs: (signal?: AbortSignal) => get<T.BackupRunList>("/api/v1/backups/runs", signal),
    run: (body: T.BackupRequest) => mutate("POST", "/api/v1/backups", body),
    restore: (body: T.RestoreRequest) => mutate("POST", "/api/v1/backups/restore", body),
    remove: (body: T.BackupDeleteRequest) => mutate("POST", "/api/v1/backups/delete", body),
    downloadUrl: (path: string) => `/api/v1/backups/download?path=${enc(path)}`,
    schedules: (signal?: AbortSignal) => get<T.BackupScheduleList>("/api/v1/backups/schedules", signal),
    saveSchedule: (body: T.BackupSchedule) =>
      body.id
        ? request<T.BackupSchedule>("PUT", `/api/v1/backups/schedules/${enc(body.id)}`, body)
        : request<T.BackupSchedule>("POST", "/api/v1/backups/schedules", body),
    enableSchedule: (id: string, enabled: boolean) =>
      request<T.BackupSchedule>("POST", `/api/v1/backups/schedules/${enc(id)}/enabled`, {
        enabled,
      } satisfies T.BackupScheduleEnable),
    removeSchedule: (id: string) => request<void>("DELETE", `/api/v1/backups/schedules/${enc(id)}`),
    rclone: (signal?: AbortSignal) => get<T.RcloneStatus>("/api/v1/backups/rclone", signal),
    rcloneTest: (body: T.RcloneTestRequest) => mutate("POST", "/api/v1/backups/rclone/test", body),
    rcloneTerminalPath: "/api/v1/backups/rclone/terminal",
  },
  /** WebSocket URL of a terminal endpoint path (see TerminalView). */
  terminalUrl: (path: string, cols: number, rows: number, session?: string) => {
    const scheme = location.protocol === "https:" ? "wss" : "ws";
    const url = new URL(path, location.origin);
    url.protocol = `${scheme}:`;
    url.searchParams.set("cols", String(cols));
    url.searchParams.set("rows", String(rows));
    url.searchParams.set("csrf", csrfToken);
    if (session) url.searchParams.set("session", session);
    return url.toString();
  },
};

export function messageOf(error: unknown): string {
  if (error instanceof ApiError && error.fields.length > 0) {
    return `${error.message}: ${error.fields.map((f) => `${f.field} ${f.message}`).join("; ")}`;
  }
  return error instanceof Error ? error.message : String(error);
}
