import { ResponseContext, RequestContext, HttpFile, HttpInfo } from '../http/http.js';
import { Configuration, ConfigurationOptions, mergeConfiguration } from '../configuration.js'
import type { Middleware } from '../middleware.js';
import { Observable, of, from } from '../rxjsStub.js';
import {mergeMap, map} from  '../rxjsStub.js';
import { APIKeyExportEntry } from '../models/APIKeyExportEntry.js';
import { AgentIdentity } from '../models/AgentIdentity.js';
import { AgentView } from '../models/AgentView.js';
import { ApproveRequest } from '../models/ApproveRequest.js';
import { ApproveResultView } from '../models/ApproveResultView.js';
import { Attachment } from '../models/Attachment.js';
import { CheckResult } from '../models/CheckResult.js';
import { ConversationDetailView } from '../models/ConversationDetailView.js';
import { ConversationSummaryView } from '../models/ConversationSummaryView.js';
import { CreateAgentRequest } from '../models/CreateAgentRequest.js';
import { CreateAgentResponse } from '../models/CreateAgentResponse.js';
import { CreateWebhookRequest } from '../models/CreateWebhookRequest.js';
import { DNSRecordView } from '../models/DNSRecordView.js';
import { DNSRecordsView } from '../models/DNSRecordsView.js';
import { DeleteUserDataResult } from '../models/DeleteUserDataResult.js';
import { DeliveryStatusJSON } from '../models/DeliveryStatusJSON.js';
import { DeploymentInfoView } from '../models/DeploymentInfoView.js';
import { Domain } from '../models/Domain.js';
import { DomainView } from '../models/DomainView.js';
import { ErrorBody } from '../models/ErrorBody.js';
import { ErrorEnvelope } from '../models/ErrorEnvelope.js';
import { EventJSON } from '../models/EventJSON.js';
import { ForwardRequest } from '../models/ForwardRequest.js';
import { LimitsCapsView } from '../models/LimitsCapsView.js';
import { LimitsUsageView } from '../models/LimitsUsageView.js';
import { LimitsView } from '../models/LimitsView.js';
import { Message } from '../models/Message.js';
import { MessageBodyView } from '../models/MessageBodyView.js';
import { MessageParsedView } from '../models/MessageParsedView.js';
import { MessageSummaryView } from '../models/MessageSummaryView.js';
import { MessageView } from '../models/MessageView.js';
import { OAuthConnectionEntry } from '../models/OAuthConnectionEntry.js';
import { PageAgentView } from '../models/PageAgentView.js';
import { PageConversationSummaryView } from '../models/PageConversationSummaryView.js';
import { PageDomainView } from '../models/PageDomainView.js';
import { PageEventJSON } from '../models/PageEventJSON.js';
import { PageMessageSummaryView } from '../models/PageMessageSummaryView.js';
import { PageSuppression } from '../models/PageSuppression.js';
import { PageWebhookDeliveryView } from '../models/PageWebhookDeliveryView.js';
import { PageWebhookView } from '../models/PageWebhookView.js';
import { RedeliverDelivery } from '../models/RedeliverDelivery.js';
import { RedeliverEventInputBody } from '../models/RedeliverEventInputBody.js';
import { RedeliverView } from '../models/RedeliverView.js';
import { RegisterDomainRequest } from '../models/RegisterDomainRequest.js';
import { RejectInputBody } from '../models/RejectInputBody.js';
import { RejectResultView } from '../models/RejectResultView.js';
import { ReplyRequest } from '../models/ReplyRequest.js';
import { Result } from '../models/Result.js';
import { RotateSecretBody } from '../models/RotateSecretBody.js';
import { SendEmailRequest } from '../models/SendEmailRequest.js';
import { SendResultView } from '../models/SendResultView.js';
import { SendingDNSRecordView } from '../models/SendingDNSRecordView.js';
import { Suppression } from '../models/Suppression.js';
import { TestWebhookOutputBody } from '../models/TestWebhookOutputBody.js';
import { TestWebhookRequest } from '../models/TestWebhookRequest.js';
import { UpdateAgentRequest } from '../models/UpdateAgentRequest.js';
import { UpdateDomainRequest } from '../models/UpdateDomainRequest.js';
import { UpdateMessageRequest } from '../models/UpdateMessageRequest.js';
import { UpdateMessageResultView } from '../models/UpdateMessageResultView.js';
import { UpdateWebhookRequest } from '../models/UpdateWebhookRequest.js';
import { UsageEventEntry } from '../models/UsageEventEntry.js';
import { UserExport } from '../models/UserExport.js';
import { UserExportUser } from '../models/UserExportUser.js';
import { VerifyDomainView } from '../models/VerifyDomainView.js';
import { WebhookDeliveryView } from '../models/WebhookDeliveryView.js';
import { WebhookFiltersView } from '../models/WebhookFiltersView.js';
import { WebhookView } from '../models/WebhookView.js';

import { AccountApiRequestFactory, AccountApiResponseProcessor} from "../apis/AccountApi.js";
export class ObservableAccountApi {
    private requestFactory: AccountApiRequestFactory;
    private responseProcessor: AccountApiResponseProcessor;
    private configuration: Configuration;

    public constructor(
        configuration: Configuration,
        requestFactory?: AccountApiRequestFactory,
        responseProcessor?: AccountApiResponseProcessor
    ) {
        this.configuration = configuration;
        this.requestFactory = requestFactory || new AccountApiRequestFactory(configuration);
        this.responseProcessor = responseProcessor || new AccountApiResponseProcessor();
    }

