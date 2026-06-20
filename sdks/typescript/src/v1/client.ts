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
  UpdateAgentRequest,
  MessageView,
  MessageSummaryView,
  SendEmailRequest,
  ReplyRequest,
  ForwardRequest,
  ApproveRequest,
  RejectRequest,
  UpdateMessageRequest,
  UpdateMessageResultView,
  SendResultView,
  RejectResultView,
  ConversationSummaryView,
  ConversationDetailView,
  DomainView,
  RegisterDomainRequest,
  UpdateDomainRequest,
  VerifyDomainView,
  EventJSON,
  RedeliverEventRequest,
  RedeliverView,
  WebhookView,
  CreateWebhookResponse,
  CreateWebhookRequest,
  UpdateWebhookRequest,
  RotateSecretResponse,
  TestWebhookRequest,
  TestWebhookResponse,
  WebhookDeliveryView,
  AccountView,
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
   * {@link WSNotification}s; fetch the body with `client.messages.get(email, id)`
   * when you need it.
   *
   *     for await (const n of client.listen("bot@acme.dev")) { ... }
   */
  listen(email: string): WSStream {
    if (!email) {
      throw new E2AError({
        code: "missing_email",
        message: "email is required — pass client.listen(email)",
        status: 0,
        retryable: false,
      });
    }
    return new WSStream({ apiKey: this.apiKey, agentEmail: email, baseUrl: this.baseUrl });
  }
}

class AgentsResource {
  constructor(private readonly api: PromiseAgentsApi) {}
  list(): AutoPager<AgentView> {
    // No cursor param: omit next_cursor so the pager stops after one page — it
    // can't forward a cursor, and surfacing one would re-fetch page 1 and trip
    // the cycle guard. Single-page at GA; see webhooks.deliveries.
    return new AutoPager(async () => ({ items: (await call(() => this.api.listAgents())).items ?? [] }));
  }
  get(email: string): Promise<AgentView> {
    return call(() => this.api.getAgent(email));
  }
  create(body: CreateAgentRequest): Promise<AgentView> {
    return call(() => this.api.createAgent(body));
  }
  update(email: string, patch: UpdateAgentRequest): Promise<AgentView> {
    return call(() => this.api.updateAgent(email, patch));
  }
  async delete(email: string): Promise<void> {
    // The typed .delete() call is itself the confirmation; the ?confirm=DELETE
    // guard exists to protect raw/curl callers (AG-6).
    await call(() => this.api.deleteAgent(email, "DELETE"));
  }
  test(email: string): Promise<SendResultView> {
    return call(() => this.api.testAgent(email));
  }
}

export interface ListMessagesParams {
  direction?: "inbound" | "outbound" | "all";
  readStatus?: "unread" | "read" | "all";
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

  list(email: string, params: ListMessagesParams = {}): AutoPager<MessageSummaryView> {
    return new AutoPager(async (cursor) => {
      const page = await call(() =>
        this.api.listMessages(email, params.direction, params.readStatus, params.sort, params.from,
          params.subjectContains, params.conversationId, params.labels, params.since, params.until,
          cursor, params.limit),
      );
      return { items: page.items ?? [], next_cursor: page.nextCursor };
    });
  }
  get(email: string, id: string): Promise<MessageView> {
    return call(() => this.api.getMessage(email, id));
  }
  send(email: string, body: SendEmailRequest, opts: RequestOptions = {}): Promise<SendResultView> {
    return call(() => this.api.sendMessage(email, body, opts.idempotencyKey));
  }
  reply(email: string, id: string, body: ReplyRequest, opts: RequestOptions = {}): Promise<SendResultView> {
    return call(() => this.api.replyToMessage(email, id, body, opts.idempotencyKey));
  }
  forward(email: string, id: string, body: ForwardRequest, opts: RequestOptions = {}): Promise<SendResultView> {
    return call(() => this.api.forwardMessage(email, id, body, opts.idempotencyKey));
  }
  approve(email: string, id: string, body: ApproveRequest = {}, opts: RequestOptions = {}): Promise<SendResultView> {
    return call(() => this.api.approveMessage(email, id, body, opts.idempotencyKey));
  }
  reject(email: string, id: string, body: RejectRequest = {}): Promise<RejectResultView> {
    return call(() => this.api.rejectMessage(email, id, body));
  }
  updateLabels(email: string, id: string, body: UpdateMessageRequest): Promise<UpdateMessageResultView> {
    return call(() => this.api.updateMessage(email, id, body));
  }
}

