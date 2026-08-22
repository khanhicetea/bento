// node_modules/.bun/@orpc+shared@1.15.0/node_modules/@orpc/shared/dist/index.mjs
function resolveMaybeOptionalOptions(rest) {
  return rest[0] ?? {};
}
function toArray(value) {
  return Array.isArray(value) ? value : value === undefined || value === null ? [] : [value];
}
var ORPC_NAME = "orpc";
var ORPC_SHARED_PACKAGE_NAME = "@orpc/shared";
var ORPC_SHARED_PACKAGE_VERSION = "1.15.0";

class AbortError extends Error {
  constructor(...rest) {
    super(...rest);
    this.name = "AbortError";
  }
}
function once(fn) {
  let cached;
  return () => {
    if (cached) {
      return cached.result;
    }
    const result = fn();
    cached = { result };
    return result;
  };
}
function sequential(fn) {
  let lastOperationPromise = Promise.resolve();
  return (...args) => {
    return lastOperationPromise = lastOperationPromise.catch(() => {}).then(() => {
      return fn(...args);
    });
  };
}
var SPAN_ERROR_STATUS = 2;
var GLOBAL_OTEL_CONFIG_KEY = `__${ORPC_SHARED_PACKAGE_NAME}@${ORPC_SHARED_PACKAGE_VERSION}/otel/config__`;
function getGlobalOtelConfig() {
  return globalThis[GLOBAL_OTEL_CONFIG_KEY];
}
function startSpan(name, options = {}, context) {
  const tracer = getGlobalOtelConfig()?.tracer;
  return tracer?.startSpan(name, options, context);
}
function setSpanError(span, error, options = {}) {
  if (!span) {
    return;
  }
  const exception = toOtelException(error);
  span.recordException(exception);
  if (!options.signal?.aborted || options.signal.reason !== error) {
    span.setStatus({
      code: SPAN_ERROR_STATUS,
      message: exception.message
    });
  }
}
function toOtelException(error) {
  if (error instanceof Error) {
    const exception = {
      message: error.message,
      name: error.name,
      stack: error.stack
    };
    if ("code" in error && (typeof error.code === "string" || typeof error.code === "number")) {
      exception.code = error.code;
    }
    return exception;
  }
  return { message: String(error) };
}
async function runWithSpan({ name, context, ...options }, fn) {
  const tracer = getGlobalOtelConfig()?.tracer;
  if (!tracer) {
    return fn();
  }
  const callback = async (span) => {
    try {
      return await fn(span);
    } catch (e) {
      setSpanError(span, e, options);
      throw e;
    } finally {
      span.end();
    }
  };
  if (context) {
    return tracer.startActiveSpan(name, options, context, callback);
  } else {
    return tracer.startActiveSpan(name, options, callback);
  }
}
async function runInSpanContext(span, fn) {
  const otelConfig = getGlobalOtelConfig();
  if (!span || !otelConfig) {
    return fn();
  }
  const ctx = otelConfig.trace.setSpan(otelConfig.context.active(), span);
  return otelConfig.context.with(ctx, fn);
}
function isAsyncIteratorObject(maybe) {
  if (!maybe || typeof maybe !== "object") {
    return false;
  }
  return "next" in maybe && typeof maybe.next === "function" && Symbol.asyncIterator in maybe && typeof maybe[Symbol.asyncIterator] === "function";
}
var fallbackAsyncDisposeSymbol = Symbol.for("asyncDispose");
var asyncDisposeSymbol = Symbol.asyncDispose ?? fallbackAsyncDisposeSymbol;

class AsyncIteratorClass {
  #isDone = false;
  #isExecuteComplete = false;
  #cleanup;
  #next;
  constructor(next, cleanup) {
    this.#cleanup = cleanup;
    this.#next = sequential(async () => {
      if (this.#isDone) {
        return { done: true, value: undefined };
      }
      try {
        const result = await next();
        if (result.done) {
          this.#isDone = true;
        }
        return result;
      } catch (err) {
        this.#isDone = true;
        throw err;
      } finally {
        if (this.#isDone && !this.#isExecuteComplete) {
          this.#isExecuteComplete = true;
          await this.#cleanup("next");
        }
      }
    });
  }
  next() {
    return this.#next();
  }
  async return(value) {
    this.#isDone = true;
    if (!this.#isExecuteComplete) {
      this.#isExecuteComplete = true;
      await this.#cleanup("return");
    }
    return { done: true, value };
  }
  async throw(err) {
    this.#isDone = true;
    if (!this.#isExecuteComplete) {
      this.#isExecuteComplete = true;
      await this.#cleanup("throw");
    }
    throw err;
  }
  async[asyncDisposeSymbol]() {
    this.#isDone = true;
    if (!this.#isExecuteComplete) {
      this.#isExecuteComplete = true;
      await this.#cleanup("dispose");
    }
  }
  [Symbol.asyncIterator]() {
    return this;
  }
}
function asyncIteratorWithSpan({ name, ...options }, iterator) {
  let span;
  return new AsyncIteratorClass(async () => {
    span ??= startSpan(name);
    try {
      const result = await runInSpanContext(span, () => iterator.next());
      span?.addEvent(result.done ? "completed" : "yielded");
      return result;
    } catch (err) {
      setSpanError(span, err, options);
      throw err;
    }
  }, async (reason) => {
    try {
      if (reason !== "next") {
        await runInSpanContext(span, () => iterator.return?.());
      }
    } catch (err) {
      setSpanError(span, err, options);
      throw err;
    } finally {
      span?.end();
    }
  });
}
function intercept(interceptors, options, main) {
  const next = (options2, index) => {
    const interceptor = interceptors[index];
    if (!interceptor) {
      return main(options2);
    }
    return interceptor({
      ...options2,
      next: (newOptions = options2) => next(newOptions, index + 1)
    });
  };
  return next(options, 0);
}
function parseEmptyableJSON(text) {
  if (!text) {
    return;
  }
  return JSON.parse(text);
}
function stringifyJSON(value) {
  return JSON.stringify(value);
}
function getConstructor(value) {
  if (!isTypescriptObject(value)) {
    return null;
  }
  return Object.getPrototypeOf(value)?.constructor;
}
function isObject(value) {
  if (!value || typeof value !== "object") {
    return false;
  }
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || !proto || !proto.constructor;
}
function isTypescriptObject(value) {
  return !!value && (typeof value === "object" || typeof value === "function");
}
function value(value2, ...args) {
  if (typeof value2 === "function") {
    return value2(...args);
  }
  return value2;
}
function preventNativeAwait(target) {
  return new Proxy(target, {
    get(target2, prop, receiver) {
      const value2 = Reflect.get(target2, prop, receiver);
      if (prop !== "then" || typeof value2 !== "function") {
        return value2;
      }
      return new Proxy(value2, {
        apply(targetFn, thisArg, args) {
          if (args.length !== 2 || args.some((arg) => !isNativeFunction(arg))) {
            return Reflect.apply(targetFn, thisArg, args);
          }
          let shouldOmit = true;
          args[0].call(thisArg, preventNativeAwait(new Proxy(target2, {
            get: (target3, prop2, receiver2) => {
              if (shouldOmit && prop2 === "then") {
                shouldOmit = false;
                return;
              }
              return Reflect.get(target3, prop2, receiver2);
            }
          })));
        }
      });
    }
  });
}
var NATIVE_FUNCTION_REGEX = /^\s*function\s*\(\)\s*\{\s*\[native code\]\s*\}\s*$/;
function isNativeFunction(fn) {
  return typeof fn === "function" && NATIVE_FUNCTION_REGEX.test(fn.toString());
}
function tryDecodeURIComponent(value2) {
  try {
    return decodeURIComponent(value2);
  } catch {
    return value2;
  }
}