    /**
     * Permanently deletes the account and cascades all owned data. Requires ?confirm=DELETE.
     * Delete your account + all data (irreversible)
     * @param [confirm] Must be DELETE — this is irreversible.
     */
    public deleteAccountWithHttpInfo(confirm?: string, _options?: ConfigurationOptions): Observable<HttpInfo<DeleteUserDataResult>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.deleteAccount(confirm, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.deleteAccountWithHttpInfo(rsp)));
            }));
    }

    /**
     * Permanently deletes the account and cascades all owned data. Requires ?confirm=DELETE.
     * Delete your account + all data (irreversible)
     * @param [confirm] Must be DELETE — this is irreversible.
     */
    public deleteAccount(confirm?: string, _options?: ConfigurationOptions): Observable<DeleteUserDataResult> {
        return this.deleteAccountWithHttpInfo(confirm, _options).pipe(map((apiResponse: HttpInfo<DeleteUserDataResult>) => apiResponse.data));
    }

    /**
     * Un-suppress a recipient. A previously-blocked send to it then succeeds (idempotency keys are released, so no fresh key is needed).
     * Remove an address from the suppression list
     * @param address
     */
    public deleteSuppressionWithHttpInfo(address: string, _options?: ConfigurationOptions): Observable<HttpInfo<void>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.deleteSuppression(address, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.deleteSuppressionWithHttpInfo(rsp)));
            }));
    }

    /**
     * Un-suppress a recipient. A previously-blocked send to it then succeeds (idempotency keys are released, so no fresh key is needed).
     * Remove an address from the suppression list
     * @param address
     */
    public deleteSuppression(address: string, _options?: ConfigurationOptions): Observable<void> {
        return this.deleteSuppressionWithHttpInfo(address, _options).pipe(map((apiResponse: HttpInfo<void>) => apiResponse.data));
    }

    /**
     * A JSON dump of every record the authenticated account owns.
     * Export your data (GDPR right-of-access)
     */
    public exportAccountWithHttpInfo(_options?: ConfigurationOptions): Observable<HttpInfo<UserExport>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.exportAccount(_config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.exportAccountWithHttpInfo(rsp)));
            }));
    }

    /**
     * A JSON dump of every record the authenticated account owns.
     * Export your data (GDPR right-of-access)
     */
    public exportAccount(_options?: ConfigurationOptions): Observable<UserExport> {
        return this.exportAccountWithHttpInfo(_options).pipe(map((apiResponse: HttpInfo<UserExport>) => apiResponse.data));
    }

    /**
     * The authenticated account\'s plan caps and current usage. (Deployment discovery — shared domain, slug registration — is the separate public GET /v1/info.)
     * Get account: plan limits + usage
     */
    public getAccountWithHttpInfo(_options?: ConfigurationOptions): Observable<HttpInfo<LimitsView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.getAccount(_config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.getAccountWithHttpInfo(rsp)));
            }));
    }

    /**
     * The authenticated account\'s plan caps and current usage. (Deployment discovery — shared domain, slug registration — is the separate public GET /v1/info.)
     * Get account: plan limits + usage
     */
    public getAccount(_options?: ConfigurationOptions): Observable<LimitsView> {
        return this.getAccountWithHttpInfo(_options).pipe(map((apiResponse: HttpInfo<LimitsView>) => apiResponse.data));
    }

    /**
     * Addresses e2a will refuse to send to (auto-added on a hard bounce or complaint, or added manually). Sends to a suppressed address fail with recipient_suppressed.
     * List suppressed recipient addresses
     */
    public listSuppressionsWithHttpInfo(_options?: ConfigurationOptions): Observable<HttpInfo<PageSuppression>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.listSuppressions(_config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.listSuppressionsWithHttpInfo(rsp)));
            }));
    }

    /**
     * Addresses e2a will refuse to send to (auto-added on a hard bounce or complaint, or added manually). Sends to a suppressed address fail with recipient_suppressed.
     * List suppressed recipient addresses
     */
    public listSuppressions(_options?: ConfigurationOptions): Observable<PageSuppression> {
        return this.listSuppressionsWithHttpInfo(_options).pipe(map((apiResponse: HttpInfo<PageSuppression>) => apiResponse.data));
    }

}

import { AgentsApiRequestFactory, AgentsApiResponseProcessor} from "../apis/AgentsApi.js";
export class ObservableAgentsApi {
    private requestFactory: AgentsApiRequestFactory;
    private responseProcessor: AgentsApiResponseProcessor;
    private configuration: Configuration;

    public constructor(
        configuration: Configuration,
        requestFactory?: AgentsApiRequestFactory,
        responseProcessor?: AgentsApiResponseProcessor
    ) {
        this.configuration = configuration;
        this.requestFactory = requestFactory || new AgentsApiRequestFactory(configuration);
        this.responseProcessor = responseProcessor || new AgentsApiResponseProcessor();
    }

