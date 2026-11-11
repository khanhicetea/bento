import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { api, ApiError, setCsrfToken } from "./client.ts";

type Call = { url: string; init: RequestInit };

const originalFetch = globalThis.fetch;
let calls: Call[] = [];

function stubFetch(respond: (url: string, init: RequestInit) => Response | Promise<Response>) {
  globalThis.fetch = (async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = String(input);
    calls.push({ url, init });
    return respond(url, init);
  }) as typeof fetch;
}

function callAt(index: number): Call {
  const call = calls[index];
  if (!call) throw new Error(`no fetch call at index ${index}`);
  return call;
}

function header(call: Call, name: string): string | undefined {
  return (call.init.headers as Record<string, string>)[name];
}

beforeEach(() => {
  calls = [];
  setCsrfToken(undefined);
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  setCsrfToken(undefined);
});

describe("api transport", () => {
  test("a 204 response resolves to undefined", async () => {
    stubFetch(() => new Response(null, { status: 204 }));
    expect(await api.session.logout()).toBeUndefined();
    expect(callAt(0).init.method).toBe("DELETE");
  });

  test("a 200 JSON response resolves to the parsed body", async () => {
    stubFetch(() => new Response(JSON.stringify({ id: "op_1" }), { status: 200 }));
    const op = await api.operations.get("op_1");
    expect(op).toEqual({ id: "op_1" } as unknown as typeof op);
    expect(callAt(0).url).toBe("/api/v1/operations/op_1");
  });

  test("a 409 error envelope rejects with an ApiError carrying its fields", async () => {
    stubFetch(
      () => new Response(JSON.stringify({ error: { code: "conflict", message: "nope", fields: [] } }), { status: 409 }),
    );
    const error = await api.operations.get("op_1").catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(409);
    expect((error as ApiError).code).toBe("conflict");
    expect((error as ApiError).message).toBe("nope");
  });

  test("a non-JSON error body falls back to the HTTP status", async () => {
    stubFetch(() => new Response("<html>Bad Gateway</html>", { status: 502 }));
    const error = await api.operations.get("op_1").catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(502);
    expect((error as ApiError).message).toBe("Request failed (HTTP 502)");
    expect((error as ApiError).code).toBe("http");
  });

  test("a network failure rejects with an ApiError of status 0", async () => {
    stubFetch(() => Promise.reject(new TypeError("failed")));
    const error = await api.operations.get("op_1").catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(0);
    expect((error as ApiError).message).toBe("The Bento backend is unreachable.");
    expect((error as ApiError).code).toBe("network");
  });

  test("an aborted request rethrows the AbortError unchanged", async () => {
    const abort = new DOMException("aborted", "AbortError");
    stubFetch(() => Promise.reject(abort));
    const error = await api.operations.get("op_1").catch((e: unknown) => e);
    expect(error).toBe(abort);
  });

  test("GET sends no CSRF token; a non-GET sends the configured one", async () => {
    stubFetch(() => new Response(JSON.stringify({}), { status: 200 }));
    setCsrfToken("csrf-123");
    await api.operations.get("op_1");
    await api.operations.cancel("op_1");
    expect(header(callAt(0), "X-CSRF-Token")).toBeUndefined();
    expect(header(callAt(1), "X-CSRF-Token")).toBe("csrf-123");
  });

  test("mutate sends an Idempotency-Key; request alone does not", async () => {
    stubFetch(() => new Response(JSON.stringify({}), { status: 202 }));
    await api.services.restart("db");
    await api.operations.cancel("op_1");
    expect(header(callAt(0), "Idempotency-Key")).toStartWith("web-");
    expect(header(callAt(1), "Idempotency-Key")).toBeUndefined();
  });
});
