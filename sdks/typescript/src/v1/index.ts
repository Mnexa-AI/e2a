// Versioned v1 API types — generated from OpenAPI spec.
// Do not edit generated/ files directly; run `make generate` to regenerate.
export type * from "./generated/types.js";

// Hand-written v1 SDK surface
export { E2AApi, E2AApiError } from "./api.js";
export type { E2AApiOptions } from "./api.js";
export { E2AClient } from "./client.js";
export type { E2AClientOptions } from "./client.js";
export { InboundEmail } from "./inbound-email.js";
export type { Attachment, AuthHeaders, WebhookPayload } from "./inbound-email.js";
export { WSListener } from "./ws.js";
export type { WSListenerOptions, WSListenerEvents, WSNotification } from "./ws.js";