    /**
     * Register an agent on a verified domain the caller owns (or, when slug registration is enabled, on the shared domain).
     * Create an agent
     * @param createAgentRequest
     */
    public createAgentWithHttpInfo(createAgentRequest: CreateAgentRequest, _options?: ConfigurationOptions): Observable<HttpInfo<CreateAgentResponse>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.createAgent(createAgentRequest, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.createAgentWithHttpInfo(rsp)));
            }));
    }

    /**
     * Register an agent on a verified domain the caller owns (or, when slug registration is enabled, on the shared domain).
     * Create an agent
     * @param createAgentRequest
     */
    public createAgent(createAgentRequest: CreateAgentRequest, _options?: ConfigurationOptions): Observable<CreateAgentResponse> {
        return this.createAgentWithHttpInfo(createAgentRequest, _options).pipe(map((apiResponse: HttpInfo<CreateAgentResponse>) => apiResponse.data));
    }

    /**
     * Delete an agent the caller owns.
     * Delete an agent
     * @param address The agent\&#39;s full email address, e.g. support@acme.com.
     */
    public deleteAgentWithHttpInfo(address: string, _options?: ConfigurationOptions): Observable<HttpInfo<void>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.deleteAgent(address, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.deleteAgentWithHttpInfo(rsp)));
            }));
    }

    /**
     * Delete an agent the caller owns.
     * Delete an agent
     * @param address The agent\&#39;s full email address, e.g. support@acme.com.
     */
    public deleteAgent(address: string, _options?: ConfigurationOptions): Observable<void> {
        return this.deleteAgentWithHttpInfo(address, _options).pipe(map((apiResponse: HttpInfo<void>) => apiResponse.data));
    }

    /**
     * Fetch a single agent the authenticated account owns, by full email address.
     * Get an agent
     * @param address The agent\&#39;s full email address, e.g. support@acme.com.
     */
    public getAgentWithHttpInfo(address: string, _options?: ConfigurationOptions): Observable<HttpInfo<AgentView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.getAgent(address, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.getAgentWithHttpInfo(rsp)));
            }));
    }

    /**
     * Fetch a single agent the authenticated account owns, by full email address.
     * Get an agent
     * @param address The agent\&#39;s full email address, e.g. support@acme.com.
     */
    public getAgent(address: string, _options?: ConfigurationOptions): Observable<AgentView> {
        return this.getAgentWithHttpInfo(address, _options).pipe(map((apiResponse: HttpInfo<AgentView>) => apiResponse.data));
    }

    /**
     * List the agents owned by the authenticated account.
     * List agents
     */
    public listAgentsWithHttpInfo(_options?: ConfigurationOptions): Observable<HttpInfo<PageAgentView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.listAgents(_config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.listAgentsWithHttpInfo(rsp)));
            }));
    }

    /**
     * List the agents owned by the authenticated account.
     * List agents
     */
    public listAgents(_options?: ConfigurationOptions): Observable<PageAgentView> {
        return this.listAgentsWithHttpInfo(_options).pipe(map((apiResponse: HttpInfo<PageAgentView>) => apiResponse.data));
    }

    /**
     * Send a platform test email to the agent\'s own address to confirm inbound delivery. 202 when held for HITL.
     * Send a test email to the agent\'s own address
     * @param address The agent\&#39;s full email address, e.g. support@acme.com.
     */
    public testAgentWithHttpInfo(address: string, _options?: ConfigurationOptions): Observable<HttpInfo<SendResultView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.testAgent(address, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.testAgentWithHttpInfo(rsp)));
            }));
    }

    /**
     * Send a platform test email to the agent\'s own address to confirm inbound delivery. 202 when held for HITL.
     * Send a test email to the agent\'s own address
     * @param address The agent\&#39;s full email address, e.g. support@acme.com.
     */
    public testAgent(address: string, _options?: ConfigurationOptions): Observable<SendResultView> {
        return this.testAgentWithHttpInfo(address, _options).pipe(map((apiResponse: HttpInfo<SendResultView>) => apiResponse.data));
    }

    /**
     * Patch an agent\'s HITL settings. Returns the post-update agent.
     * Update an agent
     * @param address
     * @param updateAgentRequest
     */
    public updateAgentWithHttpInfo(address: string, updateAgentRequest: UpdateAgentRequest, _options?: ConfigurationOptions): Observable<HttpInfo<AgentView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.updateAgent(address, updateAgentRequest, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.updateAgentWithHttpInfo(rsp)));
            }));
    }

    /**
     * Patch an agent\'s HITL settings. Returns the post-update agent.
     * Update an agent
     * @param address
     * @param updateAgentRequest
     */
    public updateAgent(address: string, updateAgentRequest: UpdateAgentRequest, _options?: ConfigurationOptions): Observable<AgentView> {
        return this.updateAgentWithHttpInfo(address, updateAgentRequest, _options).pipe(map((apiResponse: HttpInfo<AgentView>) => apiResponse.data));
    }

}

import { ConversationsApiRequestFactory, ConversationsApiResponseProcessor} from "../apis/ConversationsApi.js";
export class ObservableConversationsApi {
    private requestFactory: ConversationsApiRequestFactory;
    private responseProcessor: ConversationsApiResponseProcessor;
    private configuration: Configuration;

    public constructor(
        configuration: Configuration,
        requestFactory?: ConversationsApiRequestFactory,
        responseProcessor?: ConversationsApiResponseProcessor
    ) {
        this.configuration = configuration;
        this.requestFactory = requestFactory || new ConversationsApiRequestFactory(configuration);
        this.responseProcessor = responseProcessor || new ConversationsApiResponseProcessor();
    }

    /**
     * Fetch a single conversation thread with its participants, labels, and member messages.
     * Get a conversation
     * @param address
     * @param id
     */
    public getConversationWithHttpInfo(address: string, id: string, _options?: ConfigurationOptions): Observable<HttpInfo<ConversationDetailView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.getConversation(address, id, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.getConversationWithHttpInfo(rsp)));
            }));
    }

    /**
     * Fetch a single conversation thread with its participants, labels, and member messages.
     * Get a conversation
     * @param address
     * @param id
     */
    public getConversation(address: string, id: string, _options?: ConfigurationOptions): Observable<ConversationDetailView> {
        return this.getConversationWithHttpInfo(address, id, _options).pipe(map((apiResponse: HttpInfo<ConversationDetailView>) => apiResponse.data));
    }

    /**
     * List an agent\'s conversation threads (derived from messages.conversation_id).
     * List conversations
     * @param address
     * @param [since] RFC3339.
     * @param [until] RFC3339.
     * @param [limit]
     */
    public listConversationsWithHttpInfo(address: string, since?: string, until?: string, limit?: number, _options?: ConfigurationOptions): Observable<HttpInfo<PageConversationSummaryView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.listConversations(address, since, until, limit, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.listConversationsWithHttpInfo(rsp)));
            }));
    }

    /**
     * List an agent\'s conversation threads (derived from messages.conversation_id).
     * List conversations
     * @param address
     * @param [since] RFC3339.
     * @param [until] RFC3339.
     * @param [limit]
     */
    public listConversations(address: string, since?: string, until?: string, limit?: number, _options?: ConfigurationOptions): Observable<PageConversationSummaryView> {
        return this.listConversationsWithHttpInfo(address, since, until, limit, _options).pipe(map((apiResponse: HttpInfo<PageConversationSummaryView>) => apiResponse.data));
    }

}

import { DomainsApiRequestFactory, DomainsApiResponseProcessor} from "../apis/DomainsApi.js";
export class ObservableDomainsApi {
    private requestFactory: DomainsApiRequestFactory;
    private responseProcessor: DomainsApiResponseProcessor;
    private configuration: Configuration;

