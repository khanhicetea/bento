import { ApiError } from "../api/client.ts";

/**
 * Query retry rule. Client errors (4xx) are final: retrying cannot change
 * them. Everything else is worth two more tries: server errors (5xx), an
 * unreachable backend (status 0: SSH tunnel blip, backend restart), and
 * non-API errors.
 */
export function shouldRetryQuery(failureCount: number, error: unknown): boolean {
  if (error instanceof ApiError && error.status >= 400 && error.status < 500) return false;
  return failureCount < 2;
}
