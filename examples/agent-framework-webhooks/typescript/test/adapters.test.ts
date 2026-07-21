import { describe, expect, it, vi } from "vitest";
import type { InboundEmail } from "@e2a/sdk/v1";

import {
  ADKReplyAgent,
  AnthropicReplyAgent,
  FakeReplyAgent,
  LangChainReplyAgent,
  OpenAIReplyAgent,
  createADKReplyAgent,
  createAnthropicReplyAgent,
  createLangChainReplyAgent,
  createOpenAIReplyAgent,
} from "../src/adapters/index.js";
import { REPLY_INSTRUCTIONS, emailPrompt } from "../src/prompt.js";

function inbound(overrides: Partial<InboundEmail> = {}): InboundEmail {
  return {
    from: "Ada Lovelace <Ada@Example.COM>",
    inbox: "Assistant@Example.com",
    conversationId: "conv_1",
    subject: "Question",
    text: "Can you help?",
    verified: true,
    flagged: false,
    ...overrides,
  } as InboundEmail;
}

async function* events(...values: unknown[]): AsyncGenerator<unknown> {
  for (const value of values) {
    yield value;
  }
}

describe("OpenAIReplyAgent", () => {
  it("passes only the safe email prompt and extracts finalOutput", async () => {
    const run = vi.fn().mockResolvedValue({ finalOutput: "OpenAI" });
    const email = inbound();

    await expect(new OpenAIReplyAgent(run).reply(email)).resolves.toBe("OpenAI");
    expect(run).toHaveBeenCalledWith(emailPrompt(email));
  });

  it("turns a null final output into an empty reply", async () => {
    const agent = new OpenAIReplyAgent(async () => ({ finalOutput: null }));
    await expect(agent.reply(inbound())).resolves.toBe("");
  });

  it("constructs the official Agent and run path", async () => {
    const Agent = vi.fn(function (this: object, options: unknown) {
      Object.assign(this, { options });
    });
    const run = vi.fn().mockResolvedValue({ finalOutput: "done" });
    const agent = createOpenAIReplyAgent({ Agent, run }, { OPENAI_MODEL: "gpt-test" });

    await agent.reply(inbound());

    expect(Agent).toHaveBeenCalledWith({
      name: "Email assistant",
      instructions: REPLY_INSTRUCTIONS,
      model: "gpt-test",
    });
    expect(run.mock.calls[0]?.[0]).toMatchObject({ options: expect.any(Object) });
    expect(run.mock.calls[0]?.[1]).toBe(emailPrompt(inbound()));
  });
});

describe("AnthropicReplyAgent", () => {
  it("joins only text content blocks", async () => {
    const run = vi.fn().mockResolvedValue({
      content: [
        { type: "text", text: "Claude one" },
        { type: "tool_use", text: "do not expose" },
        { type: "text", text: "Claude two" },
      ],
    });
    const email = inbound();

    await expect(new AnthropicReplyAgent(run).reply(email)).resolves.toBe(
      "Claude one\nClaude two",
    );
    expect(run).toHaveBeenCalledWith(emailPrompt(email));
  });

  it("constructs an official Messages request", async () => {
    const create = vi.fn().mockResolvedValue({ content: [] });
    const Anthropic = vi.fn(function (this: object) {
      Object.assign(this, { messages: { create } });
    });
    const agent = createAnthropicReplyAgent(
      {
        Anthropic: Anthropic as unknown as Parameters<typeof createAnthropicReplyAgent>[0]["Anthropic"],
      },
      { ANTHROPIC_MODEL: "claude-test" },
    );

    await agent.reply(inbound());

    expect(create).toHaveBeenCalledWith({
      model: "claude-test",
      max_tokens: 1024,
      system: REPLY_INSTRUCTIONS,
      messages: [{ role: "user", content: emailPrompt(inbound()) }],
    });
  });
});

describe("LangChainReplyAgent", () => {
  it("returns the final AI message with string content", async () => {
    const run = vi.fn().mockResolvedValue({
      messages: [
        { type: "ai", content: "earlier" },
        { type: "human", content: "followup" },
        { type: "ai", content: "LangChain" },
      ],
    });
    const email = inbound();

    await expect(new LangChainReplyAgent(run).reply(email)).resolves.toBe("LangChain");
    expect(run).toHaveBeenCalledWith(emailPrompt(email));
  });

  it("supports assistant message text-block content", async () => {
    const agent = new LangChainReplyAgent(async () => ({
      messages: [
        {
          role: "assistant",
          content: [
            { type: "text", text: "Block one" },
            { type: "image", text: "ignore" },
            { type: "text", text: "Block two" },
          ],
        },
      ],
    }));

    await expect(agent.reply(inbound())).resolves.toBe("Block one\nBlock two");
  });

  it("rejects a result without an assistant message", async () => {
    const agent = new LangChainReplyAgent(async () => ({
      messages: [{ type: "human", content: "not a reply" }],
    }));

    await expect(agent.reply(inbound())).rejects.toThrow(
      "LangChain result did not contain an assistant message",
    );
  });

  it("constructs and invokes an official LangChain agent", async () => {
    const invoke = vi.fn().mockResolvedValue({ messages: [] });
    const createAgent = vi.fn().mockReturnValue({ invoke });
    const agent = createLangChainReplyAgent(
      { createAgent },
      { LANGCHAIN_MODEL: "openai:test-model" },
    );

    await expect(agent.reply(inbound())).rejects.toThrow("assistant message");

    expect(createAgent).toHaveBeenCalledWith({
      model: "openai:test-model",
      tools: [],
      systemPrompt: REPLY_INSTRUCTIONS,
    });
    expect(invoke).toHaveBeenCalledWith({
      messages: [{ role: "user", content: emailPrompt(inbound()) }],
    });
  });
});