    public constructor(
        configuration: Configuration,
        requestFactory?: DomainsApiRequestFactory,
        responseProcessor?: DomainsApiResponseProcessor
    ) {
        this.configuration = configuration;
        this.requestFactory = requestFactory || new DomainsApiRequestFactory(configuration);
        this.responseProcessor = responseProcessor || new DomainsApiResponseProcessor();
    }

    /**
     * Delete a domain
     * @param domain
     */
    public deleteDomainWithHttpInfo(domain: string, _options?: ConfigurationOptions): Observable<HttpInfo<void>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.deleteDomain(domain, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.deleteDomainWithHttpInfo(rsp)));
            }));
    }

    /**
     * Delete a domain
     * @param domain
     */
    public deleteDomain(domain: string, _options?: ConfigurationOptions): Observable<void> {
        return this.deleteDomainWithHttpInfo(domain, _options).pipe(map((apiResponse: HttpInfo<void>) => apiResponse.data));
    }

    /**
     * Get a domain
     * @param domain
     */
    public getDomainWithHttpInfo(domain: string, _options?: ConfigurationOptions): Observable<HttpInfo<DomainView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.getDomain(domain, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.getDomainWithHttpInfo(rsp)));
            }));
    }

    /**
     * Get a domain
     * @param domain
     */
    public getDomain(domain: string, _options?: ConfigurationOptions): Observable<DomainView> {
        return this.getDomainWithHttpInfo(domain, _options).pipe(map((apiResponse: HttpInfo<DomainView>) => apiResponse.data));
    }

    /**
     * List domains
     */
    public listDomainsWithHttpInfo(_options?: ConfigurationOptions): Observable<HttpInfo<PageDomainView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.listDomains(_config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.listDomainsWithHttpInfo(rsp)));
            }));
    }

    /**
     * List domains
     */
    public listDomains(_options?: ConfigurationOptions): Observable<PageDomainView> {
        return this.listDomainsWithHttpInfo(_options).pipe(map((apiResponse: HttpInfo<PageDomainView>) => apiResponse.data));
    }

    /**
     * Register a domain
     * @param registerDomainRequest
     */
    public registerDomainWithHttpInfo(registerDomainRequest: RegisterDomainRequest, _options?: ConfigurationOptions): Observable<HttpInfo<DomainView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.registerDomain(registerDomainRequest, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.registerDomainWithHttpInfo(rsp)));
            }));
    }

    /**
     * Register a domain
     * @param registerDomainRequest
     */
    public registerDomain(registerDomainRequest: RegisterDomainRequest, _options?: ConfigurationOptions): Observable<DomainView> {
        return this.registerDomainWithHttpInfo(registerDomainRequest, _options).pipe(map((apiResponse: HttpInfo<DomainView>) => apiResponse.data));
    }

    /**
     * Update a domain (set primary)
     * @param domain
     * @param updateDomainRequest
     */
    public updateDomainWithHttpInfo(domain: string, updateDomainRequest: UpdateDomainRequest, _options?: ConfigurationOptions): Observable<HttpInfo<DomainView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.updateDomain(domain, updateDomainRequest, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.updateDomainWithHttpInfo(rsp)));
            }));
    }

    /**
     * Update a domain (set primary)
     * @param domain
     * @param updateDomainRequest
     */
    public updateDomain(domain: string, updateDomainRequest: UpdateDomainRequest, _options?: ConfigurationOptions): Observable<DomainView> {
        return this.updateDomainWithHttpInfo(domain, updateDomainRequest, _options).pipe(map((apiResponse: HttpInfo<DomainView>) => apiResponse.data));
    }

    /**
     * Probe the domain\'s published DNS and, when the verification TXT is present, mark it verified. Returns the per-record diagnostic; a missing TXT yields 412.
     * Verify a domain
     * @param domain
     */
    public verifyDomainWithHttpInfo(domain: string, _options?: ConfigurationOptions): Observable<HttpInfo<VerifyDomainView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.verifyDomain(domain, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.verifyDomainWithHttpInfo(rsp)));
            }));
    }

    /**
     * Probe the domain\'s published DNS and, when the verification TXT is present, mark it verified. Returns the per-record diagnostic; a missing TXT yields 412.
     * Verify a domain
     * @param domain
     */
    public verifyDomain(domain: string, _options?: ConfigurationOptions): Observable<VerifyDomainView> {
        return this.verifyDomainWithHttpInfo(domain, _options).pipe(map((apiResponse: HttpInfo<VerifyDomainView>) => apiResponse.data));
    }

}

import { EventsApiRequestFactory, EventsApiResponseProcessor} from "../apis/EventsApi.js";
export class ObservableEventsApi {
    private requestFactory: EventsApiRequestFactory;
    private responseProcessor: EventsApiResponseProcessor;
    private configuration: Configuration;

    public constructor(
        configuration: Configuration,
        requestFactory?: EventsApiRequestFactory,
        responseProcessor?: EventsApiResponseProcessor
    ) {
        this.configuration = configuration;
        this.requestFactory = requestFactory || new EventsApiRequestFactory(configuration);
        this.responseProcessor = responseProcessor || new EventsApiResponseProcessor();
    }

