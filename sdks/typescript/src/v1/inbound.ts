import type {
  AttachmentMetaView,
  AttachmentView,
  Authentication,
  MessageView,
  SendResultView,
} from "./generated/index.js";
import { E2AValidationError } from "./errors.js";
import type { ForwardInput, ReplyInput, RequestOptions } from "./client.js";
import type { EmailReceivedData, WebhookEvent } from "./webhook-signature.js";

export type EmailReceivedEvent = WebhookEvent & {
  type: "email.received";
  schema_version: "1";
  data: EmailReceivedData;
};

export interface InboundMessageOperations {
  get(email: string, id: string): Promise<MessageView>;
  getAttachment(
    email: string,
    id: string,
    index: number,
    options?: { inline?: boolean },
  ): Promise<AttachmentView>;
  reply(
    email: string,
    id: string,
    body: ReplyInput,
    options?: RequestOptions,
  ): Promise<SendResultView>;
  forward(
    email: string,
    id: string,
    body: ForwardInput,
    options?: RequestOptions,
  ): Promise<SendResultView>;
}

export interface InboundAttachmentProjection {
  content_id?: string;
  content_type?: string;
  filename?: string;
  index: number;
  size_bytes: number;
}

export interface InboundProjection {
  id: string;
  inbox: string;
  conversation_id: string;
  from: string | null;
  envelope_from: string | null;
  verified: boolean;
  to: string[];
  cc: string[];
  reply_to: string[];
  reply_targets: string[];
  subject: string;
  text: string;
  html?: string;
  text_truncated: boolean;
  received_at: string;
  flagged: boolean;
  flag_reason?: string;
  attachments: InboundAttachmentProjection[];
}

function invalidEvent(message: string): E2AValidationError {
  return new E2AValidationError({
    code: "invalid_email_received_event",
    message,
    status: 0,
    retryable: false,
  });
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.length > 0;
}

function validateEvent(event: WebhookEvent): EmailReceivedEvent {
  if (!event || typeof event !== "object") {
    throw invalidEvent("expected an email.received event object");
  }
  if (event.schema_version !== "1" || event.type !== "email.received") {
    throw invalidEvent("expected a schema-v1 email.received event");
  }
  if (!event.data || typeof event.data !== "object" || Array.isArray(event.data)) {
    throw invalidEvent("email.received data must be an object");
  }
  const data = event.data as Record<string, unknown>;
  if (!isNonEmptyString(data.message_id) || !isNonEmptyString(data.delivered_to)) {
    throw invalidEvent("email.received requires non-empty message_id and delivered_to fetch keys");
  }
  return event as EmailReceivedEvent;
}

function attachmentProjection(meta: AttachmentMetaView): InboundAttachmentProjection {
  return {
    ...(meta.contentId === undefined ? {} : { content_id: meta.contentId }),
    ...(meta.contentType === undefined ? {} : { content_type: meta.contentType }),
    ...(meta.filename === undefined ? {} : { filename: meta.filename }),
    index: meta.index,
    size_bytes: meta.sizeBytes,
  };
}

export class InboundAttachment {
  readonly contentId?: string;
  readonly contentType?: string;
  readonly filename?: string;
  readonly index: number;
  readonly sizeBytes: number;

  constructor(
    meta: AttachmentMetaView,
    private readonly inbox: string,
    private readonly messageId: string,
    private readonly messages: InboundMessageOperations,
  ) {
    this.contentId = meta.contentId;
    this.contentType = meta.contentType;
    this.filename = meta.filename;
    this.index = meta.index;
    this.sizeBytes = meta.sizeBytes;
  }

  get(options: { inline?: boolean } = {}): Promise<AttachmentView> {
    return this.messages.getAttachment(this.inbox, this.messageId, this.index, options);
  }

  toJSON(): InboundAttachmentProjection {
    return attachmentProjection(this);
  }
}

export class InboundEmail {
  readonly id: string;
  readonly inbox: string;
  readonly conversationId: string;
  readonly from: string | null;
  readonly envelopeFrom: string | null;
  readonly authentication: Authentication | null;
  readonly verified: boolean;
  readonly to: readonly string[];
  readonly cc: readonly string[];
  readonly replyTo: readonly string[];
  readonly replyTargets: readonly string[];
  readonly subject: string;
  readonly text: string;
  readonly html?: string;
  readonly textTruncated: boolean;
  readonly receivedAt: Date;
  readonly flagged: boolean;
  readonly flagReason?: string;
  readonly attachments: readonly InboundAttachment[];

  constructor(
    readonly event: EmailReceivedEvent,
    readonly message: MessageView,
    private readonly messages: InboundMessageOperations,
  ) {
    this.id = message.id;
    this.inbox = message.deliveredTo;
    this.conversationId = message.conversationId;
    this.from = message.headerFrom;
    this.envelopeFrom = message.envelopeFrom;
    this.authentication = message.authentication;
    this.verified = message.authentication?.dmarc.status === "pass";
    this.to = Object.freeze([...message.to]);
    this.cc = Object.freeze([...message.cc]);
    this.replyTo = Object.freeze([...message.replyTo]);
    this.replyTargets = Object.freeze(
      message.replyTo.length > 0 ? [...message.replyTo] : message.headerFrom === null ? [] : [message.headerFrom],
    );
    this.subject = message.subject;
    this.text = message.parsed?.text ?? "";
    this.html = message.parsed?.html;
    this.textTruncated = message.parsed?.truncated ?? false;
    this.receivedAt = new Date(message.createdAt);
    this.flagged = message.flagged ?? false;
    this.flagReason = message.flagReason;
    this.attachments = Object.freeze(message.attachments.map(
      (meta) => new InboundAttachment(meta, this.inbox, this.id, messages),
    ));
  }

  reply(body: ReplyInput, options: RequestOptions = {}): Promise<SendResultView> {
    return this.messages.reply(this.inbox, this.id, body, options);
  }

  forward(body: ForwardInput, options: RequestOptions = {}): Promise<SendResultView> {
    return this.messages.forward(this.inbox, this.id, body, options);
  }

  toJSON(): InboundProjection {
    return {
      id: this.id,
      inbox: this.inbox,
      conversation_id: this.conversationId,
      from: this.from,
      envelope_from: this.envelopeFrom,
      verified: this.verified,
      to: [...this.to],
      cc: [...this.cc],
      reply_to: [...this.replyTo],
      reply_targets: [...this.replyTargets],
      subject: this.subject,
      text: this.text,
      ...(this.html === undefined ? {} : { html: this.html }),
      text_truncated: this.textTruncated,
      received_at: this.receivedAt.toISOString(),
      flagged: this.flagged,
      ...(this.flagReason === undefined ? {} : { flag_reason: this.flagReason }),
      attachments: this.attachments.map((attachment) => attachment.toJSON()),
    };
  }
}

export class InboundResource {
  constructor(private readonly messages: InboundMessageOperations) {}

  async fromEvent(event: WebhookEvent): Promise<InboundEmail> {
    const received = validateEvent(event);
    const message = await this.messages.get(received.data.delivered_to, received.data.message_id);
    return new InboundEmail(received, message, this.messages);
  }
}
