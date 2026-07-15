// McpClient review-queue routing: the tools split across credential tiers, so
// they MUST hit tier-correct endpoints —
//   approve_review / reject_review  → ADMIN (account-only)  → sdk.reviews.*
//   list_reviews / get_review          → RUNTIME (agent-visible) → sdk.messages.*
// The account-only /v1/reviews path 403s an agent-scoped credential, so routing
// a runtime-tier tool through it is a regression. These tests pin the routing.

import { describe, it, expect, vi } from "vitest";
import { McpClient } from "../src/client.js";

function mockSdk() {
  return {
    reviews: {
      approve: vi.fn(async () => ({ messageId: "msg_p", status: "sent" })),
      reject: vi.fn(async () => ({ messageId: "msg_p", status: "rejected" })),
      get: vi.fn(async () => ({ id: "msg_p" })),
    },
    messages: {
      get: vi.fn(async () => ({ id: "msg_p" })),
      // ownerOfPending scans outbound to resolve the owning inbox.
      list: vi.fn(() => ({ toArray: async () => [{ id: "msg_p" }] })),
    },
    agents: {
      list: vi.fn(() => ({ toArray: async () => [{ email: "bot@test.dev" }] })),
    },
  };
}

describe("McpClient trash/restore delegation", () => {
  function mockTrashSdk() {
    const agentsPage = vi.fn(async () => ({ items: [{ email: "trashed@test.dev" }], next_cursor: "agents_next" }));
    const messagesPage = vi.fn(async () => ({ items: [{ id: "msg_trashed" }], next_cursor: "messages_next" }));
    return {
      agentsPage,
      messagesPage,
      sdk: {
        agents: {
          list: vi.fn(() => ({ page: agentsPage })),
          restore: vi.fn(async (email: string) => ({ email })),
        },
        messages: {
          list: vi.fn(() => ({ page: messagesPage })),
          restore: vi.fn(async (email: string, id: string) => ({ email, id })),
        },
      },
    };
  }

  it("listAgents forwards deleted/limit and resumes from cursor", async () => {
    const { sdk, agentsPage } = mockTrashSdk();
    const c = new McpClient(sdk as never, "", "account");
    await c.listAgents({ deleted: true, limit: 25, cursor: "agents_cursor" });
    expect(sdk.agents.list).toHaveBeenCalledWith({ deleted: true, limit: 25 });
    expect(agentsPage).toHaveBeenCalledWith("agents_cursor");
  });

  it("restoreAgent forwards the resolved explicit address", async () => {
    const { sdk } = mockTrashSdk();
    const c = new McpClient(sdk as never, "", "account");
    await c.restoreAgent("trashed@test.dev");
    expect(sdk.agents.restore).toHaveBeenCalledWith("trashed@test.dev");
  });

  it("listMessages forwards deleted and explicitAddress without leaking it to the SDK filter", async () => {
    const { sdk, messagesPage } = mockTrashSdk();
    const c = new McpClient(sdk as never, "bound@test.dev", "agent");
    await c.listMessages({
      deleted: true,
      limit: 10,
      cursor: "messages_cursor",
      explicitAddress: "other@test.dev",
    });
    expect(sdk.messages.list).toHaveBeenCalledWith(
      "other@test.dev",
      { deleted: true, limit: 10 },
    );
    expect(messagesPage).toHaveBeenCalledWith("messages_cursor");
  });

  it("restoreMessage forwards message id and resolved explicit address", async () => {
    const { sdk } = mockTrashSdk();
    const c = new McpClient(sdk as never, "bound@test.dev", "agent");
    await c.restoreMessage("msg_trashed", "other@test.dev");
    expect(sdk.messages.restore).toHaveBeenCalledWith("other@test.dev", "msg_trashed");
  });
});

