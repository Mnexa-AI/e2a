// Typed error hierarchy for the e2a SDK (Slice 8b-1).
//
// The /v1 surface returns a uniform envelope `{ error: { code, message,
// request_id } }` (internal/httpapi/errors.go). We map it to typed errors so a
// caller can branch with `instanceof` and read `.code` / `.status` /
// `.requestId` / `.retryable` without parsing bodies.
//
// Class selection is code-first: a known, stable server `code` (CODE_TABLE
// below) maps to its family regardless of the HTTP status it arrives on, so a
// code that shows up on an unexpected status no longer degrades to the bare
// base error. An unknown code falls back to the HTTP status bucket, which
// preserves every status→class outcome (401→Auth, 403→Permission, 404→NotFound,
// 409→Conflict, 422→Validation, 429→RateLimit, 5xx/408→Server) so a NEW server
// code still lands in the right family.

import { ApiException } from "./generated/apis/exception.js";
import type { ErrorEnvelope } from "./generated/models/ErrorEnvelope.js";

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
export class E2ALimitExceededError extends E2AError {} // 402 — QUOTA (stock/flow) cap; NOT retryable
export class E2ARateLimitError extends E2AError {}   // 429 — request-RATE / throughput limit; retryable
export class E2AServerError extends E2AError {}      // 5xx / 408
export class E2AConnectionError extends E2AError {}  // no response (network)
export class E2AWebhookSignatureError extends E2AError {} // local webhook verify failure

/** Status codes where a retry could plausibly help (excludes connection — that
 *  path is handled separately, since there's no status to inspect). */
export function isRetryableStatus(status: number): boolean {
  return status === 408 || status === 429 || (status >= 500 && status <= 599);
}

type Make = (f: E2AErrorFields) => E2AError;

const mkAuth: Make = (f) => new E2AAuthError(f);
const mkPermission: Make = (f) => new E2APermissionError(f);
const mkNotFound: Make = (f) => new E2ANotFoundError(f);
const mkConflict: Make = (f) => new E2AConflictError(f);
const mkValidation: Make = (f) => new E2AValidationError(f);
const mkIdempotency: Make = (f) => new E2AIdempotencyError(f);
const mkLimitExceeded: Make = (f) => new E2ALimitExceededError(f);
const mkRateLimit: Make = (f) => new E2ARateLimitError(f);
const mkServer: Make = (f) => new E2AServerError(f);

// Code-first table: a stable server `code` maps to its family regardless of the
// status it rides on. Seeded from internal/httpapi (defaultCodeForStatus + the
// NewError call sites). Idempotency: in_flight is safe to retry (server dedupes
// the same key); key_reuse is a caller bug (same key, different body) — never
// retry. Keep this small and documented; unknown codes fall back to the status
// bucket in resolve().
const CODE_TABLE: Record<string, { make: Make; retryable: boolean }> = {
  // 401
  unauthorized: { make: mkAuth, retryable: false },
  // 403
  forbidden: { make: mkPermission, retryable: false },
  // 404
  not_found: { make: mkNotFound, retryable: false },
  // 409
  conflict: { make: mkConflict, retryable: false },
  // 400/422 — input/semantic validation. invalid_request is the single
  // canonical code the server now emits for both statuses; bad_request /
  // unprocessable_entity are retained only to tolerate legacy/mixed responses.
  invalid_request: { make: mkValidation, retryable: false },
  bad_request: { make: mkValidation, retryable: false },
  unprocessable_entity: { make: mkValidation, retryable: false },
  invalid_cursor: { make: mkValidation, retryable: false },
  domain_not_verified: { make: mkValidation, retryable: false },
  // 402 — QUOTA cap (stock/flow). NOT retryable: distinct from the 429
  // request-RATE limit below. This is the permanent GA 402/429 split.
  limit_exceeded: { make: mkLimitExceeded, retryable: false },
  // 429 — request-RATE / throughput limit. Retryable (back off Retry-After).
  rate_limited: { make: mkRateLimit, retryable: true },
  // idempotency (internal/httpapi/idempotency.go)
  idempotency_in_flight: { make: mkIdempotency, retryable: true },
  idempotency_key_reuse: { make: mkIdempotency, retryable: false },
};

