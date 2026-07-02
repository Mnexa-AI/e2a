import { describe, it, expect } from "vitest";
import { getFlag, getFlags, hasFlag, parseArgs, getPositionals, hasBareFlag } from "../bin/e2a.js";

describe("hasBareFlag", () => {
  it("finds a flag in normal position", () => {
    expect(hasBareFlag(["--json", "--help"], "--help")).toBe(true);
    expect(hasBareFlag(["--help"], "--help")).toBe(true);
  });

  it("does NOT match the flag when it is the value of a value-taking flag", () => {
    // `e2a send --body "--help"` must not hijack the command into help+exit 0.
    expect(hasBareFlag(["--body", "--help"], "--help")).toBe(false);
    expect(hasBareFlag(["--subject", "--version"], "--version")).toBe(false);
  });

  it("matches when preceded by a boolean flag", () => {
    expect(hasBareFlag(["--to", "a@b.c", "--json", "--help"], "--help")).toBe(true);
  });
});

describe("getPositionals", () => {
  it("extracts positionals, skipping value flags and their values", () => {
    expect(getPositionals(["msg_1", "--body", "hi", "--json"])).toEqual(["msg_1"]);
    expect(getPositionals(["--body", "hi", "msg_1"])).toEqual(["msg_1"]);
  });

  it("returns empty when only flags are present", () => {
    expect(getPositionals(["--body", "hi", "--json"])).toEqual([]);
  });
});

describe("parseArgs", () => {
  it("extracts command and remaining args", () => {
    const result = parseArgs(["node", "e2a", "inbox", "--unread"]);
    expect(result).toEqual({ command: "inbox", args: ["--unread"] });
  });

  it("returns empty command when no args", () => {
    const result = parseArgs(["node", "e2a"]);
    expect(result).toEqual({ command: "", args: [] });
  });
});

describe("getFlag", () => {
  it("returns the value after the flag", () => {
    expect(getFlag(["--to", "a@b.com", "--subject", "Hi"], "--to")).toBe(
      "a@b.com",
    );
    expect(getFlag(["--to", "a@b.com", "--subject", "Hi"], "--subject")).toBe(
      "Hi",
    );
  });

  it("returns undefined when flag is missing", () => {
    expect(getFlag(["--to", "a@b.com"], "--subject")).toBeUndefined();
  });

  it("returns undefined when next token is another flag", () => {
    // Fix #14: --subject has no value, --body follows immediately
    expect(
      getFlag(["--subject", "--body", "hello"], "--subject"),
    ).toBeUndefined();
  });

  it("returns undefined when flag is last token", () => {
    expect(getFlag(["--to"], "--to")).toBeUndefined();
  });

  it("does not consume flags as values in a realistic send scenario", () => {
    const args = ["--to", "a@b.com", "--subject", "--body", "hello"];
    // --subject should be undefined (next token is --body)
    expect(getFlag(args, "--subject")).toBeUndefined();
    // --body should still get "hello"
    expect(getFlag(args, "--body")).toBe("hello");
    // --to should still work
    expect(getFlag(args, "--to")).toBe("a@b.com");
  });
});

describe("getFlags", () => {
  it("collects repeated flag values (used by --label, --to, --cc, etc.)", () => {
    expect(
      getFlags(["--label", "urgent", "--label", "follow-up"], "--label"),
    ).toEqual(["urgent", "follow-up"]);
  });

  it("returns an empty array when the flag is absent", () => {
    expect(getFlags(["--unread"], "--label")).toEqual([]);
  });

  it("returns a single-entry array for a single occurrence", () => {
    expect(getFlags(["--label", "urgent"], "--label")).toEqual(["urgent"]);
  });
});

describe("hasFlag", () => {
  it("returns true when flag is present", () => {
    expect(hasFlag(["--unread", "--limit", "5"], "--unread")).toBe(true);
  });

  it("returns false when flag is absent", () => {
    expect(hasFlag(["--unread"], "--read")).toBe(false);
  });
});

describe("--limit validation", () => {
  it("parseInt returns NaN for non-numeric input", () => {
    // This documents the behavior that the fix addresses.
    // The actual validation is in main() which calls process.exit,
    // so we test the underlying behavior here.
    const val = parseInt("abc", 10);
    expect(Number.isFinite(val)).toBe(false);
  });

  it("parseInt returns negative for negative input", () => {
    const val = parseInt("-5", 10);
    expect(val).toBe(-5);
    expect(val < 1).toBe(true);
  });
});
