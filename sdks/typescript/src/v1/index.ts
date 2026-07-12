// Public surface of the e2a v1 SDK.
//
// The canonical request/response types are the OpenAPI-Generator `generated/`
// models; the hand-written ergonomic layer (E2AClient + resources, errors,
// retry, pagination, webhook verification, WS) wraps them. The legacy
// hand-written `api.ts` / `inbound-email.ts` surface and the old
// swag-generated types have been retired in favour of this.

// Generated request/response models (types + the small value classes).
export * from "./generated/models/all.js";

// High-level client and its per-resource parameter types.
export { E2AClient } from "./client.js";
export type {
  E2AClientOptions,
  RequestOptions,
  ListMessagesParams,
  ListEventsParams,
} from "./client.js";

// Typed error hierarchy.
export {
  E2AError,
  E2AAuthError,
  E2APermissionError,
  E2ANotFoundError,
  E2AConflictError,
  E2AValidationError,
  E2AIdempotencyError,
  E2ALimitExceededError,
  E2ARateLimitError,
  E2AServerError,
  E2AConnectionError,
  E2AWebhookSignatureError,
} from "./errors.js";

// Retry + auto-pagination primitives (exported for advanced configuration).
export { RetryHttpLibrary } from "./retry.js";
export type { RetryOptions } from "./retry.js";
export { AutoPager } from "./pagination.js";
export type { Page, FetchPage, AutoPagerOptions } from "./pagination.js";

// Webhook signature verification.
export { verifyWebhookSignature, constructEvent } from "./webhook-signature.js";
export type {
  VerifySignatureOptions,
  ConstructEventOptions,
  WebhookEvent,
  EmailReceivedPayload,
} from "./webhook-signature.js";

// Real-time WebSocket stream.
export { WSListener, WSStream } from "./ws.js";
export type { WSListenerOptions, WSListenerEvents, WSNotification } from "./ws.js";

// Friendly cross-language aliases for the most-used response shapes — mirror
// the names the Python SDK exports so users reach for the same vocabulary.
import type {
  PageMessageSummaryView,
  MessageSummaryView,
  SendResultView,
  DeploymentInfoView,
} from "./generated/index.js";
export type MessageList = PageMessageSummaryView;
export type MessageSummary = MessageSummaryView;
export type SendResult = SendResultView;
export type DeploymentInfo = DeploymentInfoView;
