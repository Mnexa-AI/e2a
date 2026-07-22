import { constructEvent, type SendResultView } from "@e2a/sdk/v1";

import type { InboundResource, ReplyAgent } from "./contracts.js";
import { EventDeduper, conversationIdFor } from "./delivery-state.js";

export class DeliveryInProgress extends Error {
  readonly eventId: string;

  constructor(eventId: string) {
    super(`delivery ${eventId} is already in progress`);
    this.name = "DeliveryInProgress";
    this.eventId = eventId;
  }
}

export interface HandleDeliveryOptions {
  body: string | Uint8Array;
  signature: string;
  secret: string;
  inbound: InboundResource;
  agent: ReplyAgent;
  deduper: EventDeduper;
}

export type HandleDeliveryResult =
  | { kind: "ignored"; status: "ignored" }
  | { kind: "duplicate"; status: "duplicate" }
  | { kind: "no_reply"; status: "no_reply"; conversationId: string }
  | { kind: "replied"; status: "replied"; conversationId: string }
  | { kind: "send_result"; status: string; conversationId: string };

/** Verify, claim, and process one webhook delivery. */
export async function handleDelivery({
  body,
  signature,
  secret,
  inbound,
  agent,
  deduper,
}: HandleDeliveryOptions): Promise<HandleDeliveryResult> {
  const rawBody = typeof body === "string" ? body : Buffer.from(body.buffer, body.byteOffset, body.byteLength);
  const event = constructEvent(rawBody, signature, secret);
  if (event.type !== "email.received") return { kind: "ignored", status: "ignored" };

  const claim = await deduper.claim(event.id);
  if (claim === "processed") return { kind: "duplicate", status: "duplicate" };
  if (claim === "processing") throw new DeliveryInProgress(event.id);

  try {
    const email = await inbound.fromEvent(event);
    const conversationId = conversationIdFor(event.id, email.conversationId);
    const replyText = (await agent.reply(email, conversationId)).trim();
    if (replyText.length === 0) {
      await deduper.complete(event.id);
      return { kind: "no_reply", status: "no_reply", conversationId };
    }

    const result: SendResultView = await email.reply(
      { text: replyText, conversationId },
      { idempotencyKey: event.id },
    );
    await deduper.complete(event.id);
    const status = result.status === "accepted" || result.status === "sent" ? "replied" : result.status;
    return status === "replied"
      ? { kind: "replied", status, conversationId }
      : { kind: "send_result", status, conversationId };
  } catch (error: unknown) {
    await deduper.release(event.id);
    throw error;
  }
}