    /**
     * Get an event
     * @param id
     */
    public getEventWithHttpInfo(id: string, _options?: ConfigurationOptions): Observable<HttpInfo<EventJSON>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.getEvent(id, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.getEventWithHttpInfo(rsp)));
            }));
    }

    /**
     * Get an event
     * @param id
     */
    public getEvent(id: string, _options?: ConfigurationOptions): Observable<EventJSON> {
        return this.getEventWithHttpInfo(id, _options).pipe(map((apiResponse: HttpInfo<EventJSON>) => apiResponse.data));
    }

    /**
     * The webhook-event delivery log, filterable by type/agent/conversation/message and time range, with cursor pagination.
     * List events
     * @param [type]
     * @param [agentId]
     * @param [conversationId]
     * @param [messageId]
     * @param [since] RFC3339.
     * @param [until] RFC3339.
     * @param [cursor]
     * @param [limit]
     */
    public listEventsWithHttpInfo(type?: string, agentId?: string, conversationId?: string, messageId?: string, since?: string, until?: string, cursor?: string, limit?: number, _options?: ConfigurationOptions): Observable<HttpInfo<PageEventJSON>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.listEvents(type, agentId, conversationId, messageId, since, until, cursor, limit, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.listEventsWithHttpInfo(rsp)));
            }));
    }

    /**
     * The webhook-event delivery log, filterable by type/agent/conversation/message and time range, with cursor pagination.
     * List events
     * @param [type]
     * @param [agentId]
     * @param [conversationId]
     * @param [messageId]
     * @param [since] RFC3339.
     * @param [until] RFC3339.
     * @param [cursor]
     * @param [limit]
     */
    public listEvents(type?: string, agentId?: string, conversationId?: string, messageId?: string, since?: string, until?: string, cursor?: string, limit?: number, _options?: ConfigurationOptions): Observable<PageEventJSON> {
        return this.listEventsWithHttpInfo(type, agentId, conversationId, messageId, since, until, cursor, limit, _options).pipe(map((apiResponse: HttpInfo<PageEventJSON>) => apiResponse.data));
    }

    /**
     * Re-enqueue webhook delivery for an event. With a webhook_id, replays to that subscriber; without, fans out to every originally-matched subscriber. Auto-deduplicated within a short window — receivers must dedup on event id.
     * Redeliver an event
     * @param id
     * @param redeliverEventInputBody
     */
    public redeliverEventWithHttpInfo(id: string, redeliverEventInputBody: RedeliverEventInputBody, _options?: ConfigurationOptions): Observable<HttpInfo<RedeliverView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.redeliverEvent(id, redeliverEventInputBody, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.redeliverEventWithHttpInfo(rsp)));
            }));
    }

    /**
     * Re-enqueue webhook delivery for an event. With a webhook_id, replays to that subscriber; without, fans out to every originally-matched subscriber. Auto-deduplicated within a short window — receivers must dedup on event id.
     * Redeliver an event
     * @param id
     * @param redeliverEventInputBody
     */
    public redeliverEvent(id: string, redeliverEventInputBody: RedeliverEventInputBody, _options?: ConfigurationOptions): Observable<RedeliverView> {
        return this.redeliverEventWithHttpInfo(id, redeliverEventInputBody, _options).pipe(map((apiResponse: HttpInfo<RedeliverView>) => apiResponse.data));
    }

}

import { MessagesApiRequestFactory, MessagesApiResponseProcessor} from "../apis/MessagesApi.js";
export class ObservableMessagesApi {
    private requestFactory: MessagesApiRequestFactory;
    private responseProcessor: MessagesApiResponseProcessor;
    private configuration: Configuration;

    public constructor(
        configuration: Configuration,
        requestFactory?: MessagesApiRequestFactory,
        responseProcessor?: MessagesApiResponseProcessor
    ) {
        this.configuration = configuration;
        this.requestFactory = requestFactory || new MessagesApiRequestFactory(configuration);
        this.responseProcessor = responseProcessor || new MessagesApiResponseProcessor();
    }