// node_modules/.bun/@orpc+client@1.15.0/node_modules/@orpc/client/dist/shared/client.CZlviB0y.mjs
var ORPC_CLIENT_PACKAGE_NAME = "@orpc/client";
var ORPC_CLIENT_PACKAGE_VERSION = "1.15.0";
var RECURSIVE_CLIENT_UNWRAP_KEYS = /* @__PURE__ */ new Set([
  "bind",
  "valueOf",
  "toString",
  "toJSON"
]);
var COMMON_ORPC_ERROR_DEFS = {
  BAD_REQUEST: {
    status: 400,
    message: "Bad Request"
  },
  UNAUTHORIZED: {
    status: 401,
    message: "Unauthorized"
  },
  FORBIDDEN: {
    status: 403,
    message: "Forbidden"
  },
  NOT_FOUND: {
    status: 404,
    message: "Not Found"
  },
  METHOD_NOT_SUPPORTED: {
    status: 405,
    message: "Method Not Supported"
  },
  NOT_ACCEPTABLE: {
    status: 406,
    message: "Not Acceptable"
  },
  TIMEOUT: {
    status: 408,
    message: "Request Timeout"
  },
  CONFLICT: {
    status: 409,
    message: "Conflict"
  },
  PRECONDITION_FAILED: {
    status: 412,
    message: "Precondition Failed"
  },
  PAYLOAD_TOO_LARGE: {
    status: 413,
    message: "Payload Too Large"
  },
  UNSUPPORTED_MEDIA_TYPE: {
    status: 415,
    message: "Unsupported Media Type"
  },
  UNPROCESSABLE_CONTENT: {
    status: 422,
    message: "Unprocessable Content"
  },
  TOO_MANY_REQUESTS: {
    status: 429,
    message: "Too Many Requests"
  },
  CLIENT_CLOSED_REQUEST: {
    status: 499,
    message: "Client Closed Request"
  },
  INTERNAL_SERVER_ERROR: {
    status: 500,
    message: "Internal Server Error"
  },
  NOT_IMPLEMENTED: {
    status: 501,
    message: "Not Implemented"
  },
  BAD_GATEWAY: {
    status: 502,
    message: "Bad Gateway"
  },
  SERVICE_UNAVAILABLE: {
    status: 503,
    message: "Service Unavailable"
  },
  GATEWAY_TIMEOUT: {
    status: 504,
    message: "Gateway Timeout"
  }
};
function fallbackORPCErrorStatus(code, status) {
  return status ?? COMMON_ORPC_ERROR_DEFS[code]?.status ?? 500;
}
function fallbackORPCErrorMessage(code, message) {
  return message || COMMON_ORPC_ERROR_DEFS[code]?.message || code;
}
var globalORPCErrorConstructors;

class ORPCError extends Error {
  defined;
  code;
  status;
  data;
  static {
    const GLOBAL_ORPC_ERROR_CONSTRUCTORS_SYMBOL = Symbol.for(`__${ORPC_CLIENT_PACKAGE_NAME}@${ORPC_CLIENT_PACKAGE_VERSION}/error/ORPC_ERROR_CONSTRUCTORS__`);
    globalThis[GLOBAL_ORPC_ERROR_CONSTRUCTORS_SYMBOL] ??= /* @__PURE__ */ new WeakSet;
    globalORPCErrorConstructors = globalThis[GLOBAL_ORPC_ERROR_CONSTRUCTORS_SYMBOL];
    globalORPCErrorConstructors.add(ORPCError);
  }
  constructor(code, ...rest) {
    const options = resolveMaybeOptionalOptions(rest);
    if (options.status !== undefined && !isORPCErrorStatus(options.status)) {
      throw new Error("[ORPCError] Invalid error status code.");
    }
    const message = fallbackORPCErrorMessage(code, options.message);
    super(message, options);
    this.code = code;
    this.status = fallbackORPCErrorStatus(code, options.status);
    this.defined = options.defined ?? false;
    this.data = options.data;
  }
  toJSON() {
    return {
      defined: this.defined,
      code: this.code,
      status: this.status,
      message: this.message,
      data: this.data
    };
  }
  static [Symbol.hasInstance](instance) {
    if (globalORPCErrorConstructors.has(this)) {
      const constructor = getConstructor(instance);
      if (constructor && globalORPCErrorConstructors.has(constructor)) {
        return true;
      }
    }
    return super[Symbol.hasInstance](instance);
  }
}
function toORPCError(error) {
  return error instanceof ORPCError ? error : new ORPCError("INTERNAL_SERVER_ERROR", {
    message: "Internal server error",
    cause: error
  });
}
function isORPCErrorStatus(status) {
  return status < 200 || status >= 400;
}
function isORPCErrorJson(json) {
  if (!isObject(json)) {
    return false;
  }
  const validKeys = ["defined", "code", "status", "message", "data"];
  if (Object.keys(json).some((k) => !validKeys.includes(k))) {
    return false;
  }
  return "defined" in json && typeof json.defined === "boolean" && "code" in json && typeof json.code === "string" && "status" in json && typeof json.status === "number" && isORPCErrorStatus(json.status) && "message" in json && typeof json.message === "string";
}
function createORPCErrorFromJson(json, options = {}) {
  return new ORPCError(json.code, {
    ...options,
    ...json
  });
}

// node_modules/.bun/@orpc+standard-server@1.15.0/node_modules/@orpc/standard-server/dist/index.mjs
class EventEncoderError extends TypeError {
}

class EventDecoderError extends TypeError {
}

class ErrorEvent extends Error {
  data;
  constructor(options) {
    super(options?.message ?? "An error event was received", options);
    this.data = options?.data;
  }
}
var LINE_ENDING_REGEX$1 = /\r\n|\r(?!\n)|\n/;
var MESSAGE_DELIMITER_REGEX = /(?:\r\n|\r(?!\n)|\n){2}/;
var MESSAGE_DELIMITER_GLOBAL_REGEX = /(?:\r\n|\r(?!\n)|\n){2}/g;
var CR = 13;
var LF = 10;
var SPACE = 32;
function decodeEventMessage(encoded) {
  const message = {
    data: undefined,
    event: undefined,
    id: undefined,
    retry: undefined,
    comments: []
  };
  for (const line of encoded.split(LINE_ENDING_REGEX$1)) {
    if (line === "") {
      continue;
    }
    const index = line.indexOf(":");
    const value2 = index === -1 ? "" : line.slice(line.charCodeAt(index + 1) === SPACE ? index + 2 : index + 1);
    if (index === 0) {
      message.comments.push(value2);
      continue;
    }
    switch (index === -1 ? line : line.slice(0, index)) {
      case "data":
        message.data = message.data === undefined ? value2 : `${message.data}
${value2}`;
        break;
      case "event":
        message.event = value2;
        break;
      case "id":
        message.id = value2;
        break;
      case "retry": {
        const maybeInteger = Number.parseInt(value2, 10);
        if (maybeInteger >= 0 && maybeInteger.toString() === value2) {
          message.retry = maybeInteger;
        }
        break;
      }
    }
  }
  return message;
}

class EventDecoder {
  constructor(options = {}) {
    this.options = options;
  }
  pending = [];
  tail = "";
  discardLeadingLF = false;
  feed(chunk) {
    if (chunk === "") {
      return;
    }
    if (this.discardLeadingLF) {
      this.discardLeadingLF = false;
      if (chunk.charCodeAt(0) === LF) {
        chunk = chunk.slice(1);
        if (chunk === "") {
          return;
        }
      }
    }
    const scan = this.tail + chunk;
    if (!MESSAGE_DELIMITER_REGEX.test(scan)) {
      this.pending.push(chunk);
      this.tail = scan.slice(-3);
      return;
    }
    this.pending.push(chunk);
    const buffered = this.pending.length === 1 ? chunk : this.pending.join("");
    const offset = buffered.length - scan.length;
    const parts = [];
    let start = 0;
    for (const match of scan.matchAll(MESSAGE_DELIMITER_GLOBAL_REGEX)) {
      parts.push(buffered.slice(start, offset + match.index));
      start = offset + match.index + match[0].length;
    }
    const incomplete = buffered.slice(start);
    this.pending.length = 0;
    this.tail = incomplete.slice(-3);
    if (incomplete === "") {
      this.discardLeadingLF = chunk.charCodeAt(chunk.length - 1) === CR;
    } else {
      this.pending.push(incomplete);
    }
    for (const encoded of parts) {
      const message = decodeEventMessage(encoded);
      if (this.options.onEvent) {
        this.options.onEvent(message);
      }
    }
  }
  end() {
    if (this.pending.length !== 0) {
      throw new EventDecoderError("Event Iterator ended before complete");
    }
  }
}