describe("McpClient review routing (tier-correct endpoints)", () => {
  it("approveReview → account-only reviews.approve (not messages.approve)", async () => {
    const sdk = mockSdk();
    const c = new McpClient(sdk as never, "bot@test.dev", "account");
    await c.approveReview("msg_p", {});
    expect(sdk.reviews.approve).toHaveBeenCalledWith("msg_p", {});
  });

  it("rejectReview → account-only reviews.reject", async () => {
    const sdk = mockSdk();
    const c = new McpClient(sdk as never, "bot@test.dev", "account");
    await c.rejectReview("msg_p", "spam");
    expect(sdk.reviews.reject).toHaveBeenCalledWith("msg_p", { reason: "spam" });
  });

  it("getReview → agent-reachable messages.get, NOT account-only reviews.get", async () => {
    // Regression guard (PR #284 adversarial finding): get_review is a
    // runtime-tier tool, so it must NOT route through sdk.reviews.get — that path
    // 403s an agent-scoped credential.
    const sdk = mockSdk();
    const c = new McpClient(sdk as never, "bot@test.dev", "agent");
    await c.getReview("msg_p");
    expect(sdk.messages.get).toHaveBeenCalledWith("bot@test.dev", "msg_p");
    expect(sdk.reviews.get).not.toHaveBeenCalled();
  });

  it("getReview on a missing/resolved pending draft throws not_found, not invalid_request", async () => {
    // PR #453 review: an already-approved/rejected/expired draft is a
    // not-found condition — a CodedError with the server's canonical
    // `not_found` code, so structuredError doesn't mislabel it as a
    // caller-input problem.
    const sdk = mockSdk();
    const c = new McpClient(sdk as never, "bot@test.dev", "agent");
    await expect(c.getReview("msg_gone")).rejects.toMatchObject({
      code: "not_found",
      message: expect.stringContaining("already been approved, rejected, or expired"),
    });
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
        list: vi.fn(() => ({ page: async () => ({ items: [{ id: "tmpl_1", name: "Welcome" }], next_cursor: undefined }) })),
        get: vi.fn(async (id: string) => ({ id, name: "Welcome", htmlBody: "<p>Hi</p>" })),
        create: vi.fn(async () => ({ id: "tmpl_new", name: "Approvals" })),
        update: vi.fn(async (id: string) => ({ id, name: "Welcome" })),
        delete: vi.fn(async () => undefined),
        validate: vi.fn(async () => ({ valid: true, errors: [], suggestedData: { name: "example" } })),
        listStarters: vi.fn(() => ({ page: async () => ({ items: [{ alias: "welcome" }], next_cursor: undefined }) })),
        getStarter: vi.fn(async (alias: string) => ({ alias, body: "Hi {{name}}" })),
      },
    };
  }

  it("routes every template method through sdk.templates", async () => {
    const sdk = mockTemplatesSdk();
    const c = new McpClient(sdk as never, "", "account");

    expect(await c.listTemplates()).toEqual({ items: [{ id: "tmpl_1", name: "Welcome" }], next_cursor: undefined });
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

    expect(await c.listStarterTemplates()).toEqual({ items: [{ alias: "welcome" }], next_cursor: undefined });
    expect(sdk.templates.listStarters).toHaveBeenCalledOnce();

    await c.getStarterTemplate("welcome");
    expect(sdk.templates.getStarter).toHaveBeenCalledWith("welcome");
  });
});

describe("McpClient API keys (agent-scope-only minting)", () => {
  const mockApiKeysSdk = () => ({
    account: {
      apiKeys: {
        list: vi.fn(() => ({ page: async () => ({ items: [{ id: "key_1" }], next_cursor: undefined }) })),
        create: vi.fn(async (req: Record<string, unknown>) => ({ id: "key_new", key: "plaintext-once", ...req })),
        delete: vi.fn(async (id: string) => ({ deleted: true, id })),
      },
    },
  });

  // THE privilege boundary: the real wrapper (not a stub) must stamp
  // scope=agent on the SDK request. The MCP tool suite stubs the wrapper, so
  // this is the only test observing the enforcement line itself.
  it("createAgentApiKey hardwires scope=agent on the SDK request", async () => {
    const sdk = mockApiKeysSdk();
    const c = new McpClient(sdk as never, "", "account");
    await c.createAgentApiKey({ agentEmail: "bot@test.dev", name: "ci" });
    expect(sdk.account.apiKeys.create).toHaveBeenCalledWith(
      expect.objectContaining({ scope: "agent", agentEmail: "bot@test.dev", name: "ci" }),
    );
  });

  it("createAgentApiKey stamps scope=agent even over a smuggled scope field", async () => {
    const sdk = mockApiKeysSdk();
    const c = new McpClient(sdk as never, "", "account");
    // The types forbid a scope param; simulate a caller forcing one past them —
    // the wrapper's spread order must still win.
    await c.createAgentApiKey({ agentEmail: "bot@test.dev", scope: "account" } as never);
    const req = sdk.account.apiKeys.create.mock.calls[0]![0] as { scope: string };
    expect(req.scope).toBe("agent");
  });

  it("listApiKeys pages and deleteApiKey returns the SDK's typed deletion receipt", async () => {
    const sdk = mockApiKeysSdk();
    const c = new McpClient(sdk as never, "", "account");
    expect(await c.listApiKeys()).toEqual({ items: [{ id: "key_1" }], next_cursor: undefined });
    expect(await c.deleteApiKey("key_1")).toEqual({ deleted: true, id: "key_1" });
    expect(sdk.account.apiKeys.delete).toHaveBeenCalledWith("key_1");
  });
});