    /**
     * Approve a pending_approval draft (with optional reviewer overrides) and send it. Honors Idempotency-Key (the approve triggers an SES send).
     * Approve a held message
     * @param address
     * @param id
     * @param approveRequest
     * @param [idempotencyKey]
     */
    public approveMessageWithHttpInfo(address: string, id: string, approveRequest: ApproveRequest, idempotencyKey?: string, _options?: ConfigurationOptions): Observable<HttpInfo<ApproveResultView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.approveMessage(address, id, approveRequest, idempotencyKey, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.approveMessageWithHttpInfo(rsp)));
            }));
    }

    /**
     * Approve a pending_approval draft (with optional reviewer overrides) and send it. Honors Idempotency-Key (the approve triggers an SES send).
     * Approve a held message
     * @param address
     * @param id
     * @param approveRequest
     * @param [idempotencyKey]
     */
    public approveMessage(address: string, id: string, approveRequest: ApproveRequest, idempotencyKey?: string, _options?: ConfigurationOptions): Observable<ApproveResultView> {
        return this.approveMessageWithHttpInfo(address, id, approveRequest, idempotencyKey, _options).pipe(map((apiResponse: HttpInfo<ApproveResultView>) => apiResponse.data));
    }

    /**
     * Forward an inbound message to new recipients; the original is quoted. 202 when held for HITL.
     * Forward a message
     * @param address
     * @param id
     * @param forwardRequest
     * @param [idempotencyKey]
     */
    public forwardMessageWithHttpInfo(address: string, id: string, forwardRequest: ForwardRequest, idempotencyKey?: string, _options?: ConfigurationOptions): Observable<HttpInfo<SendResultView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.forwardMessage(address, id, forwardRequest, idempotencyKey, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.forwardMessageWithHttpInfo(rsp)));
            }));
    }

    /**
     * Forward an inbound message to new recipients; the original is quoted. 202 when held for HITL.
     * Forward a message
     * @param address
     * @param id
     * @param forwardRequest
     * @param [idempotencyKey]
     */
    public forwardMessage(address: string, id: string, forwardRequest: ForwardRequest, idempotencyKey?: string, _options?: ConfigurationOptions): Observable<SendResultView> {
        return this.forwardMessageWithHttpInfo(address, id, forwardRequest, idempotencyKey, _options).pipe(map((apiResponse: HttpInfo<SendResultView>) => apiResponse.data));
    }

    /**
     * Fetch a single message (inbound or outbound) by id, scoped to an agent the caller owns. Includes the raw message and inbound auth headers.
     * Get a message
     * @param address The agent\&#39;s full email address.
     * @param id The message id, e.g. msg_abc123.
     */
    public getMessageWithHttpInfo(address: string, id: string, _options?: ConfigurationOptions): Observable<HttpInfo<MessageView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.getMessage(address, id, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.getMessageWithHttpInfo(rsp)));
            }));
    }

    /**
     * Fetch a single message (inbound or outbound) by id, scoped to an agent the caller owns. Includes the raw message and inbound auth headers.
     * Get a message
     * @param address The agent\&#39;s full email address.
     * @param id The message id, e.g. msg_abc123.
     */
    public getMessage(address: string, id: string, _options?: ConfigurationOptions): Observable<MessageView> {
        return this.getMessageWithHttpInfo(address, id, _options).pipe(map((apiResponse: HttpInfo<MessageView>) => apiResponse.data));
    }

    /**
     * List an agent\'s messages (inbound + outbound) with filters and cursor pagination. Held outbound drafts appear as status=pending_approval.
     * List messages
     * @param address
     * @param [direction] Defaults to inbound.
     * @param [status] Inbound only. Defaults to unread for inbound, all otherwise.
     * @param [sort] Defaults to desc (newest first).
     * @param [_from] Case-insensitive substring match on sender.
     * @param [subjectContains] Case-insensitive substring match on subject.
     * @param [conversationId]
     * @param [labels] Repeatable; AND-matched.
     * @param [since] RFC3339; created_at &gt;&#x3D; since.
     * @param [until] RFC3339; created_at &lt; until.
     * @param [cursor]
     * @param [limit]
     */
    public listMessagesWithHttpInfo(address: string, direction?: 'inbound' | 'outbound' | 'all', status?: 'unread' | 'read' | 'all', sort?: 'asc' | 'desc', _from?: string, subjectContains?: string, conversationId?: string, labels?: Array<string>, since?: string, until?: string, cursor?: string, limit?: number, _options?: ConfigurationOptions): Observable<HttpInfo<PageMessageSummaryView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.listMessages(address, direction, status, sort, _from, subjectContains, conversationId, labels, since, until, cursor, limit, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.listMessagesWithHttpInfo(rsp)));
            }));
    }

    /**
     * List an agent\'s messages (inbound + outbound) with filters and cursor pagination. Held outbound drafts appear as status=pending_approval.
     * List messages
     * @param address
     * @param [direction] Defaults to inbound.
     * @param [status] Inbound only. Defaults to unread for inbound, all otherwise.
     * @param [sort] Defaults to desc (newest first).
     * @param [_from] Case-insensitive substring match on sender.
     * @param [subjectContains] Case-insensitive substring match on subject.
     * @param [conversationId]
     * @param [labels] Repeatable; AND-matched.
     * @param [since] RFC3339; created_at &gt;&#x3D; since.
     * @param [until] RFC3339; created_at &lt; until.
     * @param [cursor]
     * @param [limit]
     */
    public listMessages(address: string, direction?: 'inbound' | 'outbound' | 'all', status?: 'unread' | 'read' | 'all', sort?: 'asc' | 'desc', _from?: string, subjectContains?: string, conversationId?: string, labels?: Array<string>, since?: string, until?: string, cursor?: string, limit?: number, _options?: ConfigurationOptions): Observable<PageMessageSummaryView> {
        return this.listMessagesWithHttpInfo(address, direction, status, sort, _from, subjectContains, conversationId, labels, since, until, cursor, limit, _options).pipe(map((apiResponse: HttpInfo<PageMessageSummaryView>) => apiResponse.data));
    }

    /**
     * Reject a pending_approval draft so it is never sent.
     * Reject a held message
     * @param address
     * @param id
     * @param rejectInputBody
     */
    public rejectMessageWithHttpInfo(address: string, id: string, rejectInputBody: RejectInputBody, _options?: ConfigurationOptions): Observable<HttpInfo<RejectResultView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.rejectMessage(address, id, rejectInputBody, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.rejectMessageWithHttpInfo(rsp)));
            }));
    }

    /**
     * Reject a pending_approval draft so it is never sent.
     * Reject a held message
     * @param address
     * @param id
     * @param rejectInputBody
     */
    public rejectMessage(address: string, id: string, rejectInputBody: RejectInputBody, _options?: ConfigurationOptions): Observable<RejectResultView> {
        return this.rejectMessageWithHttpInfo(address, id, rejectInputBody, _options).pipe(map((apiResponse: HttpInfo<RejectResultView>) => apiResponse.data));
    }

    /**
     * Reply to an inbound message; recipients/threading are derived from the original. 202 when held for HITL.
     * Reply to a message
     * @param address
     * @param id
     * @param replyRequest
     * @param [idempotencyKey]
     */
    public replyToMessageWithHttpInfo(address: string, id: string, replyRequest: ReplyRequest, idempotencyKey?: string, _options?: ConfigurationOptions): Observable<HttpInfo<SendResultView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.replyToMessage(address, id, replyRequest, idempotencyKey, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.replyToMessageWithHttpInfo(rsp)));
            }));
    }

    /**
     * Reply to an inbound message; recipients/threading are derived from the original. 202 when held for HITL.
     * Reply to a message
     * @param address
     * @param id
     * @param replyRequest
     * @param [idempotencyKey]
     */
    public replyToMessage(address: string, id: string, replyRequest: ReplyRequest, idempotencyKey?: string, _options?: ConfigurationOptions): Observable<SendResultView> {
        return this.replyToMessageWithHttpInfo(address, id, replyRequest, idempotencyKey, _options).pipe(map((apiResponse: HttpInfo<SendResultView>) => apiResponse.data));
    }

    /**
     * Send a new email from the agent named in the path (a new thread). The sender is the path agent — `reply`/`forward` are their own sub-resources. 202 + pending_approval when the agent has HITL enabled. Honors Idempotency-Key.
     * Send a new email
     * @param address
     * @param sendEmailRequest
     * @param [idempotencyKey]
     */
    public sendMessageWithHttpInfo(address: string, sendEmailRequest: SendEmailRequest, idempotencyKey?: string, _options?: ConfigurationOptions): Observable<HttpInfo<SendResultView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.sendMessage(address, sendEmailRequest, idempotencyKey, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.sendMessageWithHttpInfo(rsp)));
            }));
    }

    /**
     * Send a new email from the agent named in the path (a new thread). The sender is the path agent — `reply`/`forward` are their own sub-resources. 202 + pending_approval when the agent has HITL enabled. Honors Idempotency-Key.
     * Send a new email
     * @param address
     * @param sendEmailRequest
     * @param [idempotencyKey]
     */
    public sendMessage(address: string, sendEmailRequest: SendEmailRequest, idempotencyKey?: string, _options?: ConfigurationOptions): Observable<SendResultView> {
        return this.sendMessageWithHttpInfo(address, sendEmailRequest, idempotencyKey, _options).pipe(map((apiResponse: HttpInfo<SendResultView>) => apiResponse.data));
    }

    /**
     * Apply a labels delta (`add_labels` / `remove_labels`) to a message the caller owns; returns the post-update label set. Each list is capped at 50 entries; labels are lowercase `[a-z0-9:_-]+` up to 64 chars; the `e2a:` prefix is reserved for system labels. A message carries at most 100 labels. An empty delta is a read of the current labels.
     * Update a message (labels)
     * @param address
     * @param id
     * @param updateMessageRequest
     */
    public updateMessageWithHttpInfo(address: string, id: string, updateMessageRequest: UpdateMessageRequest, _options?: ConfigurationOptions): Observable<HttpInfo<UpdateMessageResultView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.updateMessage(address, id, updateMessageRequest, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.updateMessageWithHttpInfo(rsp)));
            }));
    }

    /**
     * Apply a labels delta (`add_labels` / `remove_labels`) to a message the caller owns; returns the post-update label set. Each list is capped at 50 entries; labels are lowercase `[a-z0-9:_-]+` up to 64 chars; the `e2a:` prefix is reserved for system labels. A message carries at most 100 labels. An empty delta is a read of the current labels.
     * Update a message (labels)
     * @param address
     * @param id
     * @param updateMessageRequest
     */
    public updateMessage(address: string, id: string, updateMessageRequest: UpdateMessageRequest, _options?: ConfigurationOptions): Observable<UpdateMessageResultView> {
        return this.updateMessageWithHttpInfo(address, id, updateMessageRequest, _options).pipe(map((apiResponse: HttpInfo<UpdateMessageResultView>) => apiResponse.data));
    }

}