class ConversationsResource {
  constructor(private readonly api: PromiseConversationsApi) {}
  // Returns an AutoPager for ergonomic consistency with every other `.list()`.
  // Cursor-paginated (CV-3): the AutoPager walks next_cursor to completion.
  list(email: string, params: { since?: string; until?: string; limit?: number } = {}): AutoPager<ConversationSummaryView> {
    return new AutoPager(async (cursor) => {
      const page = await call(() => this.api.listConversations(email, params.since, params.until, cursor, params.limit));
      return { items: page.items ?? [], next_cursor: page.nextCursor };
    });
  }
  get(email: string, id: string): Promise<ConversationDetailView> {
    return call(() => this.api.getConversation(email, id));
  }
}

class DomainsResource {
  constructor(private readonly api: PromiseDomainsApi) {}
  list(): AutoPager<DomainView> {
    // No cursor param: single-page at GA — see AgentsResource.list / deliveries.
    return new AutoPager(async () => ({ items: (await call(() => this.api.listDomains())).items ?? [] }));
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
    await call(() => this.api.deleteDomain(domain, "DELETE"));
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
  redeliver(id: string, body: RedeliverEventRequest = {}): Promise<RedeliverView> {
    return call(() => this.api.redeliverEvent(id, body));
  }
}

class WebhooksResource {
  constructor(private readonly api: PromiseWebhooksApi) {}
  list(): AutoPager<WebhookView> {
    // No cursor param: single-page at GA — see AgentsResource.list / deliveries.
    return new AutoPager(async () => ({ items: (await call(() => this.api.listWebhooks())).items ?? [] }));
  }
  get(id: string): Promise<WebhookView> {
    return call(() => this.api.getWebhook(id));
  }
  create(body: CreateWebhookRequest): Promise<CreateWebhookResponse> {
    return call(() => this.api.createWebhook(body));
  }
  update(id: string, patch: UpdateWebhookRequest): Promise<WebhookView> {
    return call(() => this.api.updateWebhook(id, patch));
  }
  async delete(id: string): Promise<void> {
    await call(() => this.api.deleteWebhook(id));
  }
  rotateSecret(id: string): Promise<RotateSecretResponse> {
    return call(() => this.api.rotateWebhookSecret(id));
  }
  test(id: string, body: TestWebhookRequest = {}): Promise<TestWebhookResponse> {
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
    return new AutoPager(async (cursor) => {
      const page = await call(() => this.api.listSuppressions(cursor));
      return { items: page.items ?? [], next_cursor: page.nextCursor };
    });
  }
  async delete(email: string): Promise<void> {
    await call(() => this.api.deleteSuppression(email));
  }
}

class AccountResource {
  readonly suppressions: SuppressionsResource;
  constructor(private readonly api: PromiseAccountApi) {
    this.suppressions = new SuppressionsResource(api);
  }
  get(): Promise<AccountView> {
    return call(() => this.api.getAccount());
  }
  export(): Promise<UserExport> {
    return call(() => this.api.exportAccount());
  }
  delete(): Promise<DeleteUserDataResult> {
    // Irreversible. The typed .delete() call is the confirmation; the SDK
    // supplies the ?confirm=DELETE guard the raw API requires.
    return call(() => this.api.deleteAccount("DELETE"));
  }
}
