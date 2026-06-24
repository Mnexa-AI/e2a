// McpClient review-queue routing: the tools split across credential tiers, so
// they MUST hit tier-correct endpoints —
//   approve_message / reject_message  → ADMIN (account-only)  → sdk.reviews.*
//   list_pending / get_pending         → RUNTIME (agent-visible) → sdk.messages.*
// The account-only /v1/reviews path 403s an agent-scoped credential, so routing
// a runtime-tier tool through it is a regression. These tests pin the routing.

import { describe, it, expect, vi } from "vitest";
import { McpClient } from "../src/client.js";

function mockSdk() {
  return {
    reviews: {
      approve: vi.fn(async () => ({ messageId: "msg_p", status: "sent" })),
      reject: vi.fn(async () => ({ messageId: "msg_p", status: "rejected" })),
      get: vi.fn(async () => ({ messageId: "msg_p" })),
    },
    messages: {
      get: vi.fn(async () => ({ messageId: "msg_p" })),
      // ownerOfPending scans outbound to resolve the owning inbox.
      list: vi.fn(() => ({ toArray: async () => [{ messageId: "msg_p" }] })),
    },
    agents: {
      list: vi.fn(() => ({ toArray: async () => [{ email: "bot@test.dev" }] })),
    },
  };
}

describe("McpClient review routing (tier-correct endpoints)", () => {
  it("approveMessage → account-only reviews.approve (not messages.approve)", async () => {
    const sdk = mockSdk();
    const c = new McpClient(sdk as never, "bot@test.dev", "account");
    await c.approveMessage("msg_p", {});
    expect(sdk.reviews.approve).toHaveBeenCalledWith("msg_p", {});
  });

  it("rejectMessage → account-only reviews.reject", async () => {
    const sdk = mockSdk();
    const c = new McpClient(sdk as never, "bot@test.dev", "account");
    await c.rejectMessage("msg_p", "spam");
    expect(sdk.reviews.reject).toHaveBeenCalledWith("msg_p", { reason: "spam" });
  });

  it("getPendingMessage → agent-reachable messages.get, NOT account-only reviews.get", async () => {
    // Regression guard (PR #284 adversarial finding): get_pending_message is a
    // runtime-tier tool, so it must NOT route through sdk.reviews.get — that path
    // 403s an agent-scoped credential.
    const sdk = mockSdk();
    const c = new McpClient(sdk as never, "bot@test.dev", "agent");
    await c.getPendingMessage("msg_p");
    expect(sdk.messages.get).toHaveBeenCalledWith("bot@test.dev", "msg_p");
    expect(sdk.reviews.get).not.toHaveBeenCalled();
  });
});
