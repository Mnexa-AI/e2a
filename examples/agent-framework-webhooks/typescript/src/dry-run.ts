import { createHmac } from "node:crypto";
import { pathToFileURL } from "node:url";

import {
  InboundResource,
  type AttachmentView,
  type ForwardInput,
  type InboundMessageOperations,
  type MessageView,
  type ReplyInput,
  type RequestOptions,
  type SendResultView,
} from "@e2a/sdk/v1";

import { FakeReplyAgent } from "./adapters/fake.js";
import { EventDeduper } from "./delivery-state.js";
import { handleDelivery } from "./handler.js";

const SECRET = "whsec_agent_framework_dry_run";

export interface DryRunEvidence {
  firstStatus: string;
  secondStatus: string;
  reply: string;
  replyCount: number;
}

class FakeMessageOperations implements InboundMessageOperations {
  readonly replies: Array<{ email: string; id: string; body: ReplyInput; idempotencyKey?: string }> = [];
  readonly message = {
    attachments: [],
    authentication: {
      spf: { status: "pass", domain: "example.net", aligned: true },
      dkim: [],
      dmarc: { status: "pass", domain: "example.net", policy: "reject", alignedBy: ["spf"] },
    },
    cc: [], conversationId: "", createdAt: new Date("2026-07-20T12:00:00.000Z"),
    deliveredTo: "agent@example.com", direction: "inbound", envelopeFrom: "sender@example.net",
    flagged: false, headerFrom: "Sender <sender@example.net>", id: "msg_dry_run", labels: [],
    parsed: { text: "Please send the deterministic response.", truncated: false },
    rawMessage: "U0VDUkVUIFJBVyBNSU1F", readStatus: "unread", replyTo: [], subject: "Dry run",
    to: ["agent@example.com"], verifiedDomain: "example.net",
  } as MessageView;

  async get(email: string, id: string): Promise<MessageView> {
    if (email !== "agent@example.com" || id !== "msg_dry_run") throw new Error("unexpected message fetch");
    return this.message;
  }
  async getAttachment(): Promise<AttachmentView> { throw new Error("fixture has no attachments"); }
  async reply(email: string, id: string, body: ReplyInput, options: RequestOptions = {}): Promise<SendResultView> {
    this.replies.push({ email, id, body, ...(options.idempotencyKey === undefined ? {} : { idempotencyKey: options.idempotencyKey }) });
    return { messageId: "msg_reply_dry_run", status: "accepted" } as SendResultView;
  }
  async forward(_email: string, _id: string, _body: ForwardInput): Promise<SendResultView> {
    throw new Error("dry run must not forward");
  }
}

function signedDelivery(): { body: Buffer; signature: string } {
  const body = Buffer.from(JSON.stringify({
    id: "evt_dry_run", type: "email.received", schema_version: "1",
    created_at: "2026-07-20T12:00:00.000Z",
    data: {
      message_id: "msg_dry_run", agent_email: "agent@example.com", direction: "inbound",
      conversation_id: "", header_from: "Sender <sender@example.net>",
      envelope_from: "sender@example.net", verified_domain: "example.net",
      to: ["agent@example.com"], cc: [], reply_to: [], delivered_to: "agent@example.com",
      subject: "Dry run", received_at: "2026-07-20T12:00:00.000Z", attachments: [],
      authentication: {
        spf: { status: "pass", domain: "example.net", aligned: true }, dkim: [],
        dmarc: { status: "pass", domain: "example.net", policy: "reject", aligned_by: ["spf"] },
      },
    },
  }));
  const timestamp = Math.floor(Date.now() / 1_000).toString();
  const digest = createHmac("sha256", SECRET).update(timestamp).update(".").update(body).digest("hex");
  return { body, signature: `t=${timestamp},v1=${digest}` };
}

export async function runDryRun({ print = true }: { print?: boolean } = {}): Promise<DryRunEvidence> {
  const operations = new FakeMessageOperations();
  const inbound = new InboundResource(operations);
  const agent = new FakeReplyAgent("Deterministic fake reply");
  const deduper = new EventDeduper();
  const delivery = signedDelivery();
  const input = { ...delivery, secret: SECRET, inbound, agent, deduper };
  const first = await handleDelivery(input);
  const second = await handleDelivery(input);
  if (operations.replies.length !== 1) throw new Error("dry run expected exactly one reply");
  const captured = operations.replies[0];
  if (!captured || captured.email !== "agent@example.com" || captured.id !== "msg_dry_run" ||
      captured.idempotencyKey !== "evt_dry_run" || captured.body.text !== "Deterministic fake reply") {
    throw new Error("dry run captured an unexpected bound reply");
  }
  const evidence = {
    firstStatus: first.status, secondStatus: second.status,
    reply: captured.body.text, replyCount: operations.replies.length,
  };
  if (print) console.log(`status=${evidence.firstStatus} status=${evidence.secondStatus} reply=${evidence.reply} reply_count=${evidence.replyCount}`);
  return evidence;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await runDryRun();
}
