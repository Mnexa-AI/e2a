import { describe, expect, it } from "vitest";
import type { InboundEmail } from "@e2a/sdk/v1";

import { OpenAIReplyAgent } from "../src/agent.js";
import { emailPrompt } from "../src/prompt.js";

const email = {
  from: "Sender <sender@example.net>",
  inbox: "agent@example.com",
  subject: "Hello",
  text: "Please answer this question.",
  verified: true,
  flagged: false,
  message: { rawMessage: "raw MIME sentinel" },
} as unknown as InboundEmail;

describe("OpenAIReplyAgent", () => {
  it("projects only the safe email prompt into OpenAI", async () => {
    const prompts: string[] = [];
    const agent = new OpenAIReplyAgent(async (prompt) => {
      prompts.push(prompt);
      return { finalOutput: "OpenAI reply" };
    });

    await expect(agent.reply(email, "conv_evt_full")).resolves.toBe("OpenAI reply");
    expect(prompts).toEqual([emailPrompt(email)]);
    expect(prompts[0]).not.toContain("raw MIME sentinel");
  });

  it("returns an empty reply for a null final output", async () => {
    const agent = new OpenAIReplyAgent(async () => ({ finalOutput: null }));
    await expect(agent.reply(email, "conv_evt_full")).resolves.toBe("");
  });

  it("propagates provider errors", async () => {
    const failure = new Error("provider failed");
    const agent = new OpenAIReplyAgent(async () => { throw failure; });
    await expect(agent.reply(email, "conv_evt_full")).rejects.toBe(failure);
  });
});
