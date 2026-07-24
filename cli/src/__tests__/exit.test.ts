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

describe("exit code contract", () => {
  it("published values are frozen — add codes, never renumber", () => {
    expect(EXIT.OK).toBe(0);
    expect(EXIT.ERROR).toBe(1);
    expect(EXIT.USAGE).toBe(2);
    expect(EXIT.HELD).toBe(3);
    expect(EXIT.AUTH).toBe(4);
    expect(EXIT.REQUEST).toBe(5);
    expect(EXIT.TIMEOUT).toBe(6);
    expect(EXIT.SEND_OUTCOME).toBe(7);
    expect(EXIT.WARN).toBe(8);
    expect(EXIT.CONFIG).toBe(9);
  });
});
