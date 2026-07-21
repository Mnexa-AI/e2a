import type { InboundEmail } from "@e2a/sdk/v1";

import type { ReplyAgent } from "../contracts.js";
import { emailPrompt } from "../prompt.js";

export class FakeReplyAgent implements ReplyAgent {
  readonly prompts: string[] = [];

  constructor(readonly response = "Fake") {}

  get callCount(): number {
    return this.prompts.length;
  }

  async reply(email: InboundEmail, _conversationId: string): Promise<string> {
    this.prompts.push(emailPrompt(email));
    return this.response;
  }
}
