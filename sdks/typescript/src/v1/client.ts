// The e2a high-level client (Slice 8b). A thin, namespaced ergonomic layer over
// the generated `generated/` base: resource sub-clients (`client.agents`,
// `.messages`, …) wrap the generated `Promise*Api` classes (composition, never
// inheritance), map the generated `ApiException` to the typed `E2AError`
// hierarchy, unwrap envelope output bodies, and expose cursor lists as an
// `AutoPager`. The generated base supplies transport (the retry-wrapped
// `HttpLibrary`), bearer auth, models, and `ApiException`.

import { createConfiguration } from "./generated/configuration.js";
import { ServerConfiguration } from "./generated/servers.js";
import { IsomorphicFetchHttpLibrary } from "./generated/http/isomorphic-fetch.js";
import { ApiException } from "./generated/apis/exception.js";
import {
  PromiseAgentsApi,
  PromiseMessagesApi,
  PromiseConversationsApi,
  PromiseDomainsApi,
  PromiseEventsApi,
  PromiseWebhooksApi,
  PromiseAccountApi,
  PromiseMetaApi,
} from "./generated/types/PromiseAPI.js";
import type {
  AgentView,
  CreateAgentRequest,
  CreateAgentResponse,
  UpdateAgentRequest,
  MessageView,
  MessageSummaryView,
  SendEmailRequest,
  ReplyRequest,
  ForwardRequest,
  ApproveRequest,
  RejectInputBody,
  UpdateMessageRequest,
  UpdateMessageResultView,
  SendResultView,
  ApproveResultView,
  RejectResultView,
  ConversationSummaryView,
  ConversationDetailView,
  DomainView,
  RegisterDomainRequest,
  UpdateDomainRequest,
  VerifyDomainView,
  EventJSON,
  RedeliverEventInputBody,
  RedeliverView,
  WebhookView,
  CreateWebhookRequest,
  UpdateWebhookRequest,
  RotateSecretOutputBody,
  TestWebhookRequest,
  TestWebhookOutputBody,
  WebhookDeliveryView,
  LimitsView,
  UserExport,
  DeleteUserDataResult,
  Suppression,
  DeploymentInfoView,
} from "./generated/index.js";
import { RetryHttpLibrary, type RetryOptions } from "./retry.js";
import { E2AError, fromApiException, connectionError } from "./errors.js";
import { AutoPager } from "./pagination.js";
import { WSStream } from "./ws.js";

export interface E2AClientOptions {
  /** Account (`e2a_acct_`) or agent (`e2a_agt_`) key, or an OAuth access token.
   *  Falls back to `E2A_API_KEY`. */
  apiKey?: string;
  /** API base URL. Default `https://api.e2a.dev`; override for self-host. */
  baseUrl?: string;
  /** Max retry attempts on 429/5xx/connection (default 2). */
  maxRetries?: RetryOptions["maxRetries"];
  /** Optional total deadline across attempts (ms). */
  maxElapsedMs?: RetryOptions["maxElapsedMs"];
}

/** Per-call options for unsafe writes. */
export interface RequestOptions {
  /** Stable idempotency key. Omit and the SDK mints one (and reuses it across
   *  retries). Supply a stable value derived from the triggering event to also
   *  survive a process restart. */
  idempotencyKey?: string;
}

function envVar(name: string): string | undefined {
  if (typeof process !== "undefined" && process.env && process.env[name]) return process.env[name];
  return undefined;
}

// Map generated/transport failures to the typed hierarchy: ApiException →
// envelope-mapped E2AError; an already-typed E2AError passes through; anything
// else (a transport throw from the retry layer) is a connection error.
async function call<T>(fn: () => Promise<T>): Promise<T> {
  try {
    return await fn();
  } catch (e) {
    if (e instanceof E2AError) throw e;
    if (e instanceof ApiException) throw fromApiException(e);
    throw connectionError(e instanceof Error ? e.message : String(e), e);
  }
}

export class E2AClient {
  readonly agents: AgentsResource;
  readonly messages: MessagesResource;
  readonly conversations: ConversationsResource;
  readonly domains: DomainsResource;
  readonly events: EventsResource;
  readonly webhooks: WebhooksResource;
  readonly account: AccountResource;
  private readonly meta: PromiseMetaApi;
  private readonly apiKey: string;
  private readonly baseUrl: string;