class EventDecoderStream extends TransformStream {
  constructor() {
    let decoder;
    super({
      start(controller) {
        decoder = new EventDecoder({
          onEvent: (event) => {
            controller.enqueue(event);
          }
        });
      },
      transform(chunk) {
        decoder.feed(chunk);
      },
      flush() {
        decoder.end();
      }
    });
  }
}
var LINE_ENDING_REGEX = /\r\n|[\n\r]/;
var LINE_ENDING_GLOBAL_REGEX = /\r\n|[\n\r]/g;
function containsLineBreak(value2) {
  return LINE_ENDING_REGEX.test(value2);
}
function assertEventId(id) {
  if (containsLineBreak(id)) {
    throw new EventEncoderError("Event's id must not contain a carriage return or newline character");
  }
}
function assertEventName(event) {
  if (containsLineBreak(event)) {
    throw new EventEncoderError("Event's event must not contain a carriage return or newline character");
  }
}
function assertEventRetry(retry) {
  if (!Number.isInteger(retry) || retry < 0) {
    throw new EventEncoderError("Event's retry must be a integer and >= 0");
  }
}
function assertEventComment(comment) {
  if (containsLineBreak(comment)) {
    throw new EventEncoderError("Event's comment must not contain a carriage return or newline character");
  }
}
function encodeEventData(data) {
  if (data === undefined) {
    return "";
  }
  return `data: ${data.replace(LINE_ENDING_GLOBAL_REGEX, `
data: `)}
`;
}
function encodeEventComments(comments) {
  let output = "";
  for (const comment of comments ?? []) {
    assertEventComment(comment);
    output += `: ${comment}
`;
  }
  return output;
}
function encodeEventMessage(message) {
  let output = "";
  output += encodeEventComments(message.comments);
  if (message.event !== undefined) {
    assertEventName(message.event);
    output += `event: ${message.event}
`;
  }
  if (message.retry !== undefined) {
    assertEventRetry(message.retry);
    output += `retry: ${message.retry}
`;
  }
  if (message.id !== undefined) {
    assertEventId(message.id);
    output += `id: ${message.id}
`;
  }
  output += encodeEventData(message.data);
  output += `
`;
  return output;
}
var EVENT_SOURCE_META_SYMBOL = Symbol("ORPC_EVENT_SOURCE_META");
function withEventMeta(container, meta) {
  if (meta.id === undefined && meta.retry === undefined && !meta.comments?.length) {
    return container;
  }
  if (meta.id !== undefined) {
    assertEventId(meta.id);
  }
  if (meta.retry !== undefined) {
    assertEventRetry(meta.retry);
  }
  if (meta.comments !== undefined) {
    for (const comment of meta.comments) {
      assertEventComment(comment);
    }
  }
  return new Proxy(container, {
    get(target, prop, receiver) {
      if (prop === EVENT_SOURCE_META_SYMBOL) {
        return meta;
      }
      return Reflect.get(target, prop, receiver);
    }
  });
}
function getEventMeta(container) {
  return isTypescriptObject(container) ? Reflect.get(container, EVENT_SOURCE_META_SYMBOL) : undefined;
}
function generateContentDisposition(filename, disposition = "inline") {
  const encodedFileName = filename.replace(/[^\x20-\x7E]/g, "_").replace(/"/g, "\\\"");
  const encodedFilenameStar = encodeURIComponent(filename).replace(/['()*]/g, (c) => `%${c.charCodeAt(0).toString(16).toUpperCase()}`).replace(/%(7C|60|5E)/g, (str, hex) => String.fromCharCode(Number.parseInt(hex, 16)));
  return `${disposition}; filename="${encodedFileName}"; filename*=utf-8''${encodedFilenameStar}`;
}
function getFilenameFromContentDisposition(contentDisposition) {
  const encodedFilenameStarMatch = contentDisposition.match(/filename\*=(UTF-8'')?([^;]*)/i);
  if (encodedFilenameStarMatch && typeof encodedFilenameStarMatch[2] === "string") {
    return tryDecodeURIComponent(encodedFilenameStarMatch[2]);
  }
  const encodedFilenameMatch = contentDisposition.match(/filename="((?:\\"|[^"])*)"/i);
  if (encodedFilenameMatch && typeof encodedFilenameMatch[1] === "string") {
    return encodedFilenameMatch[1].replace(/\\"/g, '"');
  }
}
function mergeStandardHeaders(a, b) {
  const merged = { ...a };
  for (const key in b) {
    if (Array.isArray(b[key])) {
      merged[key] = [...toArray(merged[key]), ...b[key]];
    } else if (b[key] !== undefined) {
      if (Array.isArray(merged[key])) {
        merged[key] = [...merged[key], b[key]];
      } else if (merged[key] !== undefined) {
        merged[key] = [merged[key], b[key]];
      } else {
        merged[key] = b[key];
      }
    }
  }
  return merged;
}

// node_modules/.bun/@orpc+client@1.15.0/node_modules/@orpc/client/dist/shared/client.BLtwTQUg.mjs
function mapEventIterator(iterator, maps) {
  const mapError = async (error) => {
    let mappedError = await maps.error(error);
    if (mappedError !== error) {
      const meta = getEventMeta(error);
      if (meta && isTypescriptObject(mappedError)) {
        mappedError = withEventMeta(mappedError, meta);
      }
    }
    return mappedError;
  };
  return new AsyncIteratorClass(async () => {
    const { done, value: value2 } = await (async () => {
      try {
        return await iterator.next();
      } catch (error) {
        throw await mapError(error);
      }
    })();
    let mappedValue = await maps.value(value2, done);
    if (mappedValue !== value2) {
      const meta = getEventMeta(value2);
      if (meta && isTypescriptObject(mappedValue)) {
        mappedValue = withEventMeta(mappedValue, meta);
      }
    }
    return { done, value: mappedValue };
  }, async () => {
    try {
      await iterator.return?.();
    } catch (error) {
      throw await mapError(error);
    }
  });
}

// node_modules/.bun/@orpc+client@1.15.0/node_modules/@orpc/client/dist/index.mjs
function resolveFriendlyClientOptions(options) {
  return {
    ...options,
    context: options.context ?? {}
  };
}
function createORPCClient(link, options = {}) {
  const path = options.path ?? [];
  const procedureClient = async (...[input, options2 = {}]) => {
    return await link.call(path, input, resolveFriendlyClientOptions(options2));
  };
  const recursive = new Proxy(procedureClient, {
    get(target, key) {
      if (typeof key !== "string" || RECURSIVE_CLIENT_UNWRAP_KEYS.has(key)) {
        return Reflect.get(target, key);
      }
      return createORPCClient(link, {
        ...options,
        path: [...path, key]
      });
    }
  });
  return preventNativeAwait(recursive);
}

// node_modules/.bun/@orpc+standard-server-fetch@1.15.0/node_modules/@orpc/standard-server-fetch/dist/index.mjs
function toEventIterator(stream, options = {}) {
  const eventStream = stream?.pipeThrough(new TextDecoderStream).pipeThrough(new EventDecoderStream);
  const reader = eventStream?.getReader();
  let span;
  let isCancelled = false;
  return new AsyncIteratorClass(async () => {
    span ??= startSpan("consume_event_iterator_stream");
    try {
      while (true) {
        if (reader === undefined) {
          return { done: true, value: undefined };
        }
        const { done, value: value2 } = await runInSpanContext(span, () => reader.read());
        if (done) {
          if (isCancelled) {
            throw new AbortError("Stream was cancelled");
          }
          return { done: true, value: undefined };
        }
        switch (value2.event) {
          case "message": {
            let message = parseEmptyableJSON(value2.data);
            if (isTypescriptObject(message)) {
              message = withEventMeta(message, value2);
            }
            span?.addEvent("message");
            return { done: false, value: message };
          }
          case "error": {
            let error = new ErrorEvent({
              data: parseEmptyableJSON(value2.data)
            });
            error = withEventMeta(error, value2);
            span?.addEvent("error");
            throw error;
          }
          case "done": {
            let done2 = parseEmptyableJSON(value2.data);
            if (isTypescriptObject(done2)) {
              done2 = withEventMeta(done2, value2);
            }
            span?.addEvent("done");
            return { done: true, value: done2 };
          }
          default: {
            span?.addEvent("maybe_keepalive");
          }
        }
      }
    } catch (e) {
      if (!(e instanceof ErrorEvent)) {
        setSpanError(span, e, options);
      }
      throw e;
    }
  }, async (reason) => {
    try {
      if (reason !== "next") {
        isCancelled = true;
        span?.addEvent("cancelled");
      }
      await runInSpanContext(span, () => reader?.cancel());
    } catch (e) {
      setSpanError(span, e, options);
      throw e;
    } finally {
      span?.end();
    }
  });
}
function toEventStream(iterator, options = {}) {
  const keepAliveEnabled = options.eventIteratorKeepAliveEnabled ?? true;
  const keepAliveInterval = options.eventIteratorKeepAliveInterval ?? 5000;
  const keepAliveComment = options.eventIteratorKeepAliveComment ?? "";
  const initialCommentEnabled = options.eventIteratorInitialCommentEnabled ?? true;
  const initialComment = options.eventIteratorInitialComment ?? "";
  let cancelled = false;
  let timeout;
  let span;
  const stream = new ReadableStream({
    start(controller) {
      span = startSpan("stream_event_iterator");
      if (initialCommentEnabled) {
        controller.enqueue(encodeEventMessage({
          comments: [initialComment]
        }));
      }
    },
    async pull(controller) {
      try {
        if (keepAliveEnabled) {
          timeout = setInterval(() => {
            controller.enqueue(encodeEventMessage({
              comments: [keepAliveComment]
            }));
            span?.addEvent("keepalive");
          }, keepAliveInterval);
        }
        const value2 = await runInSpanContext(span, () => iterator.next());
        clearInterval(timeout);
        if (cancelled) {
          return;
        }
        const meta = getEventMeta(value2.value);
        if (!value2.done || value2.value !== undefined || meta !== undefined) {
          const event = value2.done ? "done" : "message";
          controller.enqueue(encodeEventMessage({
            ...meta,
            event,
            data: stringifyJSON(value2.value)
          }));
          span?.addEvent(event);
        }
        if (value2.done) {
          controller.close();
          span?.end();
        }
      } catch (err) {
        clearInterval(timeout);
        if (cancelled) {
          return;
        }
        if (err instanceof ErrorEvent) {
          controller.enqueue(encodeEventMessage({
            ...getEventMeta(err),
            event: "error",
            data: stringifyJSON(err.data)
          }));
          span?.addEvent("error");
          controller.close();
        } else {
          setSpanError(span, err);
          controller.error(err);
        }
        span?.end();
      }
    },
    async cancel() {
      try {
        cancelled = true;
        clearInterval(timeout);
        span?.addEvent("cancelled");
        await runInSpanContext(span, () => iterator.return?.());
      } catch (e) {
        setSpanError(span, e);
        throw e;
      } finally {
        span?.end();
      }
    }
  }).pipeThrough(new TextEncoderStream);
  return stream;
}
function toStandardBody(re, options = {}) {
  return runWithSpan({ name: "parse_standard_body", signal: options.signal }, async () => {
    const contentDisposition = re.headers.get("content-disposition");
    if (typeof contentDisposition === "string") {
      const fileName = getFilenameFromContentDisposition(contentDisposition) ?? "blob";
      const blob2 = await re.blob();
      return new File([blob2], fileName, {
        type: blob2.type
      });
    }
    const contentType = re.headers.get("content-type");
    if (!contentType || contentType.startsWith("application/json")) {
      const text = await re.text();
      return parseEmptyableJSON(text);
    }
    if (contentType.startsWith("multipart/form-data")) {
      return await re.formData();
    }
    if (contentType.startsWith("application/x-www-form-urlencoded")) {
      const text = await re.text();
      return new URLSearchParams(text);
    }
    if (contentType.startsWith("text/event-stream")) {
      return toEventIterator(re.body, options);
    }
    if (contentType.startsWith("text/plain")) {
      return await re.text();
    }
    const blob = await re.blob();
    return new File([blob], "blob", {
      type: blob.type
    });
  });
}
function toFetchBody(body, headers, options = {}) {
  if (body instanceof ReadableStream) {
    return body;
  }
  const currentContentDisposition = headers.get("content-disposition");
  headers.delete("content-type");
  headers.delete("content-disposition");
  if (body === undefined) {
    return;
  }
  if (body instanceof Blob) {
    headers.set("content-type", body.type);
    headers.set("content-length", body.size.toString());
    headers.set("content-disposition", currentContentDisposition ?? generateContentDisposition(body instanceof File ? body.name : "blob"));
    return body;
  }
  if (body instanceof FormData) {
    return body;
  }
  if (body instanceof URLSearchParams) {
    return body;
  }
  if (isAsyncIteratorObject(body)) {
    headers.set("content-type", "text/event-stream");
    return toEventStream(body, options);
  }
  headers.set("content-type", "application/json");
  return stringifyJSON(body);
}
function toStandardHeaders(headers, standardHeaders = {}) {
  headers.forEach((value2, key) => {
    if (Array.isArray(standardHeaders[key])) {
      standardHeaders[key].push(value2);
    } else if (standardHeaders[key] !== undefined) {
      standardHeaders[key] = [standardHeaders[key], value2];
    } else {
      standardHeaders[key] = value2;
    }
  });
  return standardHeaders;
}
function toFetchHeaders(headers, fetchHeaders = new Headers) {
  for (const [key, value2] of Object.entries(headers)) {
    if (Array.isArray(value2)) {
      for (const v of value2) {
        fetchHeaders.append(key, v);
      }
    } else if (value2 !== undefined) {
      fetchHeaders.append(key, value2);
    }
  }
  return fetchHeaders;
}
function toFetchRequest(request, options = {}) {
  const headers = toFetchHeaders(request.headers);
  const body = toFetchBody(request.body, headers, options);
  return new Request(request.url, {
    signal: request.signal,
    method: request.method,
    headers,
    body
  });
}
function toStandardLazyResponse(response, options = {}) {
  return {
    body: once(() => toStandardBody(response, options)),
    status: response.status,
    get headers() {
      const headers = toStandardHeaders(response.headers);
      Object.defineProperty(this, "headers", { value: headers, writable: true });
      return headers;
    },
    set headers(value2) {
      Object.defineProperty(this, "headers", { value: value2, writable: true });
    }
  };
}

// node_modules/.bun/@orpc+client@1.15.0/node_modules/@orpc/client/dist/shared/client.BtiuJPEa.mjs
class CompositeStandardLinkPlugin {
  plugins;
  constructor(plugins = []) {
    this.plugins = [...plugins].sort((a, b) => (a.order ?? 0) - (b.order ?? 0));
  }
  init(options) {
    for (const plugin of this.plugins) {
      plugin.init?.(options);
    }
  }
}

class StandardLink {
  constructor(codec, sender, options = {}) {
    this.codec = codec;
    this.sender = sender;
    const plugin = new CompositeStandardLinkPlugin(options.plugins);
    plugin.init(options);
    this.interceptors = toArray(options.interceptors);
    this.clientInterceptors = toArray(options.clientInterceptors);
  }
  interceptors;
  clientInterceptors;
  call(path, input, options) {
    return runWithSpan({ name: `${ORPC_NAME}.${path.join("/")}`, signal: options.signal }, (span) => {
      span?.setAttribute("rpc.system", ORPC_NAME);
      span?.setAttribute("rpc.method", path.join("."));
      if (isAsyncIteratorObject(input)) {
        input = asyncIteratorWithSpan({ name: "consume_event_iterator_input", signal: options.signal }, input);
      }
      return intercept(this.interceptors, { ...options, path, input }, async ({ path: path2, input: input2, ...options2 }) => {
        const otelConfig = getGlobalOtelConfig();
        let otelContext;
        const currentSpan = otelConfig?.trace.getActiveSpan() ?? span;
        if (currentSpan && otelConfig) {
          otelContext = otelConfig?.trace.setSpan(otelConfig.context.active(), currentSpan);
        }
        const request = await runWithSpan({ name: "encode_request", context: otelContext }, () => this.codec.encode(path2, input2, options2));
        const response = await intercept(this.clientInterceptors, { ...options2, input: input2, path: path2, request }, ({ input: input3, path: path3, request: request2, ...options3 }) => {
          return runWithSpan({ name: "send_request", signal: options3.signal, context: otelContext }, () => this.sender.call(request2, options3, path3, input3));
        });
        const output = await runWithSpan({ name: "decode_response", context: otelContext }, () => this.codec.decode(response, options2, path2, input2));
        if (isAsyncIteratorObject(output)) {
          return asyncIteratorWithSpan({ name: "consume_event_iterator_output", signal: options2.signal }, output);
        }
        return output;
      });
    });
  }
}
var STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES = {
  BIGINT: 0,
  DATE: 1,
  NAN: 2,
  UNDEFINED: 3,
  URL: 4,
  REGEXP: 5,
  SET: 6,
  MAP: 7
};

class StandardRPCJsonSerializer {
  customSerializers;
  constructor(options = {}) {
    this.customSerializers = options.customJsonSerializers ?? [];
    if (this.customSerializers.length !== new Set(this.customSerializers.map((custom) => custom.type)).size) {
      throw new Error("Custom serializer type must be unique.");
    }
  }
  serialize(data, segments = [], meta = [], maps = [], blobs = []) {
    for (const custom of this.customSerializers) {
      if (custom.condition(data)) {
        const result = this.serialize(custom.serialize(data), segments, meta, maps, blobs);
        meta.push([custom.type, ...segments]);
        return result;
      }
    }
    if (data instanceof Blob) {
      maps.push(segments);
      blobs.push(data);
      return [data, meta, maps, blobs];
    }
    if (typeof data === "bigint") {
      meta.push([STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.BIGINT, ...segments]);
      return [data.toString(), meta, maps, blobs];
    }
    if (data instanceof Date) {
      meta.push([STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.DATE, ...segments]);
      if (Number.isNaN(data.getTime())) {
        return [null, meta, maps, blobs];
      }
      return [data.toISOString(), meta, maps, blobs];
    }
    if (Number.isNaN(data)) {
      meta.push([STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.NAN, ...segments]);
      return [null, meta, maps, blobs];
    }
    if (data instanceof URL) {
      meta.push([STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.URL, ...segments]);
      return [data.toString(), meta, maps, blobs];
    }
    if (data instanceof RegExp) {
      meta.push([STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.REGEXP, ...segments]);
      return [data.toString(), meta, maps, blobs];
    }
    if (data instanceof Set) {
      const result = this.serialize(Array.from(data), segments, meta, maps, blobs);
      meta.push([STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.SET, ...segments]);
      return result;
    }
    if (data instanceof Map) {
      const result = this.serialize(Array.from(data.entries()), segments, meta, maps, blobs);
      meta.push([STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.MAP, ...segments]);
      return result;
    }
    if (Array.isArray(data)) {
      const json = data.map((v, i) => {
        if (v === undefined) {
          meta.push([STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.UNDEFINED, ...segments, i]);
          return null;
        }
        return this.serialize(v, [...segments, i], meta, maps, blobs)[0];
      });
      return [json, meta, maps, blobs];
    }
    if (isObject(data)) {
      const json = {};
      for (const k in data) {
        if (k === "toJSON" && typeof data[k] === "function") {
          continue;
        }
        json[k] = this.serialize(data[k], [...segments, k], meta, maps, blobs)[0];
      }
      return [json, meta, maps, blobs];
    }
    return [data, meta, maps, blobs];
  }
  deserialize(json, meta, maps, getBlob) {
    const ref = { data: json };
    if (maps && getBlob) {
      maps.forEach((segments, i) => {
        let currentRef = ref;
        let preSegment = "data";
        segments.forEach((segment) => {
          currentRef = currentRef[preSegment];
          preSegment = segment;
          if (!Object.hasOwn(currentRef, preSegment)) {
            throw new Error(`Security error: accessing non-existent path during deserialization. Path segment: ${preSegment}`);
          }
        });
        currentRef[preSegment] = getBlob(i);
      });
    }
    for (const item of meta) {
      const type = item[0];
      let currentRef = ref;
      let preSegment = "data";
      for (let i = 1;i < item.length; i++) {
        currentRef = currentRef[preSegment];
        preSegment = item[i];
        if (!Object.hasOwn(currentRef, preSegment)) {
          throw new Error(`Security error: accessing non-existent path during deserialization. Path segment: ${preSegment}`);
        }
      }
      for (const custom of this.customSerializers) {
        if (custom.type === type) {
          currentRef[preSegment] = custom.deserialize(currentRef[preSegment]);
          break;
        }
      }
      switch (type) {
        case STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.BIGINT:
          currentRef[preSegment] = BigInt(currentRef[preSegment]);
          break;
        case STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.DATE:
          currentRef[preSegment] = new Date(currentRef[preSegment] ?? "Invalid Date");
          break;
        case STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.NAN:
          currentRef[preSegment] = Number.NaN;
          break;
        case STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.UNDEFINED:
          currentRef[preSegment] = undefined;
          break;
        case STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.URL:
          currentRef[preSegment] = new URL(currentRef[preSegment]);
          break;
        case STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.REGEXP: {
          const [, pattern, flags] = currentRef[preSegment].match(/^\/(.*)\/([a-z]*)$/);
          currentRef[preSegment] = new RegExp(pattern, flags);
          break;
        }
        case STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.SET:
          currentRef[preSegment] = new Set(currentRef[preSegment]);
          break;
        case STANDARD_RPC_JSON_SERIALIZER_BUILT_IN_TYPES.MAP:
          currentRef[preSegment] = new Map(currentRef[preSegment]);
          break;
      }
    }
    return ref.data;
  }
}
function toHttpPath(path) {
  return `/${path.map(encodeURIComponent).join("/")}`;
}
function toStandardHeaders2(headers) {
  if (typeof headers.forEach === "function") {
    return toStandardHeaders(headers);
  }
  return headers;
}
function getMalformedResponseErrorCode(status) {
  return Object.entries(COMMON_ORPC_ERROR_DEFS).find(([, def]) => def.status === status)?.[0] ?? "MALFORMED_ORPC_ERROR_RESPONSE";
}

class StandardRPCLinkCodec {
  constructor(serializer, options) {
    this.serializer = serializer;
    this.baseUrl = options.url;
    this.maxUrlLength = options.maxUrlLength ?? 2083;
    this.fallbackMethod = options.fallbackMethod ?? "POST";
    this.expectedMethod = options.method ?? this.fallbackMethod;
    this.headers = options.headers ?? {};
  }
  baseUrl;
  maxUrlLength;
  fallbackMethod;
  expectedMethod;
  headers;
  async encode(path, input, options) {
    let headers = toStandardHeaders2(await value(this.headers, options, path, input));
    if (options.lastEventId !== undefined) {
      headers = mergeStandardHeaders(headers, { "last-event-id": options.lastEventId });
    }
    const expectedMethod = await value(this.expectedMethod, options, path, input);
    const baseUrl = await value(this.baseUrl, options, path, input);
    const url = new URL(baseUrl);
    url.pathname = `${url.pathname.replace(/\/$/, "")}${toHttpPath(path)}`;
    const serialized = this.serializer.serialize(input);
    if (expectedMethod === "GET" && !(serialized instanceof FormData) && !isAsyncIteratorObject(serialized)) {
      const maxUrlLength = await value(this.maxUrlLength, options, path, input);
      const getUrl = new URL(url);
      getUrl.searchParams.append("data", stringifyJSON(serialized));
      if (getUrl.toString().length <= maxUrlLength) {
        return {
          body: undefined,
          method: expectedMethod,
          headers,
          url: getUrl,
          signal: options.signal
        };
      }
    }
    return {
      url,
      method: expectedMethod === "GET" ? this.fallbackMethod : expectedMethod,
      headers,
      body: serialized,
      signal: options.signal
    };
  }
  async decode(response) {
    const isOk = !isORPCErrorStatus(response.status);
    const deserialized = await (async () => {
      let isBodyOk = false;
      try {
        const body = await response.body();
        isBodyOk = true;
        return this.serializer.deserialize(body);
      } catch (error) {
        if (!isBodyOk) {
          throw new Error("Cannot parse response body, please check the response body and content-type.", {
            cause: error
          });
        }
        throw new Error("Invalid RPC response format.", {
          cause: error
        });
      }
    })();
    if (!isOk) {
      if (isORPCErrorJson(deserialized)) {
        throw createORPCErrorFromJson(deserialized);
      }
      throw new ORPCError(getMalformedResponseErrorCode(response.status), {
        status: response.status,
        data: { ...response, body: deserialized }
      });
    }
    return deserialized;
  }
}

class StandardRPCSerializer {
  constructor(jsonSerializer) {
    this.jsonSerializer = jsonSerializer;
  }
  serialize(data) {
    if (isAsyncIteratorObject(data)) {
      return mapEventIterator(data, {
        value: async (value2) => this.#serialize(value2, false),
        error: async (e) => {
          return new ErrorEvent({
            data: this.#serialize(toORPCError(e).toJSON(), false),
            cause: e
          });
        }
      });
    }
    return this.#serialize(data, true);
  }
  #serialize(data, enableFormData) {
    const [json, meta_, maps, blobs] = this.jsonSerializer.serialize(data);
    const meta = meta_.length === 0 ? undefined : meta_;
    if (!enableFormData || blobs.length === 0) {
      return {
        json,
        meta
      };
    }
    const form = new FormData;
    form.set("data", stringifyJSON({ json, meta, maps }));
    blobs.forEach((blob, i) => {
      form.set(i.toString(), blob);
    });
    return form;
  }
  deserialize(data) {
    if (isAsyncIteratorObject(data)) {
      return mapEventIterator(data, {
        value: async (value2) => this.#deserialize(value2),
        error: async (e) => {
          if (!(e instanceof ErrorEvent)) {
            return e;
          }
          const deserialized = this.#deserialize(e.data);
          if (isORPCErrorJson(deserialized)) {
            return createORPCErrorFromJson(deserialized, { cause: e });
          }
          return new ErrorEvent({
            data: deserialized,
            cause: e
          });
        }
      });
    }
    return this.#deserialize(data);
  }
  #deserialize(data) {
    if (data === undefined) {
      return;
    }
    if (!(data instanceof FormData)) {
      return this.jsonSerializer.deserialize(data.json, data.meta ?? []);
    }
    const serialized = JSON.parse(data.get("data"));
    return this.jsonSerializer.deserialize(serialized.json, serialized.meta ?? [], serialized.maps, (i) => data.get(i.toString()));
  }
}

class StandardRPCLink extends StandardLink {
  constructor(linkClient, options) {
    const jsonSerializer = new StandardRPCJsonSerializer(options);
    const serializer = new StandardRPCSerializer(jsonSerializer);
    const linkCodec = new StandardRPCLinkCodec(serializer, options);
    super(linkCodec, linkClient, options);
  }
}

// node_modules/.bun/@orpc+client@1.15.0/node_modules/@orpc/client/dist/adapters/fetch/index.mjs
class CompositeLinkFetchPlugin extends CompositeStandardLinkPlugin {
  initRuntimeAdapter(options) {
    for (const plugin of this.plugins) {
      plugin.initRuntimeAdapter?.(options);
    }
  }
}

class LinkFetchClient {
  fetch;
  toFetchRequestOptions;
  adapterInterceptors;
  constructor(options) {
    const plugin = new CompositeLinkFetchPlugin(options.plugins);
    plugin.initRuntimeAdapter(options);
    this.fetch = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.toFetchRequestOptions = options;
    this.adapterInterceptors = toArray(options.adapterInterceptors);
  }
  async call(standardRequest, options, path, input) {
    const request = toFetchRequest(standardRequest, this.toFetchRequestOptions);
    const fetchResponse = await intercept(this.adapterInterceptors, { ...options, request, path, input, init: { redirect: "manual" } }, ({ request: request2, path: path2, input: input2, init, ...options2 }) => this.fetch(request2, init, options2, path2, input2));
    const lazyResponse = toStandardLazyResponse(fetchResponse, { signal: request.signal });
    return lazyResponse;
  }
}

class RPCLink extends StandardRPCLink {
  constructor(options) {
    const linkClient = new LinkFetchClient(options);
    super(linkClient, options);
  }
}

// web/src/app.ts
var link = new RPCLink({ url: `${location.origin}/rpc` });
var client = createORPCClient(link);
var $ = (selector) => document.querySelector(selector);
var content = $("#content");
var notice = $("#notice");
var runner = $("#runner");
var outputModal = $("#output-modal");
var snapshot = null;
var catalog = [];
var activeView = location.hash.slice(1) || "overview";
var views = [
  ["overview", "⌂", "Overview"],
  ["apps", "◫", "Applications"],
  ["data", "◆", "Data & runtimes"],
  ["routing", "↗", "Routing & TLS"],
  ["jobs", "↻", "Jobs & workers"],
  ["operations", "✓", "Operations"],
  ["advanced", "›_", "Advanced"]
];
function escapeHtml(value2) {
  return String(value2 ?? "").replace(/[&<>'"]/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" })[char]);
}
function badge(value2, kind = "outline") {
  return `<span class="badge badge-${kind}">${escapeHtml(value2)}</span>`;
}
function commands(values) {
  return `<div class="command-list">${values.map((command) => `<div class="command-row"><code>bento ${escapeHtml(command)}</code><button class="btn btn-sm btn-outline run-preset" data-command="${escapeHtml(command)}">Configure</button></div>`).join("")}</div>`;
}
function renderNav() {
  $("#nav").innerHTML = views.map(([id, icon, label]) => `<button class="nav-link ${activeView === id ? "active" : ""}" data-view="${id}"><span class="nav-icon">${icon}</span>${label}</button>`).join("");
  document.querySelectorAll("[data-view]").forEach((button) => button.addEventListener("click", () => {
    location.hash = button.dataset.view;
    $(".sidebar").classList.remove("open");
  }));
}
async function load() {
  try {
    [snapshot, catalog] = await Promise.all([client.snapshot({}), client.catalog({})]);
    $("#connection").className = "status status-success";
    $("#connection-label").textContent = "Connected";
    $("#stack-name").textContent = snapshot.projectName || snapshot.stackRoot;
    notice.textContent = "";
    render();
  } catch (error) {
    $("#connection").className = "status status-error";
    $("#connection-label").textContent = "Disconnected";
    notice.textContent = error instanceof Error ? error.message : String(error);
    content.innerHTML = `<div class="alert alert-error">Could not load Bento. Verify the server is running and refresh.</div>`;
  }
}
function render() {
  renderNav();
  const title = views.find(([id]) => id === activeView)?.[2] ?? "Overview";
  $("#page-title").textContent = title;
  if (!snapshot)
    return;
  if (!snapshot.initialized && snapshot.error) {
    content.innerHTML = `<div class="alert alert-error"><div><strong>The selected stack cannot be loaded.</strong><p>${escapeHtml(snapshot.error)}</p><p><code>${escapeHtml(snapshot.stackRoot)}</code></p></div></div><section class="panel full"><h2>Choose a compatible stack</h2><p>Stop the server and restart it with <code>--stack &lt;schema-v1-stack-root&gt;</code>. Bento will not overwrite or migrate an incompatible state file.</p></section>`;
  } else if (!snapshot.initialized && activeView !== "operations" && activeView !== "advanced") {
    content.innerHTML = `<div class="hero"><div><p class="eyebrow">WELCOME TO BENTO</p><h2>Initialize this stack to begin.</h2><p>The web control plane uses the same desired state, validation, and locking as the CLI.</p></div><button class="btn btn-secondary run-preset" data-command="init --name bento">Initialize</button></div>`;
  } else if (activeView === "overview")
    renderOverview(snapshot);
  else if (activeView === "apps")
    renderApps(snapshot);
  else if (activeView === "data")
    renderData(snapshot);
  else if (activeView === "routing")
    renderRouting(snapshot);
  else if (activeView === "jobs")
    renderJobs(snapshot);
  else if (activeView === "operations")
    renderOperations();
  else
    renderAdvanced();
  bindPresets();
}
function renderOverview(data) {
  const dbCount = data.apps.reduce((sum, app) => sum + app.databases.length, 0);
  content.innerHTML = `<div class="hero"><div><p class="eyebrow">STACK ${escapeHtml(data.projectName || "BENTO")}</p><h2>Everything your host needs, in one calm workspace.</h2><p>Manage applications, data services, routing and background jobs through Bento's typed oRPC API.</p></div><button class="btn btn-secondary run-preset" data-command="apply --preview">Preview changes</button></div>
  <div class="stats-grid"><div class="metric"><div class="metric-label">Applications</div><div class="metric-value">${data.apps.length}</div></div><div class="metric"><div class="metric-label">Reverse proxies</div><div class="metric-value">${data.proxies.length}</div></div><div class="metric"><div class="metric-label">Database bindings</div><div class="metric-value">${dbCount}</div></div><div class="metric"><div class="metric-label">Background tasks</div><div class="metric-value">${data.cronJobs.length + data.workers.length}</div></div></div>
  <div class="card-grid"><section class="panel"><h2>Quick actions</h2><div class="quick-grid"><button class="btn btn-outline quick run-preset" data-command="status">Stack status</button><button class="btn btn-outline quick run-preset" data-command="doctor">Run doctor</button><button class="btn btn-outline quick run-preset" data-command="apply">Apply config</button></div></section><section class="panel"><h2>Stack</h2><p><strong>Root</strong><br><code>${escapeHtml(data.stackRoot)}</code></p><p><strong>Last state update</strong><br>${escapeHtml(data.updatedAt || "—")}</p></section></div>`;
}
function renderApps(data) {
  const rows = data.apps.map((app) => `<tr><td><strong>${escapeHtml(app.slug)}</strong><br><small>${escapeHtml(app.domain)}</small></td><td>${badge(app.enabled ? "enabled" : "disabled", app.enabled ? "success" : "warning")}</td><td>PHP ${escapeHtml(app.phpVersion)} · ${escapeHtml(app.fpmProfile)}</td><td><div class="pill-row">${app.databases.map((db) => badge(db.engine)).join("")}</div></td><td>${badge(app.tls)}</td><td><button class="btn btn-xs btn-outline run-preset" data-command="app show ${escapeHtml(app.slug)}">Manage</button></td></tr>`).join("");
  content.innerHTML = `<div class="section-head"><div><h2>Applications</h2><p>Domains, runtimes, databases, access logs and deployment.</p></div><button class="btn btn-primary run-preset" data-command="app create <slug> --domain <domain> --docroot public --db">Create app</button></div><div class="table-wrap"><table class="table"><thead><tr><th>Application</th><th>State</th><th>Runtime</th><th>Data</th><th>TLS</th><th></th></tr></thead><tbody>${rows || `<tr><td colspan="6" class="empty">No applications yet.</td></tr>`}</tbody></table></div>
  <div class="card-grid"><section class="panel"><h3>Lifecycle & domains</h3>${commands(["app update <slug> --domain <domain>", "app enable <slug>", "app disable <slug>", "app delete <slug> --confirm 'delete <slug>'", "app prune <slug> --confirm delete", "tls set --app <slug> --mode self-ca"])}</section><section class="panel"><h3>App features</h3>${commands(["deploy status <slug>", "deploy enable <slug>", "deploy disable <slug>", "deploy rotate <slug>", "deploy drain <slug>", "deploy instructions <slug>", "logs access enable --app <slug>", "logs access rotate --app <slug>", "logs access report --app <slug>", "template drift --app <slug>", "template select --app <slug> --kind vhost --source <path>", "template return --app <slug> --kind vhost", "app shell <slug> --print", "exec <slug> -- <command>"])}</section></div>`;
}
function renderData(data) {
  content.innerHTML = `<div class="section-head"><div><h2>Data & runtimes</h2><p>PHP, MySQL, PostgreSQL, SQLite and backups.</p></div><button class="btn btn-primary run-preset" data-command="backup --all">Back up all</button></div><div class="stats-grid"><div class="metric"><div class="metric-label">PHP versions</div><div class="metric-value">${data.phpVersions.length}</div><div class="pill-row">${data.phpVersions.map((v) => badge(v.version)).join("")}</div></div><div class="metric"><div class="metric-label">MySQL</div><div class="metric-value">${data.mysqlVersions.length}</div><div class="pill-row">${data.mysqlVersions.map((v) => badge(v.version)).join("")}</div></div><div class="metric"><div class="metric-label">PostgreSQL</div><div class="metric-value">${data.postgresVersions.length}</div><div class="pill-row">${data.postgresVersions.map((v) => badge(v.version)).join("")}</div></div><div class="metric"><div class="metric-label">SQLite apps</div><div class="metric-value">${data.apps.filter((a) => a.databases.some((d) => d.engine === "sqlite" || d.engine === "litestream")).length}</div></div></div><div class="card-grid"><section class="panel"><h3>Services</h3>${commands(["php list", "php add <version>", "php reload <version>", "mysql list", "mysql add <version>", "mysql size", "mysql processlist", "mysql shell --app <app> --print", "postgres list", "postgres add <major>", "postgres size", "postgres processlist", "postgres shell --app <app> --print"])}</section><section class="panel"><h3>Database operations</h3>${commands(["mysql db <app> <database>", "postgres db <app> <database>", "sqlite backup local <app> --gzip", "sqlite backup enable <app>", "sqlite backup status", "sqlite backup sync", "sqlite backup verify --app <app>", "sqlite backup export --app <app> --output <path>", "backup --app <slug> --gzip", "restore --file <path> --app <slug>"])}</section></div>`;
}
function renderRouting(data) {
  const rows = data.proxies.map((proxy) => `<tr><td><strong>${escapeHtml(proxy.name)}</strong></td><td>${escapeHtml(proxy.domain)}</td><td>${proxy.upstreams.map(escapeHtml).join("<br>")}</td><td>${badge(proxy.tls)}</td><td><button class="btn btn-xs btn-outline run-preset" data-command="tls set --proxy ${escapeHtml(proxy.name)} --mode self-ca">TLS</button></td></tr>`).join("");
  content.innerHTML = `<div class="section-head"><div><h2>Routing & TLS</h2><p>Reverse proxies, upstreams, certificates and ingress.</p></div><button class="btn btn-primary run-preset" data-command="proxy create <name> --domain <domain> --upstream <url>">Create proxy</button></div><div class="table-wrap"><table class="table"><thead><tr><th>Name</th><th>Domain</th><th>Upstreams</th><th>TLS</th><th></th></tr></thead><tbody>${rows || `<tr><td colspan="5" class="empty">No reverse proxies.</td></tr>`}</tbody></table></div><div class="card-grid"><section class="panel"><h3>Routing</h3>${commands(["proxy list", "proxy delete <name> --confirm 'delete <name>'", "stack ingress show", "stack ingress set bridge --http-port 8080 --https-port 8443"])}</section><section class="panel"><h3>Certificates</h3>${commands(["tls set --app <slug> --mode self-ca", "tls set --proxy <name> --mode acme", "tls ca export --output <path>"])}</section></div>`;
}
function renderJobs(data) {
  const taskRows = [
    ...data.cronJobs.map((j) => `<tr><td>${badge("cron")}</td><td>${escapeHtml(j.app)}</td><td>${escapeHtml(j.name)}</td><td><code>${escapeHtml(j.schedule)}</code></td><td>${badge(j.enabled ? "enabled" : "disabled")}</td></tr>`),
    ...data.workers.map((w) => `<tr><td>${badge("worker")}</td><td>${escapeHtml(w.app)}</td><td>${escapeHtml(w.name)}</td><td><code>${escapeHtml(w.command.join(" "))}</code></td><td>${badge(w.enabled ? "enabled" : "disabled")}</td></tr>`)
  ].join("");
  content.innerHTML = `<div class="section-head"><div><h2>Jobs & workers</h2><p>Schedules, long-running processes and runtime controls.</p></div></div><div class="table-wrap"><table class="table"><thead><tr><th>Kind</th><th>App</th><th>Name</th><th>Schedule / command</th><th>State</th></tr></thead><tbody>${taskRows || `<tr><td colspan="5" class="empty">No jobs or workers.</td></tr>`}</tbody></table></div><div class="card-grid"><section class="panel"><h3>Cron jobs</h3>${commands(["cron list <app>", "cron add --app <app> --name <name> --schedule '<cron>' --cmd '<command>'", "cron edit <app> <name>", "cron remove <app> <name>", "cron reload <app>"])}</section><section class="panel"><h3>Workers</h3>${commands(["worker list <app>", "worker add --app <app> --name <name> --cmd '<command>'", "worker start <app> <name>", "worker stop <app> <name>", "worker restart <app> <name>", "worker signal <app> <name> --signal HUP", "worker inspect <app> <name>", "worker remove <app> <name>"])}</section></div>`;
}
function renderOperations() {
  content.innerHTML = `<div class="section-head"><div><h2>Operations & diagnostics</h2><p>Render, validate, inspect and protect your stack.</p></div></div><div class="card-grid"><section class="panel"><h3>Apply</h3>${commands(["render", "apply --preview", "apply --render-only", "apply"])}</section><section class="panel"><h3>Health & safety</h3>${commands(["status", "doctor", "permissions check", "permissions repair --dry-run", "support-bundle"])}</section><section class="panel"><h3>Maintenance</h3>${commands(["maintenance run", "maintenance register", "maintenance unregister", "backup schedule status", "backup schedule run", "compose files"])}</section><section class="panel"><h3>Stack</h3>${commands(["stack ingress show", "stack export <directory>", "stack import <directory>", "test-stack --skip-build"])}</section></div>`;
}
function renderAdvanced() {
  content.innerHTML = `<div class="section-head"><div><h2>Advanced command catalog</h2><p>Every browser-safe management action is sent as a typed argv array—never through a shell.</p></div><button class="btn btn-primary run-preset" data-command="status">Open runner</button></div><div class="card-grid">${catalog.map((group) => `<section class="panel"><h3>${escapeHtml(group.category)}</h3>${commands(group.commands)}</section>`).join("")}</div>`;
}
function bindPresets() {
  document.querySelectorAll(".run-preset").forEach((button) => button.addEventListener("click", () => openRunner(button.dataset.command || "status")));
}
function openRunner(command) {
  $("#command").value = command;
  $("#runner-title").textContent = command.split(" ").slice(0, 2).join(" ");
  runner.showModal();
  setTimeout(() => $("#command").focus(), 0);
}
function parseArgv(value2) {
  const args = [];
  let current = "";
  let quote = "";
  let escaped = false;
  for (const char of value2.trim()) {
    if (escaped) {
      current += char;
      escaped = false;
      continue;
    }
    if (char === "\\" && quote !== "'") {
      escaped = true;
      continue;
    }
    if (quote) {
      if (char === quote)
        quote = "";
      else
        current += char;
      continue;
    }
    if (char === "'" || char === '"') {
      quote = char;
      continue;
    }
    if (/\s/.test(char)) {
      if (current) {
        args.push(current);
        current = "";
      }
    } else
      current += char;
  }
  if (quote)
    throw new Error("Close the quoted argument before running");
  if (escaped)
    current += "\\";
  if (current)
    args.push(current);
  return args[0] === "bento" ? args.slice(1) : args;
}
async function execute() {
  const button = $("#run-command");
  try {
    const argv = parseArgv($("#command").value);
    if (!argv.length)
      throw new Error("Enter a Bento command");
    if (argv.some((arg) => /^<.*>$/.test(arg)))
      throw new Error("Replace all <placeholders> with real values");
    if (/\b(delete|remove|prune|repair|restore|import)\b/.test(argv.join(" ")) && !confirm(`Run destructive operation?

bento ${argv.join(" ")}`))
      return;
    button.disabled = true;
    button.classList.add("loading");
    const result = await client.execute({ argv });
    runner.close();
    $("#output-title").textContent = `bento ${argv.join(" ")}`;
    $("#exit-code").textContent = result.timedOut ? "timed out" : `exit ${result.code}`;
    $("#exit-code").className = `badge ${result.code === 0 ? "badge-success" : "badge-error"}`;
    $("#output").textContent = [result.stdout, result.stderr].filter(Boolean).join(`
`) || "Command completed without output.";
    outputModal.showModal();
    await load();
  } catch (error) {
    notice.textContent = error instanceof Error ? error.message : String(error);
  } finally {
    button.disabled = false;
    button.classList.remove("loading");
  }
}
window.addEventListener("hashchange", () => {
  activeView = location.hash.slice(1) || "overview";
  render();
});
$("#refresh").addEventListener("click", load);
$("#menu-button").addEventListener("click", () => $(".sidebar").classList.toggle("open"));
$("#run-command").addEventListener("click", execute);
$("#copy-output").addEventListener("click", async () => {
  await navigator.clipboard.writeText($("#output").textContent || "");
});
$("#theme").addEventListener("click", () => {
  const next = document.documentElement.dataset.theme === "night" ? "bento" : "night";
  document.documentElement.dataset.theme = next;
  localStorage.setItem("bento-theme", next);
});
document.documentElement.dataset.theme = localStorage.getItem("bento-theme") || (matchMedia("(prefers-color-scheme: dark)").matches ? "night" : "bento");
renderNav();
load();