import { MetaApiRequestFactory, MetaApiResponseProcessor} from "../apis/MetaApi.js";
export class ObservableMetaApi {
    private requestFactory: MetaApiRequestFactory;
    private responseProcessor: MetaApiResponseProcessor;
    private configuration: Configuration;

    public constructor(
        configuration: Configuration,
        requestFactory?: MetaApiRequestFactory,
        responseProcessor?: MetaApiResponseProcessor
    ) {
        this.configuration = configuration;
        this.requestFactory = requestFactory || new MetaApiRequestFactory(configuration);
        this.responseProcessor = responseProcessor || new MetaApiResponseProcessor();
    }

    /**
     * Public deployment metadata: the shared agent domain (if slug registration is enabled) and the public base URL. Unauthenticated.
     * Deployment info
     */
    public getInfoWithHttpInfo(_options?: ConfigurationOptions): Observable<HttpInfo<DeploymentInfoView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.getInfo(_config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.getInfoWithHttpInfo(rsp)));
            }));
    }

    /**
     * Public deployment metadata: the shared agent domain (if slug registration is enabled) and the public base URL. Unauthenticated.
     * Deployment info
     */
    public getInfo(_options?: ConfigurationOptions): Observable<DeploymentInfoView> {
        return this.getInfoWithHttpInfo(_options).pipe(map((apiResponse: HttpInfo<DeploymentInfoView>) => apiResponse.data));
    }

}

import { WebhooksApiRequestFactory, WebhooksApiResponseProcessor} from "../apis/WebhooksApi.js";
export class ObservableWebhooksApi {
    private requestFactory: WebhooksApiRequestFactory;
    private responseProcessor: WebhooksApiResponseProcessor;
    private configuration: Configuration;

    public constructor(
        configuration: Configuration,
        requestFactory?: WebhooksApiRequestFactory,
        responseProcessor?: WebhooksApiResponseProcessor
    ) {
        this.configuration = configuration;
        this.requestFactory = requestFactory || new WebhooksApiRequestFactory(configuration);
        this.responseProcessor = responseProcessor || new WebhooksApiResponseProcessor();
    }