  constructor(opts: E2AClientOptions = {}) {
    const apiKey = opts.apiKey ?? envVar("E2A_API_KEY");
    if (!apiKey) {
      throw new E2AError({
        code: "no_api_key",
        message: "apiKey is required — pass { apiKey } or set E2A_API_KEY",
        status: 0,
        retryable: false,
      });
    }
    const baseUrl = opts.baseUrl ?? envVar("E2A_BASE_URL") ?? "https://api.e2a.dev";
    this.apiKey = apiKey;
    this.baseUrl = baseUrl;
    const httpApi = new RetryHttpLibrary(new IsomorphicFetchHttpLibrary(), {
      maxRetries: opts.maxRetries,
      maxElapsedMs: opts.maxElapsedMs,
    });
    const config = createConfiguration({
      baseServer: new ServerConfiguration(baseUrl, {}),
      httpApi,
      authMethods: { bearer: { tokenProvider: { getToken: () => apiKey } } },
    });

    this.agents = new AgentsResource(new PromiseAgentsApi(config));
    this.messages = new MessagesResource(new PromiseMessagesApi(config));
    this.conversations = new ConversationsResource(new PromiseConversationsApi(config));
    this.domains = new DomainsResource(new PromiseDomainsApi(config));
    this.events = new EventsResource(new PromiseEventsApi(config));
    this.webhooks = new WebhooksResource(new PromiseWebhooksApi(config));
    this.account = new AccountResource(new PromiseAccountApi(config));
    this.meta = new PromiseMetaApi(config);
  }

  /** Public deployment metadata. */
  info(): Promise<DeploymentInfoView> {
    return call(() => this.meta.getInfo());
  }

  /**
   * Open a notification stream for an agent's inbox. Yields lightweight
   * {@link WSNotification}s; fetch the body with `client.messages.get(address, id)`
   * when you need it. `address` falls back to `E2A_AGENT_EMAIL`.
   *
   *     for await (const n of client.listen("bot@acme.dev")) { ... }
   */
  listen(address?: string): WSStream {
    const target = address ?? envVar("E2A_AGENT_EMAIL");
    if (!target) {
      throw new E2AError({
        code: "missing_address",
        message: "agentEmail is required — pass client.listen(address) or set E2A_AGENT_EMAIL",
        status: 0,
        retryable: false,
      });
    }
    return new WSStream({ apiKey: this.apiKey, agentEmail: target, baseUrl: this.baseUrl });
  }
}

class AgentsResource {
  constructor(private readonly api: PromiseAgentsApi) {}
  list(): AutoPager<AgentView> {
    return new AutoPager(async () => ({ items: (await call(() => this.api.listAgents())).agents ?? [] }));
  }
  get(address: string): Promise<AgentView> {
    return call(() => this.api.getAgent(address));
  }
  create(body: CreateAgentRequest): Promise<CreateAgentResponse> {
    return call(() => this.api.createAgent(body));
  }
  update(address: string, patch: UpdateAgentRequest): Promise<AgentView> {
    return call(() => this.api.updateAgent(address, patch));
  }
  async delete(address: string): Promise<void> {
    await call(() => this.api.deleteAgent(address));
  }
  test(address: string): Promise<SendResultView> {
    return call(() => this.api.testAgent(address));
  }
}

export interface ListMessagesParams {
  direction?: "inbound" | "outbound" | "all";
  status?: "unread" | "read" | "all";
  sort?: "asc" | "desc";
  from?: string;
  subjectContains?: string;
  conversationId?: string;
  labels?: string[];
  since?: string;
  until?: string;
  limit?: number;
}

class MessagesResource {
  constructor(private readonly api: PromiseMessagesApi) {}

  list(address: string, params: ListMessagesParams = {}): AutoPager<MessageSummaryView> {
    return new AutoPager(async (cursor) => {
      const page = await call(() =>
        this.api.listMessages(address, params.direction, params.status, params.sort, params.from,
          params.subjectContains, params.conversationId, params.labels, params.since, params.until,
          cursor, params.limit),
      );
      return { items: page.items ?? [], next_cursor: page.nextCursor };
    });
  }
  get(address: string, id: string): Promise<MessageView> {
    return call(() => this.api.getMessage(address, id));
  }
  send(address: string, body: SendEmailRequest, opts: RequestOptions = {}): Promise<SendResultView> {
    return call(() => this.api.sendMessage(address, body, opts.idempotencyKey));
  }
  reply(address: string, id: string, body: ReplyRequest, opts: RequestOptions = {}): Promise<SendResultView> {
    return call(() => this.api.replyToMessage(address, id, body, opts.idempotencyKey));
  }
  forward(address: string, id: string, body: ForwardRequest, opts: RequestOptions = {}): Promise<SendResultView> {
    return call(() => this.api.forwardMessage(address, id, body, opts.idempotencyKey));
  }
  approve(address: string, id: string, body: ApproveRequest = {}, opts: RequestOptions = {}): Promise<ApproveResultView> {
    return call(() => this.api.approveMessage(address, id, body, opts.idempotencyKey));
  }
  reject(address: string, id: string, body: RejectInputBody = {}): Promise<RejectResultView> {
    return call(() => this.api.rejectMessage(address, id, body));
  }
  updateLabels(address: string, id: string, body: UpdateMessageRequest): Promise<UpdateMessageResultView> {
    return call(() => this.api.updateMessage(address, id, body));
  }
}

