import type { FieldError } from "../../src/v1/generated/models/FieldError.js";
import type { ErrorBody } from "../../src/v1/generated/models/ErrorBody.js";
import type {
  Authentication,
  EmailReceivedData,
  ListMessagesParams,
  SPFResult,
} from "../../src/v1/index.js";
import type { EmailSentData, WebhookEvent } from "../../src/v1/webhook-signature.js";
import type { WSEvent } from "../../src/v1/ws.js";
import { E2AClient } from "../../src/v1/client.js";
import type { InboundEmail } from "../../src/v1/inbound.js";

const senderFilter: ListMessagesParams = { from_: "alice@example.com" };
void senderFilter;

// The pre-GA breaking rename intentionally removes the old public spelling.
// @ts-expect-error `from` is the wire name; SDK callers use `from_`.
const removedSenderFilter: ListMessagesParams = { from: "alice@example.com" };
void removedSenderFilter;

const validationField: FieldError = { location: "", message: "invalid request" };
void validationField;

// @ts-expect-error location is required by the GA validation contract.
const missingValidationLocation: FieldError = { message: "invalid request" };
void missingValidationLocation;

const futureError: ErrorBody = {
  code: "future_error_code",
  message: "future failure",
  requestId: "req_future",
  details: { future_field: { nested: true } },
};
void futureError;

// @ts-expect-error requestId is required on every /v1 error.
const missingErrorRequestId: ErrorBody = { code: "invalid_request", message: "invalid" };
void missingErrorRequestId;

const eventEnvelope: WebhookEvent = {
  type: "email.received",
  id: "evt_1",
  schema_version: "1",
  created_at: "2026-07-01T10:30:00Z",
  data: {},
};
const wsEnvelope: WSEvent = eventEnvelope;
void wsEnvelope;

const wireAuthentication: Authentication = {
  spf: { status: "pass", domain: "example.com", aligned: true },
  dkim: [],
  dmarc: {
    status: "pass",
    domain: "example.com",
    policy: "reject",
    aligned_by: ["spf"],
  },
};
const receivedAuthentication: EmailReceivedData["authentication"] = wireAuthentication;
void receivedAuthentication;

// @ts-expect-error RFC 7208 has no SPF "policy" result.
const retiredSPFPolicy: SPFResult["status"] = "policy";
void retiredSPFPolicy;

const loopbackSentData: EmailSentData = {
  message_id: "msg_local",
  agent_email: "bot@example.com",
  direction: "outbound",
  method: "loopback",
  from: "bot@example.com",
  to: ["bot@example.com"],
  subject: "Note to self",
  message_type: "send",
};
void loopbackSentData;

const managedClient = new E2AClient({ apiKey: "e2a_test" });
managedClient.messages.send("sender@example.com", {
  to: ["recipient@example.net"],
  subject: "Update",
  text: "Hello",
  unsubscribe: { mode: "managed" },
});
managedClient.agents.listSuppressions("sender@example.com");
managedClient.agents.createSuppression("sender@example.com", {
  address: "recipient@example.net",
  reason: "recipient opted out",
});
managedClient.agents.deleteSuppression("sender@example.com", "recipient@example.net");

// @ts-expect-error all five core envelope fields are required.
const incompleteEventEnvelope: WebhookEvent = { type: "email.received", data: {} };
void incompleteEventEnvelope;

const inboundEmailPromise: Promise<InboundEmail> = managedClient.inbound.fromEvent(eventEnvelope);
inboundEmailPromise.then((email) => {
  const envelopeFrom: string | null = email.envelopeFrom;
  const verified: boolean = email.verified;
  const targets: readonly string[] = email.replyTargets;
  const result = email.reply({ text: "ok" });
  const attachment = email.attachments[0].get({ inline: true });
  void envelopeFrom;
  void verified;
  void targets;
  void result;
  void attachment;
});
