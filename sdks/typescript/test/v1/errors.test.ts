import { describe, it, expect } from "vitest";
import {
  E2AError,
  E2AAuthError,
  E2APermissionError,
  E2ANotFoundError,
  E2AConflictError,
  E2AValidationError,
  E2AIdempotencyError,
  E2ARateLimitError,
  E2AServerError,
  E2AConnectionError,
  toE2AError,
  fromApiException,
  connectionError,
  isRetryableStatus,
} from "../../src/v1/errors.js";
import { ApiException } from "../../src/v1/oag/apis/exception.js";

describe("toE2AError status → class mapping", () => {
  const cases: Array<[number, any, boolean]> = [
    [401, E2AAuthError, false],
    [403, E2APermissionError, false],
    [404, E2ANotFoundError, false],
    [409, E2AConflictError, false],
    [422, E2AValidationError, false],
    [429, E2ARateLimitError, true],
    [500, E2AServerError, true],
    [503, E2AServerError, true],
  ];
  for (const [status, ctor, retryable] of cases) {
    it(`${status} → ${ctor.name} (retryable=${retryable})`, () => {
      const err = toE2AError({ status, code: "x", message: "m" });
      expect(err).toBeInstanceOf(ctor);
      expect(err).toBeInstanceOf(E2AError);
      expect(err.status).toBe(status);
      expect(err.retryable).toBe(retryable);
    });
  }
});

describe("idempotency codes route to E2AIdempotencyError", () => {
  it("idempotency_in_flight (409) is retryable", () => {
    const err = toE2AError({ status: 409, code: "idempotency_in_flight", message: "in flight" });
    expect(err).toBeInstanceOf(E2AIdempotencyError);
    expect(err.retryable).toBe(true);
  });
  it("idempotency_key_reuse (422) is NOT retryable", () => {
    const err = toE2AError({ status: 422, code: "idempotency_key_reuse", message: "reuse" });
    expect(err).toBeInstanceOf(E2AIdempotencyError);
    expect(err.retryable).toBe(false);
  });
});

describe("envelope details + headers", () => {
  it("preserves code, message, details, requestId, retry-after", () => {
    const err = toE2AError({
      status: 429,
      code: "rate_limited",
      message: "slow down",
      details: { resource: "send" },
      requestId: "req_abc",
      headers: { "retry-after": "12" },
    });
    expect(err.code).toBe("rate_limited");
    expect(err.message).toBe("slow down");
    expect(err.requestId).toBe("req_abc");
    expect(err.details).toEqual({ resource: "send" });
    expect(err.retryAfterSeconds).toBe(12);
  });

  it("unknown code falls back to the status bucket, not a bare E2AError", () => {
    const err = toE2AError({ status: 404, code: "recipient_unknown", message: "no" });
    expect(err).toBeInstanceOf(E2ANotFoundError);
    expect(err.code).toBe("recipient_unknown");
  });

  it("parses an HTTP-date Retry-After into retryAfterSeconds", () => {
    const err = toE2AError({
      status: 429,
      headers: { "retry-after": new Date(Date.now() + 10_000).toUTCString() },
    });
    // ~10s out, allowing for second-truncation + test timing.
    expect(err.retryAfterSeconds).toBeGreaterThanOrEqual(8);
    expect(err.retryAfterSeconds).toBeLessThanOrEqual(11);
  });
});

describe("fromApiException", () => {
  it("parses the {error:{code,message}} envelope + x-request-id header", () => {
    const apiEx = new ApiException(
      403,
      "HTTP 403",
      { error: { code: "forbidden", message: "agent not yours", details: null } },
      { "x-request-id": "req_xyz", "content-type": "application/json" },
    );
    const err = fromApiException(apiEx);
    expect(err).toBeInstanceOf(E2APermissionError);
    expect(err.code).toBe("forbidden");
    expect(err.message).toBe("agent not yours");
    expect(err.requestId).toBe("req_xyz");
  });

  it("tolerates a non-envelope body", () => {
    const apiEx = new ApiException(500, "boom", "plain text", {});
    const err = fromApiException(apiEx);
    expect(err).toBeInstanceOf(E2AServerError);
    expect(err.status).toBe(500);
    expect(err.retryable).toBe(true);
  });
});

describe("connectionError + isRetryableStatus", () => {
  it("connectionError is status 0, retryable", () => {
    const err = connectionError("ECONNREFUSED", new Error("refused"));
    expect(err).toBeInstanceOf(E2AConnectionError);
    expect(err.status).toBe(0);
    expect(err.retryable).toBe(true);
  });
  it("isRetryableStatus", () => {
    expect(isRetryableStatus(429)).toBe(true);
    expect(isRetryableStatus(408)).toBe(true);
    expect(isRetryableStatus(503)).toBe(true);
    expect(isRetryableStatus(404)).toBe(false);
    expect(isRetryableStatus(200)).toBe(false);
  });
});