class ConversationsResource {
  constructor(private readonly api: PromiseConversationsApi) {}
  async list(address: string, params: { since?: string; until?: string; limit?: number } = {}): Promise<ConversationSummaryView[]> {
    // listConversations has no cursor param — single page by contract.
    const page = await call(() => this.api.listConversations(address, params.since, params.until, params.limit));
    return page.items ?? [];
  }
  get(address: string, id: string): Promise<ConversationDetailView> {
    return call(() => this.api.getConversation(address, id));
  }
}

class DomainsResource {
  constructor(private readonly api: PromiseDomainsApi) {}
  list(): AutoPager<DomainView> {
    return new AutoPager(async () => ({ items: (await call(() => this.api.listDomains())).domains ?? [] }));
  }
  get(domain: string): Promise<DomainView> {
    return call(() => this.api.getDomain(domain));
  }
  create(body: RegisterDomainRequest): Promise<DomainView> {
    return call(() => this.api.registerDomain(body));
  }
  update(domain: string, patch: UpdateDomainRequest): Promise<DomainView> {
    return call(() => this.api.updateDomain(domain, patch));
  }
  async delete(domain: string): Promise<void> {
    await call(() => this.api.deleteDomain(domain));
  }
  verify(domain: string): Promise<VerifyDomainView> {
    return call(() => this.api.verifyDomain(domain));
  }
}

export interface ListEventsParams {
  type?: string;
  agentId?: string;
  conversationId?: string;
  messageId?: string;
  since?: string;
  until?: string;
  limit?: number;
}

class EventsResource {
  constructor(private readonly api: PromiseEventsApi) {}
  list(params: ListEventsParams = {}): AutoPager<EventJSON> {
    return new AutoPager(async (cursor) => {
      const page = await call(() =>
        this.api.listEvents(params.type, params.agentId, params.conversationId, params.messageId,
          params.since, params.until, cursor, params.limit),
      );
      return { items: page.items ?? [], next_cursor: page.nextCursor };
    });
  }
  get(id: string): Promise<EventJSON> {
    return call(() => this.api.getEvent(id));
  }
  redeliver(id: string, body: RedeliverEventInputBody = {}): Promise<RedeliverView> {
    return call(() => this.api.redeliverEvent(id, body));
  }
}

class WebhooksResource {
  constructor(private readonly api: PromiseWebhooksApi) {}
  list(): AutoPager<WebhookView> {
    return new AutoPager(async () => ({ items: (await call(() => this.api.listWebhooks())).webhooks ?? [] }));
  }
  get(id: string): Promise<WebhookView> {
    return call(() => this.api.getWebhook(id));
  }
  create(body: CreateWebhookRequest): Promise<WebhookView> {
    return call(() => this.api.createWebhook(body));
  }
  update(id: string, patch: UpdateWebhookRequest): Promise<WebhookView> {
    return call(() => this.api.updateWebhook(id, patch));
  }
  async delete(id: string): Promise<void> {
    await call(() => this.api.deleteWebhook(id));
  }
  rotateSecret(id: string): Promise<RotateSecretOutputBody> {
    return call(() => this.api.rotateWebhookSecret(id));
  }
  test(id: string, body: TestWebhookRequest = {}): Promise<TestWebhookOutputBody> {
    return call(() => this.api.testWebhook(id, body));
  }
  deliveries(id: string, params: { status?: "pending" | "delivered" | "failed"; limit?: number } = {}): AutoPager<WebhookDeliveryView> {
    return new AutoPager(async (cursor) => {
      void cursor; // listWebhookDeliveries has no cursor param — single page by contract.
      const page = await call(() => this.api.listWebhookDeliveries(id, params.status, params.limit));
      // Drop next_cursor: we can't pass it back (no cursor param), so surfacing
      // it would make the pager re-fetch the same page and trip the cycle guard.
      return { items: page.items ?? [], next_cursor: undefined };
    });
  }
}

class SuppressionsResource {
  constructor(private readonly api: PromiseAccountApi) {}
  list(): AutoPager<Suppression> {
    return new AutoPager(async () => ({ items: (await call(() => this.api.listSuppressions())).suppressions ?? [] }));
  }
  async delete(address: string): Promise<void> {
    await call(() => this.api.deleteSuppression(address));
  }
}

class AccountResource {
  readonly suppressions: SuppressionsResource;
  constructor(private readonly api: PromiseAccountApi) {
    this.suppressions = new SuppressionsResource(api);
  }
  get(): Promise<LimitsView> {
    return call(() => this.api.getAccount());
  }
  export(): Promise<UserExport> {
    return call(() => this.api.exportAccount());
  }
  delete(confirm?: string): Promise<DeleteUserDataResult> {
    return call(() => this.api.deleteAccount(confirm));
  }
}