describe("ADKReplyAgent", () => {
  it("ignores non-final events and returns the last final response text", async () => {
    const run = vi.fn(() =>
      events(
        { isFinalResponse: () => false, content: { parts: [{ text: "draft" }] } },
        { isFinalResponse: () => true, content: { parts: [{ text: "ADK one" }] } },
        {
          isFinalResponse: () => true,
          content: { parts: [{ text: "ADK two" }, { functionCall: {} }, { text: "done" }] },
        },
      ),
    );
    const email = inbound();

    await expect(new ADKReplyAgent(run).reply(email)).resolves.toBe("ADK two\ndone");
    expect(run).toHaveBeenCalledWith({
      prompt: emailPrompt(email),
      userId: expect.stringMatching(/^sender-[0-9a-f]{20}$/),
      sessionId: "conv_1",
    });
  });

  it("canonicalizes sender identity and namespaces it by inbox", async () => {
    const inputs: unknown[] = [];
    const run = vi.fn((input) => {
      inputs.push(input);
      return events();
    });
    const agent = new ADKReplyAgent(run);

    await agent.reply(inbound());
    await agent.reply(inbound({ from: "  ada@example.com  ", inbox: "assistant@example.COM" }));
    await agent.reply(inbound({ from: "ada@example.com", inbox: "other@example.com" }));

    const userIds = inputs.map((value) => (value as { userId: string }).userId);
    expect(userIds[0]).toBe(userIds[1]);
    expect(userIds[2]).not.toBe(userIds[0]);
    expect(userIds[0]).not.toContain("ada");
    expect(userIds[0]).not.toContain("example.com");
  });

  it("constructs the official in-memory ADK runner path", async () => {
    const getOrCreateSession = vi.fn().mockResolvedValue({ id: "conv_1" });
    const runAsync = vi.fn(() =>
      events({ isFinalResponse: () => true, content: { parts: [{ text: "ADK" }] } }),
    );
    const LlmAgent = vi.fn(function (this: object, options: unknown) {
      Object.assign(this, { options });
    });
    const InMemoryRunner = vi.fn(function (this: object) {
      Object.assign(this, { sessionService: { getOrCreateSession }, runAsync });
    });
    const agent = createADKReplyAgent(
      {
        LlmAgent,
        InMemoryRunner: InMemoryRunner as unknown as Parameters<typeof createADKReplyAgent>[0]["InMemoryRunner"],
      },
      { ADK_MODEL: "gemini-test" },
    );

    await expect(agent.reply(inbound())).resolves.toBe("ADK");

    expect(LlmAgent).toHaveBeenCalledWith({
      name: "email_assistant",
      model: "gemini-test",
      instruction: REPLY_INSTRUCTIONS,
    });
    expect(InMemoryRunner).toHaveBeenCalledWith({ agent: expect.any(Object), appName: "e2a_email_assistant" });
    const context = expect.objectContaining({
      appName: "e2a_email_assistant",
      userId: expect.stringMatching(/^sender-/),
      sessionId: "conv_1",
    });
    expect(getOrCreateSession).toHaveBeenCalledWith(context);
    expect(runAsync).toHaveBeenCalledWith({
      userId: expect.stringMatching(/^sender-/),
      sessionId: "conv_1",
      newMessage: { role: "user", parts: [{ text: emailPrompt(inbound()) }] },
    });
  });
});

describe("FakeReplyAgent", () => {
  it("records prompts and returns a deterministic response", async () => {
    const agent = new FakeReplyAgent("Fake");
    const email = inbound();

    await expect(agent.reply(email)).resolves.toBe("Fake");
    expect(agent.callCount).toBe(1);
    expect(agent.prompts).toEqual([emailPrompt(email)]);
  });
});

describe("adapter failures", () => {
  it.each([
    ["OpenAI", () => new OpenAIReplyAgent(async () => Promise.reject(new Error("provider failed")))],
    ["Anthropic", () => new AnthropicReplyAgent(async () => Promise.reject(new Error("provider failed")))],
    ["LangChain", () => new LangChainReplyAgent(async () => Promise.reject(new Error("provider failed")))],
    [
      "ADK",
      () =>
        new ADKReplyAgent(async function* () {
          throw new Error("provider failed");
        }),
    ],
  ])("does not swallow %s errors", async (_name, makeAgent) => {
    await expect(makeAgent().reply(inbound())).rejects.toThrow("provider failed");
  });
});
