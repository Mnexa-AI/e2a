import type { InboundEmail, WebhookEvent } from "@e2a/sdk/v1";

/** An agent that produces a reply for an inbound email. */
export interface ReplyAgent {
  reply(email: InboundEmail): Promise<string>;
  close?(): Promise<void> | void;
}

/** Convert verified webhook events into normalized inbound email facades. */
export interface InboundResource {
  fromEvent(event: WebhookEvent): Promise<InboundEmail>;
}
