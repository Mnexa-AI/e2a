// Typed error hierarchy for the e2a SDK (Slice 8b-1).
//
// The /v1 surface returns a uniform envelope `{ error: { code, message,
// request_id } }` (internal/httpapi/errors.go). We map it to typed errors so a
// caller can branch with `instanceof` and read `.code` / `.status` /
// `.requestId` / `.retryable` without parsing bodies. The class is chosen
// primarily from the semantic `error.code`, falling back to the HTTP status
// bucket — so a NEW server code degrades to the right family rather than a bare
// base error.

import { ApiException } from "./oag/apis/exception.js";
import type { ErrorEnvelope } from "./oag/models/ErrorEnvelope.js";

export interface E2AErrorFields {
  /** Stable machine code from the envelope (e.g. "domain_not_verified"). */
  code: string;
  message: string;
  /** HTTP status; 0 for a connection-level failure with no response. */
  status: number;
  /** X-Request-Id echoed by the server — quote it in support requests. */
  requestId?: string;
  /** Structured field-level detail (envelope `error.details`). */
  details?: unknown;
  /** True when retrying could plausibly succeed (429 / 5xx / connection). */
  retryable: boolean;
  /** Seconds from a Retry-After header, when present. */
  retryAfterSeconds?: number;
  /** Underlying cause (e.g. the fetch TypeError on a connection failure). */
  cause?: unknown;
}

/** Base class for every error the SDK throws. */
export class E2AError extends Error {
  readonly code: string;
  readonly status: number;
  readonly requestId?: string;
  readonly details?: unknown;
  readonly retryable: boolean;
  readonly retryAfterSeconds?: number;

  constructor(fields: E2AErrorFields) {
    super(fields.message, fields.cause !== undefined ? { cause: fields.cause } : undefined);
    // new.target so subclasses report their own name without per-class boilerplate.
    this.name = new.target.name;
    this.code = fields.code;
    this.status = fields.status;
    this.requestId = fields.requestId;
    this.details = fields.details;
    this.retryable = fields.retryable;
    this.retryAfterSeconds = fields.retryAfterSeconds;
    // Restore the prototype chain so `instanceof` works after transpilation.
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export class E2AAuthError extends E2AError {}        // 401
export class E2APermissionError extends E2AError {}  // 403
export class E2ANotFoundError extends E2AError {}    // 404
export class E2AConflictError extends E2AError {}    // 409
export class E2AValidationError extends E2AError {}  // 422 — input validation
export class E2AIdempotencyError extends E2AError {} // idempotency_in_flight / _key_reuse
export class E2ARateLimitError extends E2AError {}   // 429
export class E2AServerError extends E2AError {}      // 5xx / 408
export class E2AConnectionError extends E2AError {}  // no response (network)
export class E2AWebhookSignatureError extends E2AError {} // local webhook verify failure

/** Status codes where a retry could plausibly help (excludes connection — that
 *  path is handled separately, since there's no status to inspect). */
export function isRetryableStatus(status: number): boolean {
  return status === 408 || status === 429 || (status >= 500 && status <= 599);
}

// The two idempotency-conflict codes from internal/httpapi/idempotency.go.
// in_flight is safe to retry (same key, server dedupes); key_reuse is a caller
// bug (same key, different body) and must not be retried.
const IDEMPOTENCY_RETRYABLE = "idempotency_in_flight";
const IDEMPOTENCY_CODES = new Set([IDEMPOTENCY_RETRYABLE, "idempotency_key_reuse"]);

type Make = (f: E2AErrorFields) => E2AError;

function resolve(status: number, code: string): { make: Make; retryable: boolean } {
  if (IDEMPOTENCY_CODES.has(code)) {
    return { make: (f) => new E2AIdempotencyError(f), retryable: code === IDEMPOTENCY_RETRYABLE };
  }
  switch (status) {
    case 401:
      return { make: (f) => new E2AAuthError(f), retryable: false };
    case 403:
      return { make: (f) => new E2APermissionError(f), retryable: false };
    case 404:
      return { make: (f) => new E2ANotFoundError(f), retryable: false };
    case 409:
      return { make: (f) => new E2AConflictError(f), retryable: false };
    case 422:
      return { make: (f) => new E2AValidationError(f), retryable: false };
    case 429:
      return { make: (f) => new E2ARateLimitError(f), retryable: true };
  }
  if (isRetryableStatus(status)) return { make: (f) => new E2AServerError(f), retryable: true };
  return { make: (f) => new E2AError(f), retryable: false };
}

function headerGet(headers: Record<string, string> | undefined, name: string): string | undefined {
  if (!headers) return undefined;
  const lower = name.toLowerCase();
  for (const k of Object.keys(headers)) {
    if (k.toLowerCase() === lower) return headers[k];
  }
  return undefined;
}

function parseRetryAfter(headers: Record<string, string> | undefined): number | undefined {
  const v = headerGet(headers, "retry-after");
  if (!v) return undefined;
  const secs = Number(v);
  if (Number.isFinite(secs) && secs >= 0) return secs;
  // RFC 9110 §10.2.3 also allows an HTTP-date (common behind CDNs).
  const at = Date.parse(v);
  if (Number.isFinite(at)) return Math.max(0, Math.round((at - Date.now()) / 1000));
  return undefined;
}

const DEFAULT_CODE: Record<number, string> = {
  400: "invalid_request",
  401: "unauthorized",
  403: "forbidden",
  404: "not_found",
  409: "conflict",
  422: "unprocessable_entity",
  429: "rate_limited",
};

/** Build a typed error from status + the parsed envelope fields. */
export function toE2AError(args: {
  status: number;
  code?: string;
  message?: string;
  requestId?: string;
  details?: unknown;
  headers?: Record<string, string>;
  cause?: unknown;
}): E2AError {
  const code = args.code || DEFAULT_CODE[args.status] || (args.status >= 500 ? "internal_error" : "error");
  const m = resolve(args.status, args.code || "");
  return m.make({
    code,
    message: args.message || `e2a API error (${args.status})`,
    status: args.status,
    requestId: args.requestId,
    details: args.details,
    retryable: m.retryable,
    retryAfterSeconds: parseRetryAfter(args.headers),
    cause: args.cause,
  });
}

/** Map a generated `ApiException<ErrorEnvelope>` (thrown by the oag `*Api`
 *  classes on a non-2xx response) to a typed E2AError. */
export function fromApiException(e: ApiException<unknown>): E2AError {
  const headers = (e.headers ?? {}) as Record<string, string>;
  const requestId = headerGet(headers, "x-request-id");
  let code = "";
  let message = e.message;
  let details: unknown;
  const env = e.body as Partial<ErrorEnvelope> | undefined;
  if (env && env.error) {
    code = env.error.code ?? "";
    message = env.error.message ?? message;
    details = env.error.details ?? undefined;
  }
  return toE2AError({ status: e.code, code, message, requestId, details, headers });
}

/** A connection-level failure with no HTTP response (DNS, refused, aborted). */
export function connectionError(message: string, cause?: unknown): E2AConnectionError {
  return new E2AConnectionError({
    code: "connection_error",
    message,
    status: 0,
    retryable: true,
    cause,
  });
}
