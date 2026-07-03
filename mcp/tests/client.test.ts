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

// Templates (beta) ride the SDK's `templates` resource through the shared
// E2AClient — same retries/typed errors/camelCase views as every other tool.
// These pin the delegation: every McpClient template method must route to
// sdk.templates (no parallel HTTP stack), collapsing the single-page list
// pagers to flat arrays.
describe("McpClient templates (SDK-backed)", () => {
  function mockTemplatesSdk() {
    return {
      templates: {
        list: vi.fn(() => ({ toArray: async () => [{ id: "tmpl_1", name: "Welcome" }] })),
        get: vi.fn(async (id: string) => ({ id, name: "Welcome", htmlBody: "<p>Hi</p>" })),
        create: vi.fn(async () => ({ id: "tmpl_new", name: "Approvals" })),
        update: vi.fn(async (id: string) => ({ id, name: "Welcome" })),
        delete: vi.fn(async () => undefined),
        validate: vi.fn(async () => ({ valid: true, errors: [], suggestedData: { name: "example" } })),
        listStarters: vi.fn(() => ({ toArray: async () => [{ alias: "welcome" }] })),
        getStarter: vi.fn(async (alias: string) => ({ alias, body: "Hi {{name}}" })),
      },
    };
  }

  it("routes every template method through sdk.templates", async () => {
    const sdk = mockTemplatesSdk();
    const c = new McpClient(sdk as never, "", "account");

    expect(await c.listTemplates()).toEqual([{ id: "tmpl_1", name: "Welcome" }]);
    expect(sdk.templates.list).toHaveBeenCalledOnce();

    await c.getTemplate("tmpl_1");
    expect(sdk.templates.get).toHaveBeenCalledWith("tmpl_1");

    await c.createTemplate({ fromStarter: "welcome", alias: "my-welcome" });
    expect(sdk.templates.create).toHaveBeenCalledWith({ fromStarter: "welcome", alias: "my-welcome" });

    await c.updateTemplate("tmpl_1", { subject: "New {{x}}", htmlBody: "" });
    expect(sdk.templates.update).toHaveBeenCalledWith("tmpl_1", { subject: "New {{x}}", htmlBody: "" });

    await c.deleteTemplate("tmpl_1");
    expect(sdk.templates.delete).toHaveBeenCalledWith("tmpl_1");

    await c.validateTemplate({ subject: "Hi {{name}}", testData: { name: "Ada" } });
    expect(sdk.templates.validate).toHaveBeenCalledWith({ subject: "Hi {{name}}", testData: { name: "Ada" } });

    expect(await c.listStarterTemplates()).toEqual([{ alias: "welcome" }]);
    expect(sdk.templates.listStarters).toHaveBeenCalledOnce();

    await c.getStarterTemplate("welcome");
    expect(sdk.templates.getStarter).toHaveBeenCalledWith("welcome");
  });
});
