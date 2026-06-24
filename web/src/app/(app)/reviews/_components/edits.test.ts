import { diffApproveEdits, joinCSV, parseCSV } from "./edits";
import type { PendingMessageDetail } from "../../../components/types";

// makeMsg builds a minimal PendingMessageDetail fixture. Fields not
// supplied default to the agent-authored "draft" values the form would
// load from GET /v1/agents/{address}/messages/{id}.
function makeMsg(overrides: Partial<PendingMessageDetail> = {}): PendingMessageDetail {
  return {
    id: "msg_abc",
    agent_email: "bot@acme.io",
    direction: "outbound",
    subject: "original subject",
    type: "reply",
    to: ["alice@example.com"],
    cc: [],
    bcc: [],
    status: "pending_approval",
    created_at: "2026-05-23T00:00:00Z",
    body_text: "original body",
    body_html: "",
    ...overrides,
  };
}

// ── parseCSV / joinCSV ───────────────────────────────────

describe("parseCSV", () => {
  it("splits commas and trims whitespace", () => {
    expect(parseCSV("alice@x.com, bob@x.com")).toEqual([
      "alice@x.com",
      "bob@x.com",
    ]);
  });

  it("filters empty entries", () => {
    expect(parseCSV("alice@x.com, , bob@x.com")).toEqual([
      "alice@x.com",
      "bob@x.com",
    ]);
  });

  it("returns empty array for empty string", () => {
    expect(parseCSV("")).toEqual([]);
    expect(parseCSV("  ,  ")).toEqual([]);
  });

  it("joinCSV is the inverse", () => {
    expect(joinCSV(["alice@x.com", "bob@x.com"])).toBe(
      "alice@x.com, bob@x.com",
    );
    expect(joinCSV(undefined)).toBe("");
  });
});

// ── diffApproveEdits ─────────────────────────────────────

describe("diffApproveEdits", () => {
  const baseDraft = {
    subject: "original subject",
    bodyText: "original body",
    bodyHTML: "",
    to: "alice@example.com",
    cc: "",
    bcc: "",
  };

  it("returns empty payload when nothing changed", () => {
    expect(diffApproveEdits(makeMsg(), baseDraft)).toEqual({});
  });

  it("picks up subject changes only", () => {
    expect(
      diffApproveEdits(makeMsg(), { ...baseDraft, subject: "edited" }),
    ).toEqual({ subject: "edited" });
  });

  it("picks up body_text changes only", () => {
    expect(
      diffApproveEdits(makeMsg(), {
        ...baseDraft,
        bodyText: "edited body content",
      }),
    ).toEqual({ body: "edited body content" });
  });

  it("converts CSV recipients into string arrays", () => {
    const payload = diffApproveEdits(makeMsg(), {
      ...baseDraft,
      to: "alice@example.com, bob@example.com",
    });
    expect(payload.to).toEqual(["alice@example.com", "bob@example.com"]);
    // Other fields unchanged → absent from payload
    expect(payload.subject).toBeUndefined();
    expect(payload.body).toBeUndefined();
  });

  it("does NOT emit recipient fields when the CSV form just adds whitespace", () => {
    // "alice@example.com" vs "  alice@example.com  " → same parsed array
    const payload = diffApproveEdits(makeMsg(), {
      ...baseDraft,
      to: "  alice@example.com  ",
    });
    expect(payload.to).toBeUndefined();
  });

  it("treats clearing a recipient list as a real edit", () => {
    const msg = makeMsg({ cc: ["carol@example.com"] });
    const payload = diffApproveEdits(msg, {
      ...baseDraft,
      cc: "",
    });
    expect(payload.cc).toEqual([]);
  });

  it("collects multiple simultaneous edits into one payload", () => {
    const payload = diffApproveEdits(makeMsg(), {
      ...baseDraft,
      subject: "edited",
      bodyText: "edited body",
      to: "alice@example.com, bob@example.com",
    });
    expect(payload).toEqual({
      subject: "edited",
      body: "edited body",
      to: ["alice@example.com", "bob@example.com"],
    });
  });

  it("handles undefined subject on the loaded message (treats as empty)", () => {
    // Some messages may have missing/null fields if loaded mid-edit; the
    // diff should still produce a sensible override, not crash.
    const msg = makeMsg({ subject: undefined as unknown as string });
    expect(
      diffApproveEdits(msg, { ...baseDraft, subject: "" }),
    ).toEqual({});
    expect(
      diffApproveEdits(msg, { ...baseDraft, subject: "now set" }),
    ).toEqual({ subject: "now set" });
  });
});
