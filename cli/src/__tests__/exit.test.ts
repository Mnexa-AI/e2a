import { describe, expect, it } from "vitest";
import { EXIT, exitCodeForAPIError } from "../exit.js";

describe("API error exit classification", () => {
  it("reserves AUTH for credentials/scope and treats policy rejection as permanent request", () => {
    expect(exitCodeForAPIError({ code: "unauthorized", retryable: false })).toBe(EXIT.AUTH);
    expect(exitCodeForAPIError({ code: "forbidden", retryable: false })).toBe(EXIT.AUTH);
    expect(exitCodeForAPIError({ code: "blocked_by_policy", retryable: false })).toBe(EXIT.REQUEST);
  });

  it("keeps retryable API errors transient", () => {
    expect(exitCodeForAPIError({ code: "rate_limited", retryable: true })).toBe(EXIT.ERROR);
  });
});
