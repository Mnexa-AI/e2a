// Versioned v1 API types — generated from OpenAPI spec.
// Do not edit generated/ files directly; run `make generate` to regenerate.
export type * from "./generated/types.js";

// Hand-written v1 SDK surface
export { E2AApi, E2AApiError } from "./api.js";
export type { E2AApiOptions, SendOptions } from "./api.js";
export { E2AClient } from "./client.js";
export type { E2AClientOptions } from "./client.js";
export { InboundEmail, UnverifiedEmailError } from "./inbound-email.js";
export type { Attachment, AuthHeaders, WebhookPayload } from "./inbound-email.js";
export { WSListener, WSStream } from "./ws.js";
export type { WSListenerOptions, WSListenerEvents, WSNotification } from "./ws.js";

// Friendly aliases for the most-used response shapes. These mirror the
// types Python's SDK exports under the same names (MessageList, MessageSummary,
// SendResult), so cross-language users can reach for the same vocabulary
// without diving into `components["schemas"]`.
import type { components as _components } from "./generated/types.js";
export type MessageList = _components["schemas"]["ListMessagesResponse"];
export type MessageSummary = _components["schemas"]["MessageSummary"];
export type SendResult = _components["schemas"]["SendEmailResponse"];
export type DeploymentInfo = _components["schemas"]["DeploymentInfo"];
