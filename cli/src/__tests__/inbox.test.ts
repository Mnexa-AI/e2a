import { describe, it, expect } from "vitest";

// Test the column width logic extracted from inbox.ts
// We test the formatting logic directly since the command itself
// calls process.exit and requires API calls.

describe("inbox column widths", () => {
  function computeIdWidth(messageIds: string[]): number {
    return Math.max(4, ...messageIds.map((id) => id.length)) + 2;
  }

  it("uses minimum width of 6 for short IDs", () => {
    const width = computeIdWidth(["msg1"]);
    expect(width).toBe(6); // max(4, 4) + 2
  });

  it("expands to fit long message IDs", () => {
    const longId = "msg_abc123def456ghi789jkl012mno345";
    const width = computeIdWidth([longId]);
    expect(width).toBe(longId.length + 2);
  });

  it("never truncates the longest ID", () => {
    const ids = ["msg_short", "msg_this_is_a_very_long_message_id_that_exceeds_20_chars"];
    const width = computeIdWidth(ids);
    const longestId = ids[1];
    // The padded ID should contain the full original
    expect(longestId.padEnd(width).startsWith(longestId)).toBe(true);
    expect(longestId.padEnd(width).length).toBe(width);
  });

  it("formats table row with full ID visible", () => {
    const longId = "msg_abc123def456ghi789jkl012mno345";
    const idW = computeIdWidth([longId]);
    const formatted = longId.padEnd(idW);
    // Full ID is present, not truncated
    expect(formatted.trim()).toBe(longId);
  });
});