    /**
     * Create a webhook
     * @param createWebhookRequest
     */
    public createWebhookWithHttpInfo(createWebhookRequest: CreateWebhookRequest, _options?: ConfigurationOptions): Observable<HttpInfo<WebhookView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.createWebhook(createWebhookRequest, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.createWebhookWithHttpInfo(rsp)));
            }));
    }

    /**
     * Create a webhook
     * @param createWebhookRequest
     */
    public createWebhook(createWebhookRequest: CreateWebhookRequest, _options?: ConfigurationOptions): Observable<WebhookView> {
        return this.createWebhookWithHttpInfo(createWebhookRequest, _options).pipe(map((apiResponse: HttpInfo<WebhookView>) => apiResponse.data));
    }

    /**
     * Delete a webhook
     * @param id
     */
    public deleteWebhookWithHttpInfo(id: string, _options?: ConfigurationOptions): Observable<HttpInfo<void>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.deleteWebhook(id, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.deleteWebhookWithHttpInfo(rsp)));
            }));
    }

    /**
     * Delete a webhook
     * @param id
     */
    public deleteWebhook(id: string, _options?: ConfigurationOptions): Observable<void> {
        return this.deleteWebhookWithHttpInfo(id, _options).pipe(map((apiResponse: HttpInfo<void>) => apiResponse.data));
    }

    /**
     * Get a webhook
     * @param id
     */
    public getWebhookWithHttpInfo(id: string, _options?: ConfigurationOptions): Observable<HttpInfo<WebhookView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.getWebhook(id, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.getWebhookWithHttpInfo(rsp)));
            }));
    }

    /**
     * Get a webhook
     * @param id
     */
    public getWebhook(id: string, _options?: ConfigurationOptions): Observable<WebhookView> {
        return this.getWebhookWithHttpInfo(id, _options).pipe(map((apiResponse: HttpInfo<WebhookView>) => apiResponse.data));
    }

    /**
     * The per-webhook delivery log (read-only debug view).
     * List webhook deliveries
     * @param id
     * @param [status]
     * @param [limit]
     */
    public listWebhookDeliveriesWithHttpInfo(id: string, status?: 'pending' | 'delivered' | 'failed', limit?: number, _options?: ConfigurationOptions): Observable<HttpInfo<PageWebhookDeliveryView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.listWebhookDeliveries(id, status, limit, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.listWebhookDeliveriesWithHttpInfo(rsp)));
            }));
    }

    /**
     * The per-webhook delivery log (read-only debug view).
     * List webhook deliveries
     * @param id
     * @param [status]
     * @param [limit]
     */
    public listWebhookDeliveries(id: string, status?: 'pending' | 'delivered' | 'failed', limit?: number, _options?: ConfigurationOptions): Observable<PageWebhookDeliveryView> {
        return this.listWebhookDeliveriesWithHttpInfo(id, status, limit, _options).pipe(map((apiResponse: HttpInfo<PageWebhookDeliveryView>) => apiResponse.data));
    }

    /**
     * List webhooks
     */
    public listWebhooksWithHttpInfo(_options?: ConfigurationOptions): Observable<HttpInfo<PageWebhookView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.listWebhooks(_config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.listWebhooksWithHttpInfo(rsp)));
            }));
    }

    /**
     * List webhooks
     */
    public listWebhooks(_options?: ConfigurationOptions): Observable<PageWebhookView> {
        return this.listWebhooksWithHttpInfo(_options).pipe(map((apiResponse: HttpInfo<PageWebhookView>) => apiResponse.data));
    }

    /**
     * Mint a new signing secret; the previous one stays valid for a 24h grace window. Returns the new secret (shown once). Honors Idempotency-Key so a retried rotate replays the same secret instead of rotating twice.
     * Rotate a webhook signing secret
     * @param id
     * @param [idempotencyKey]
     */
    public rotateWebhookSecretWithHttpInfo(id: string, idempotencyKey?: string, _options?: ConfigurationOptions): Observable<HttpInfo<RotateSecretBody>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.rotateWebhookSecret(id, idempotencyKey, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.rotateWebhookSecretWithHttpInfo(rsp)));
            }));
    }

    /**
     * Mint a new signing secret; the previous one stays valid for a 24h grace window. Returns the new secret (shown once). Honors Idempotency-Key so a retried rotate replays the same secret instead of rotating twice.
     * Rotate a webhook signing secret
     * @param id
     * @param [idempotencyKey]
     */
    public rotateWebhookSecret(id: string, idempotencyKey?: string, _options?: ConfigurationOptions): Observable<RotateSecretBody> {
        return this.rotateWebhookSecretWithHttpInfo(id, idempotencyKey, _options).pipe(map((apiResponse: HttpInfo<RotateSecretBody>) => apiResponse.data));
    }

    /**
     * Schedule a one-off synthetic delivery to this webhook for development. Returns the delivery id.
     * Fire a synthetic event
     * @param id
     * @param testWebhookRequest
     */
    public testWebhookWithHttpInfo(id: string, testWebhookRequest: TestWebhookRequest, _options?: ConfigurationOptions): Observable<HttpInfo<TestWebhookOutputBody>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.testWebhook(id, testWebhookRequest, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.testWebhookWithHttpInfo(rsp)));
            }));
    }

    /**
     * Schedule a one-off synthetic delivery to this webhook for development. Returns the delivery id.
     * Fire a synthetic event
     * @param id
     * @param testWebhookRequest
     */
    public testWebhook(id: string, testWebhookRequest: TestWebhookRequest, _options?: ConfigurationOptions): Observable<TestWebhookOutputBody> {
        return this.testWebhookWithHttpInfo(id, testWebhookRequest, _options).pipe(map((apiResponse: HttpInfo<TestWebhookOutputBody>) => apiResponse.data));
    }

    /**
     * Partial update. url/events/filters are full-replace when present. Re-enabling within the auto-disable cooldown returns 409.
     * Update a webhook
     * @param id
     * @param updateWebhookRequest
     */
    public updateWebhookWithHttpInfo(id: string, updateWebhookRequest: UpdateWebhookRequest, _options?: ConfigurationOptions): Observable<HttpInfo<WebhookView>> {
        const _config = mergeConfiguration(this.configuration, _options);

        const requestContextPromise = this.requestFactory.updateWebhook(id, updateWebhookRequest, _config);
        // build promise chain
        let middlewarePreObservable = from<RequestContext>(requestContextPromise);
        for (const middleware of _config.middleware) {
            middlewarePreObservable = middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => middleware.pre(ctx)));
        }

        return middlewarePreObservable.pipe(mergeMap((ctx: RequestContext) => _config.httpApi.send(ctx))).
            pipe(mergeMap((response: ResponseContext) => {
                let middlewarePostObservable = of(response);
                for (const middleware of _config.middleware.reverse()) {
                    middlewarePostObservable = middlewarePostObservable.pipe(mergeMap((rsp: ResponseContext) => middleware.post(rsp)));
                }
                return middlewarePostObservable.pipe(map((rsp: ResponseContext) => this.responseProcessor.updateWebhookWithHttpInfo(rsp)));
            }));
    }

    /**
     * Partial update. url/events/filters are full-replace when present. Re-enabling within the auto-disable cooldown returns 409.
     * Update a webhook
     * @param id
     * @param updateWebhookRequest
     */
    public updateWebhook(id: string, updateWebhookRequest: UpdateWebhookRequest, _options?: ConfigurationOptions): Observable<WebhookView> {
        return this.updateWebhookWithHttpInfo(id, updateWebhookRequest, _options).pipe(map((apiResponse: HttpInfo<WebhookView>) => apiResponse.data));
    }

}