function resolve(status: number, code: string): { make: Make; retryable: boolean } {
  // Code-first: a known code wins over the status bucket.
  if (code) {
    const byCode = CODE_TABLE[code];
    if (byCode) return byCode;
    // Pattern families the server may add (e.g. agent_not_found, slug_exists).
    if (code.endsWith("_not_found")) return { make: mkNotFound, retryable: false };
    if (code.endsWith("_exists")) return { make: mkConflict, retryable: false };
  }
  switch (status) {
    case 400:
      // Every 400 is a client/validation error. Maps the many 400 codes
      // (confirmation_required, too_many_recipients, invalid_domain, …) to the
      // validation family instead of degrading to the bare base error.
      return { make: mkValidation, retryable: false };
    case 401:
      return { make: mkAuth, retryable: false };
    case 402:
      return { make: mkLimitExceeded, retryable: false };
    case 403:
      return { make: mkPermission, retryable: false };
    case 404:
      return { make: mkNotFound, retryable: false };
    case 409:
      return { make: mkConflict, retryable: false };
    case 422:
      return { make: mkValidation, retryable: false };
    case 429:
      return { make: mkRateLimit, retryable: true };
  }
  if (isRetryableStatus(status)) return { make: mkServer, retryable: true };
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

// The send-path 429 carries its retry hint in `details.retry_after_seconds`
// rather than a Retry-After header (a Huma constraint — see
// internal/httpapi/outbound.go). Read it as a fallback so the surfaced error
// still exposes `retryAfterSeconds` even when the header is absent.
function retryAfterFromDetails(details: unknown): number | undefined {
  if (details && typeof details === "object") {
    const v = (details as Record<string, unknown>).retry_after_seconds;
    if (typeof v === "number" && Number.isFinite(v) && v >= 0) return v;
  }
  return undefined;
}

// Fallback code synthesized when the envelope omits one. invalid_request is the
// single canonical validation code the server emits for BOTH 400 (malformed)
// and 422 (semantically invalid), so both statuses map to it here.
const DEFAULT_CODE: Record<number, string> = {
  400: "invalid_request",
  401: "unauthorized",
  402: "limit_exceeded",
  403: "forbidden",
  404: "not_found",
  409: "conflict",
  422: "invalid_request",
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
    // Prefer the Retry-After header; fall back to details.retry_after_seconds
    // (the send-path 429 carries its hint there — F3).
    retryAfterSeconds: parseRetryAfter(args.headers) ?? retryAfterFromDetails(args.details),
    cause: args.cause,
  });
}

/** Map a generated `ApiException<ErrorEnvelope>` (thrown by the generated `*Api`
 *  classes on a non-2xx response) to a typed E2AError. */
export function fromApiException(e: ApiException<unknown>): E2AError {
  const headers = (e.headers ?? {}) as Record<string, string>;
  let requestId = headerGet(headers, "x-request-id");
  let code = "";
  // Do NOT seed message from e.message: the generated ApiException builds its
  // .message as a full dump ("HTTP-Code: …\nMessage: …\nBody: <raw>\nHeaders:
  // <all headers>"), which leaks the raw body + headers (incl. x-request-id).
  // Leave it undefined when the envelope is missing/unparseable; toE2AError then
  // synthesizes a clean "e2a API error (<status>)".
  let message: string | undefined;
  let details: unknown;

  // `e.body` is the parsed envelope for operations that declare a `default`
  // error response in the spec. Operations that declare ONLY success codes
  // (e.g. sendMessage / replyToMessage / forwardMessage) hand back the RAW
  // body STRING instead — parse it so the machine `code` and the clean
  // envelope message survive regardless of which operations declared `default`.
  let body: unknown = e.body;
  if (typeof body === "string") {
    try {
      body = JSON.parse(body);
    } catch {
      // Not JSON — leave `body` as the string; we fall through to the status
      // bucket below (no code), preserving the pre-fix behavior for that case.
    }
  }
  const env = body as Partial<ErrorEnvelope> | undefined;
  if (env && env.error) {
    code = env.error.code ?? "";
    message = env.error.message ?? message;
    details = env.error.details ?? undefined;
    // request_id lives in the envelope too; prefer the header, fall back to it.
    const bodyReqId = (env.error as { request_id?: unknown }).request_id;
    if (!requestId && typeof bodyReqId === "string") requestId = bodyReqId;
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
