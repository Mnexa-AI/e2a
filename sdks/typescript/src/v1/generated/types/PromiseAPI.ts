import { ResponseContext, RequestContext, HttpFile, HttpInfo } from '../http/http.js';
import { Configuration, PromiseConfigurationOptions, wrapOptions } from '../configuration.js'
import { PromiseMiddleware, Middleware, PromiseMiddlewareWrapper } from '../middleware.js';

import { APIKeyExportEntry } from '../models/APIKeyExportEntry.js';
import { APIKeyView } from '../models/APIKeyView.js';
import { AccountUserView } from '../models/AccountUserView.js';
import { AccountView } from '../models/AccountView.js';
import { AgentIdentity } from '../models/AgentIdentity.js';
import { AgentView } from '../models/AgentView.js';
import { ApproveRequest } from '../models/ApproveRequest.js';
import { Attachment } from '../models/Attachment.js';
import { AttachmentMetaView } from '../models/AttachmentMetaView.js';
import { AttachmentView } from '../models/AttachmentView.js';
import { AuthVerdict } from '../models/AuthVerdict.js';
import { CheckResult } from '../models/CheckResult.js';
import { ConversationDetailView } from '../models/ConversationDetailView.js';
import { ConversationSummaryView } from '../models/ConversationSummaryView.js';
import { CreateAPIKeyRequest } from '../models/CreateAPIKeyRequest.js';
import { CreateAPIKeyResponse } from '../models/CreateAPIKeyResponse.js';
import { CreateAgentRequest } from '../models/CreateAgentRequest.js';
import { CreateTemplateRequest } from '../models/CreateTemplateRequest.js';
import { CreateWebhookRequest } from '../models/CreateWebhookRequest.js';
import { CreateWebhookResponse } from '../models/CreateWebhookResponse.js';
import { DNSRecord } from '../models/DNSRecord.js';
import { DeleteUserDataResult } from '../models/DeleteUserDataResult.js';
import { DeliveryStatusJSON } from '../models/DeliveryStatusJSON.js';
import { DeploymentInfoView } from '../models/DeploymentInfoView.js';
import { Domain } from '../models/Domain.js';
import { DomainView } from '../models/DomainView.js';
import { ErrorBody } from '../models/ErrorBody.js';
import { ErrorBodyDetails } from '../models/ErrorBodyDetails.js';
import { ErrorEnvelope } from '../models/ErrorEnvelope.js';
import { EventJSON } from '../models/EventJSON.js';
import { FieldError } from '../models/FieldError.js';
import { ForwardRequest } from '../models/ForwardRequest.js';
import { LimitExceededDetails } from '../models/LimitExceededDetails.js';
import { LimitExceededEnvelope } from '../models/LimitExceededEnvelope.js';
import { LimitExceededErrorBody } from '../models/LimitExceededErrorBody.js';
import { LimitsCapsView } from '../models/LimitsCapsView.js';
import { LimitsUsageView } from '../models/LimitsUsageView.js';
import { Message } from '../models/Message.js';
import { MessageBodyView } from '../models/MessageBodyView.js';
import { MessageParsedView } from '../models/MessageParsedView.js';
import { MessageSummaryView } from '../models/MessageSummaryView.js';
import { MessageView } from '../models/MessageView.js';
import { OAuthConnectionEntry } from '../models/OAuthConnectionEntry.js';
import { PageAPIKeyView } from '../models/PageAPIKeyView.js';
import { PageAgentView } from '../models/PageAgentView.js';
import { PageConversationSummaryView } from '../models/PageConversationSummaryView.js';
import { PageDomainView } from '../models/PageDomainView.js';
import { PageEventJSON } from '../models/PageEventJSON.js';
import { PageMessageSummaryView } from '../models/PageMessageSummaryView.js';
import { PageReviewView } from '../models/PageReviewView.js';
import { PageStarterTemplateView } from '../models/PageStarterTemplateView.js';
import { PageSuppression } from '../models/PageSuppression.js';
import { PageTemplateSummaryView } from '../models/PageTemplateSummaryView.js';
import { PageWebhookDeliveryView } from '../models/PageWebhookDeliveryView.js';
import { PageWebhookView } from '../models/PageWebhookView.js';
import { ProtectionConfigView } from '../models/ProtectionConfigView.js';
import { ProtectionDirectionView } from '../models/ProtectionDirectionView.js';
import { ProtectionEventExportEntry } from '../models/ProtectionEventExportEntry.js';
import { ProtectionGateView } from '../models/ProtectionGateView.js';
import { ProtectionHoldsView } from '../models/ProtectionHoldsView.js';
import { ProtectionScanView } from '../models/ProtectionScanView.js';
import { RateLimitedDetails } from '../models/RateLimitedDetails.js';
import { RateLimitedEnvelope } from '../models/RateLimitedEnvelope.js';
import { RateLimitedErrorBody } from '../models/RateLimitedErrorBody.js';
import { RedeliverDelivery } from '../models/RedeliverDelivery.js';
import { RedeliverEventRequest } from '../models/RedeliverEventRequest.js';
import { RedeliverView } from '../models/RedeliverView.js';
import { RegisterDomainRequest } from '../models/RegisterDomainRequest.js';
import { RejectRequest } from '../models/RejectRequest.js';
import { RejectResultView } from '../models/RejectResultView.js';
import { RenderedTemplateView } from '../models/RenderedTemplateView.js';
import { ReplyRequest } from '../models/ReplyRequest.js';
import { Result } from '../models/Result.js';
import { ReviewView } from '../models/ReviewView.js';
import { RotateSecretResponse } from '../models/RotateSecretResponse.js';
import { SendEmailRequest } from '../models/SendEmailRequest.js';
import { SendResultView } from '../models/SendResultView.js';
import { StarterTemplateDetailView } from '../models/StarterTemplateDetailView.js';
import { StarterTemplateVariableView } from '../models/StarterTemplateVariableView.js';
import { StarterTemplateView } from '../models/StarterTemplateView.js';
import { Suppression } from '../models/Suppression.js';
import { SuppressionExportEntry } from '../models/SuppressionExportEntry.js';
import { TemplatePartError } from '../models/TemplatePartError.js';
import { TemplateSummaryView } from '../models/TemplateSummaryView.js';
import { TemplateView } from '../models/TemplateView.js';
import { TestWebhookRequest } from '../models/TestWebhookRequest.js';
import { TestWebhookResponse } from '../models/TestWebhookResponse.js';
import { UpdateAgentRequest } from '../models/UpdateAgentRequest.js';
import { UpdateMessageRequest } from '../models/UpdateMessageRequest.js';
import { UpdateMessageResultView } from '../models/UpdateMessageResultView.js';
import { UpdateTemplateRequest } from '../models/UpdateTemplateRequest.js';
import { UpdateWebhookRequest } from '../models/UpdateWebhookRequest.js';
import { UsageEventEntry } from '../models/UsageEventEntry.js';
import { UserExport } from '../models/UserExport.js';
import { UserExportUser } from '../models/UserExportUser.js';
import { ValidateTemplateRequest } from '../models/ValidateTemplateRequest.js';
import { ValidateTemplateResponse } from '../models/ValidateTemplateResponse.js';
import { ValidationErrorDetails } from '../models/ValidationErrorDetails.js';
import { VerifyDomainView } from '../models/VerifyDomainView.js';
import { WebhookDeliveryView } from '../models/WebhookDeliveryView.js';
import { WebhookFiltersView } from '../models/WebhookFiltersView.js';
import { WebhookView } from '../models/WebhookView.js';
import { ObservableAccountApi } from './ObservableAPI.js';

import { AccountApiRequestFactory, AccountApiResponseProcessor} from "../apis/AccountApi.js";
export class PromiseAccountApi {
    private api: ObservableAccountApi

    public constructor(
        configuration: Configuration,
        requestFactory?: AccountApiRequestFactory,
        responseProcessor?: AccountApiResponseProcessor
    ) {
        this.api = new ObservableAccountApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Mint a new API key; the plaintext key is returned once. scope=account is workspace admin (agent/domain/key management); scope=agent binds the key to one inbox so it can act only as that agent. Account scope only.
     * Create an API key
     * @param createAPIKeyRequest
     */
    public createApiKeyWithHttpInfo(createAPIKeyRequest: CreateAPIKeyRequest, _options?: PromiseConfigurationOptions): Promise<HttpInfo<CreateAPIKeyResponse>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.createApiKeyWithHttpInfo(createAPIKeyRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Mint a new API key; the plaintext key is returned once. scope=account is workspace admin (agent/domain/key management); scope=agent binds the key to one inbox so it can act only as that agent. Account scope only.
     * Create an API key
     * @param createAPIKeyRequest
     */
    public createApiKey(createAPIKeyRequest: CreateAPIKeyRequest, _options?: PromiseConfigurationOptions): Promise<CreateAPIKeyResponse> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.createApiKey(createAPIKeyRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Permanently deletes the account and cascades all owned data. Requires ?confirm=DELETE.
     * Delete your account + all data (irreversible)
     * @param confirm Must be the literal DELETE — this action is irreversible.
     */
    public deleteAccountWithHttpInfo(confirm: 'DELETE', _options?: PromiseConfigurationOptions): Promise<HttpInfo<DeleteUserDataResult>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.deleteAccountWithHttpInfo(confirm, observableOptions);
        return result.toPromise();
    }

    /**
     * Permanently deletes the account and cascades all owned data. Requires ?confirm=DELETE.
     * Delete your account + all data (irreversible)
     * @param confirm Must be the literal DELETE — this action is irreversible.
     */
    public deleteAccount(confirm: 'DELETE', _options?: PromiseConfigurationOptions): Promise<DeleteUserDataResult> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.deleteAccount(confirm, observableOptions);
        return result.toPromise();
    }

    /**
     * Revoke a key by id. Integrations using it stop authenticating immediately. Account scope only. Requires ?confirm=DELETE.
     * Revoke an API key
     * @param id
     * @param confirm Must be the literal DELETE — this action is irreversible.
     */
    public deleteApiKeyWithHttpInfo(id: string, confirm: 'DELETE', _options?: PromiseConfigurationOptions): Promise<HttpInfo<void>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.deleteApiKeyWithHttpInfo(id, confirm, observableOptions);
        return result.toPromise();
    }

    /**
     * Revoke a key by id. Integrations using it stop authenticating immediately. Account scope only. Requires ?confirm=DELETE.
     * Revoke an API key
     * @param id
     * @param confirm Must be the literal DELETE — this action is irreversible.
     */
    public deleteApiKey(id: string, confirm: 'DELETE', _options?: PromiseConfigurationOptions): Promise<void> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.deleteApiKey(id, confirm, observableOptions);
        return result.toPromise();
    }

    /**
     * Un-suppress a recipient. A previously-blocked send to it then succeeds (idempotency keys are released, so no fresh key is needed). Requires ?confirm=DELETE.
     * Remove an address from the suppression list
     * @param address
     * @param confirm Must be the literal DELETE — this action is irreversible.
     */
    public deleteSuppressionWithHttpInfo(address: string, confirm: 'DELETE', _options?: PromiseConfigurationOptions): Promise<HttpInfo<void>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.deleteSuppressionWithHttpInfo(address, confirm, observableOptions);
        return result.toPromise();
    }

    /**
     * Un-suppress a recipient. A previously-blocked send to it then succeeds (idempotency keys are released, so no fresh key is needed). Requires ?confirm=DELETE.
     * Remove an address from the suppression list
     * @param address
     * @param confirm Must be the literal DELETE — this action is irreversible.
     */
    public deleteSuppression(address: string, confirm: 'DELETE', _options?: PromiseConfigurationOptions): Promise<void> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.deleteSuppression(address, confirm, observableOptions);
        return result.toPromise();
    }

    /**
     * A JSON dump of every record the authenticated account owns.
     * Export your data (GDPR right-of-access)
     */
    public exportAccountWithHttpInfo(_options?: PromiseConfigurationOptions): Promise<HttpInfo<UserExport>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.exportAccountWithHttpInfo(observableOptions);
        return result.toPromise();
    }

    /**
     * A JSON dump of every record the authenticated account owns.
     * Export your data (GDPR right-of-access)
     */
    public exportAccount(_options?: PromiseConfigurationOptions): Promise<UserExport> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.exportAccount(observableOptions);
        return result.toPromise();
    }

    /**
     * The authenticated principal\'s identity (user + scope; agent_email for agent-scoped credentials), plan caps, and current usage. Works for both account- and agent-scoped credentials. (Deployment discovery — shared domain, slug registration — is the separate public GET /v1/info.)
     * Get account: identity + plan limits + usage (whoami)
     */
    public getAccountWithHttpInfo(_options?: PromiseConfigurationOptions): Promise<HttpInfo<AccountView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getAccountWithHttpInfo(observableOptions);
        return result.toPromise();
    }

    /**
     * The authenticated principal\'s identity (user + scope; agent_email for agent-scoped credentials), plan caps, and current usage. Works for both account- and agent-scoped credentials. (Deployment discovery — shared domain, slug registration — is the separate public GET /v1/info.)
     * Get account: identity + plan limits + usage (whoami)
     */
    public getAccount(_options?: PromiseConfigurationOptions): Promise<AccountView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getAccount(observableOptions);
        return result.toPromise();
    }

    /**
     * API keys for the account (metadata only — secrets are shown once, at creation). Account scope only: an agent-scoped credential cannot manage keys.
     * List API keys
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listApiKeysWithHttpInfo(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<HttpInfo<PageAPIKeyView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listApiKeysWithHttpInfo(cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * API keys for the account (metadata only — secrets are shown once, at creation). Account scope only: an agent-scoped credential cannot manage keys.
     * List API keys
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listApiKeys(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<PageAPIKeyView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listApiKeys(cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * Addresses e2a will refuse to send to (auto-added on a hard bounce or complaint, or added manually). Sends to a suppressed address fail with recipient_suppressed.
     * List suppressed recipient addresses
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listSuppressionsWithHttpInfo(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<HttpInfo<PageSuppression>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listSuppressionsWithHttpInfo(cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * Addresses e2a will refuse to send to (auto-added on a hard bounce or complaint, or added manually). Sends to a suppressed address fail with recipient_suppressed.
     * List suppressed recipient addresses
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listSuppressions(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<PageSuppression> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listSuppressions(cursor, limit, observableOptions);
        return result.toPromise();
    }


}



import { ObservableAgentsApi } from './ObservableAPI.js';

import { AgentsApiRequestFactory, AgentsApiResponseProcessor} from "../apis/AgentsApi.js";
export class PromiseAgentsApi {
    private api: ObservableAgentsApi

    public constructor(
        configuration: Configuration,
        requestFactory?: AgentsApiRequestFactory,
        responseProcessor?: AgentsApiResponseProcessor
    ) {
        this.api = new ObservableAgentsApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Register an agent by full email. A custom-domain agent\'s domain must be a verified domain the caller owns; an email on the deployment\'s shared domain (e.g. xyz@agents.e2a.dev) is registered as a shared-domain agent. Returns the full agent.
     * Create an agent
     * @param createAgentRequest
     */
    public createAgentWithHttpInfo(createAgentRequest: CreateAgentRequest, _options?: PromiseConfigurationOptions): Promise<HttpInfo<AgentView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.createAgentWithHttpInfo(createAgentRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Register an agent by full email. A custom-domain agent\'s domain must be a verified domain the caller owns; an email on the deployment\'s shared domain (e.g. xyz@agents.e2a.dev) is registered as a shared-domain agent. Returns the full agent.
     * Create an agent
     * @param createAgentRequest
     */
    public createAgent(createAgentRequest: CreateAgentRequest, _options?: PromiseConfigurationOptions): Promise<AgentView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.createAgent(createAgentRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Delete an agent the caller owns. Requires ?confirm=DELETE (irreversible).
     * Delete an agent
     * @param email
     * @param confirm Must be the literal DELETE — this action is irreversible.
     */
    public deleteAgentWithHttpInfo(email: string, confirm: 'DELETE', _options?: PromiseConfigurationOptions): Promise<HttpInfo<void>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.deleteAgentWithHttpInfo(email, confirm, observableOptions);
        return result.toPromise();
    }

    /**
     * Delete an agent the caller owns. Requires ?confirm=DELETE (irreversible).
     * Delete an agent
     * @param email
     * @param confirm Must be the literal DELETE — this action is irreversible.
     */
    public deleteAgent(email: string, confirm: 'DELETE', _options?: PromiseConfigurationOptions): Promise<void> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.deleteAgent(email, confirm, observableOptions);
        return result.toPromise();
    }

    /**
     * Fetch a single agent the authenticated account owns, by full email address.
     * Get an agent
     * @param email The agent\&#39;s full email address, e.g. support@acme.com.
     */
    public getAgentWithHttpInfo(email: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<AgentView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getAgentWithHttpInfo(email, observableOptions);
        return result.toPromise();
    }

    /**
     * Fetch a single agent the authenticated account owns, by full email address.
     * Get an agent
     * @param email The agent\&#39;s full email address, e.g. support@acme.com.
     */
    public getAgent(email: string, _options?: PromiseConfigurationOptions): Promise<AgentView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getAgent(email, observableOptions);
        return result.toPromise();
    }

    /**
     * Read the agent\'s protection posture — inbound/outbound trust gate, content-scan sensitivity, and hold-queue mechanism. Account scope only: an agent-scoped credential cannot read its own protection config. Beta: the agent protection config is unstable — its shape may change before it is declared stable.
     * Get an agent\'s protection config (beta)
     * @param email The agent\&#39;s full email address.
     */
    public getAgentProtectionWithHttpInfo(email: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<ProtectionConfigView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getAgentProtectionWithHttpInfo(email, observableOptions);
        return result.toPromise();
    }

    /**
     * Read the agent\'s protection posture — inbound/outbound trust gate, content-scan sensitivity, and hold-queue mechanism. Account scope only: an agent-scoped credential cannot read its own protection config. Beta: the agent protection config is unstable — its shape may change before it is declared stable.
     * Get an agent\'s protection config (beta)
     * @param email The agent\&#39;s full email address.
     */
    public getAgentProtection(email: string, _options?: PromiseConfigurationOptions): Promise<ProtectionConfigView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getAgentProtection(email, observableOptions);
        return result.toPromise();
    }

    /**
     * List the agents owned by the authenticated account, newest first, with cursor pagination.
     * List agents
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listAgentsWithHttpInfo(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<HttpInfo<PageAgentView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listAgentsWithHttpInfo(cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * List the agents owned by the authenticated account, newest first, with cursor pagination.
     * List agents
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listAgents(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<PageAgentView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listAgents(cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * Replace the agent\'s protection posture wholesale. The three top-level keys (inbound, outbound, holds) are required; leaves default. Account scope only. Beta: the agent protection config is unstable — its shape may change before it is declared stable.
     * Replace an agent\'s protection config (beta)
     * @param email The agent\&#39;s full email address.
     * @param protectionConfigView
     */
    public putAgentProtectionWithHttpInfo(email: string, protectionConfigView: ProtectionConfigView, _options?: PromiseConfigurationOptions): Promise<HttpInfo<ProtectionConfigView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.putAgentProtectionWithHttpInfo(email, protectionConfigView, observableOptions);
        return result.toPromise();
    }

    /**
     * Replace the agent\'s protection posture wholesale. The three top-level keys (inbound, outbound, holds) are required; leaves default. Account scope only. Beta: the agent protection config is unstable — its shape may change before it is declared stable.
     * Replace an agent\'s protection config (beta)
     * @param email The agent\&#39;s full email address.
     * @param protectionConfigView
     */
    public putAgentProtection(email: string, protectionConfigView: ProtectionConfigView, _options?: PromiseConfigurationOptions): Promise<ProtectionConfigView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.putAgentProtection(email, protectionConfigView, observableOptions);
        return result.toPromise();
    }

    /**
     * Send a platform test email to the agent\'s own address to confirm inbound delivery. 202 when held for HITL.
     * Send a test email to the agent\'s own address
     * @param email The agent\&#39;s full email address, e.g. support@acme.com.
     */
    public testAgentWithHttpInfo(email: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<SendResultView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.testAgentWithHttpInfo(email, observableOptions);
        return result.toPromise();
    }

    /**
     * Send a platform test email to the agent\'s own address to confirm inbound delivery. 202 when held for HITL.
     * Send a test email to the agent\'s own address
     * @param email The agent\&#39;s full email address, e.g. support@acme.com.
     */
    public testAgent(email: string, _options?: PromiseConfigurationOptions): Promise<SendResultView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.testAgent(email, observableOptions);
        return result.toPromise();
    }

    /**
     * Update an agent\'s display name. The screening/protection config lives on the /v1/agents/{email}/protection sub-resource. Returns the post-update agent.
     * Update an agent
     * @param email
     * @param updateAgentRequest
     */
    public updateAgentWithHttpInfo(email: string, updateAgentRequest: UpdateAgentRequest, _options?: PromiseConfigurationOptions): Promise<HttpInfo<AgentView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.updateAgentWithHttpInfo(email, updateAgentRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Update an agent\'s display name. The screening/protection config lives on the /v1/agents/{email}/protection sub-resource. Returns the post-update agent.
     * Update an agent
     * @param email
     * @param updateAgentRequest
     */
    public updateAgent(email: string, updateAgentRequest: UpdateAgentRequest, _options?: PromiseConfigurationOptions): Promise<AgentView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.updateAgent(email, updateAgentRequest, observableOptions);
        return result.toPromise();
    }


}



import { ObservableConversationsApi } from './ObservableAPI.js';

import { ConversationsApiRequestFactory, ConversationsApiResponseProcessor} from "../apis/ConversationsApi.js";
export class PromiseConversationsApi {
    private api: ObservableConversationsApi

    public constructor(
        configuration: Configuration,
        requestFactory?: ConversationsApiRequestFactory,
        responseProcessor?: ConversationsApiResponseProcessor
    ) {
        this.api = new ObservableConversationsApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Fetch a single conversation thread with its participants, labels, and member messages.
     * Get a conversation
     * @param email
     * @param id
     */
    public getConversationWithHttpInfo(email: string, id: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<ConversationDetailView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getConversationWithHttpInfo(email, id, observableOptions);
        return result.toPromise();
    }

    /**
     * Fetch a single conversation thread with its participants, labels, and member messages.
     * Get a conversation
     * @param email
     * @param id
     */
    public getConversation(email: string, id: string, _options?: PromiseConfigurationOptions): Promise<ConversationDetailView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getConversation(email, id, observableOptions);
        return result.toPromise();
    }

    /**
     * List an agent\'s conversation threads (derived from messages.conversation_id).
     * List conversations
     * @param email
     * @param [since] RFC3339.
     * @param [until] RFC3339.
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change since/until.
     * @param [limit]
     */
    public listConversationsWithHttpInfo(email: string, since?: string, until?: string, cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<HttpInfo<PageConversationSummaryView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listConversationsWithHttpInfo(email, since, until, cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * List an agent\'s conversation threads (derived from messages.conversation_id).
     * List conversations
     * @param email
     * @param [since] RFC3339.
     * @param [until] RFC3339.
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change since/until.
     * @param [limit]
     */
    public listConversations(email: string, since?: string, until?: string, cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<PageConversationSummaryView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listConversations(email, since, until, cursor, limit, observableOptions);
        return result.toPromise();
    }


}



import { ObservableDomainsApi } from './ObservableAPI.js';

import { DomainsApiRequestFactory, DomainsApiResponseProcessor} from "../apis/DomainsApi.js";
export class PromiseDomainsApi {
    private api: ObservableDomainsApi

    public constructor(
        configuration: Configuration,
        requestFactory?: DomainsApiRequestFactory,
        responseProcessor?: DomainsApiResponseProcessor
    ) {
        this.api = new ObservableDomainsApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Deprovisions the domain\'s sending identity and breaks sending for every agent on it. Requires ?confirm=DELETE (irreversible).
     * Delete a domain
     * @param domain
     * @param confirm Must be the literal DELETE — this action is irreversible.
     */
    public deleteDomainWithHttpInfo(domain: string, confirm: 'DELETE', _options?: PromiseConfigurationOptions): Promise<HttpInfo<void>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.deleteDomainWithHttpInfo(domain, confirm, observableOptions);
        return result.toPromise();
    }

    /**
     * Deprovisions the domain\'s sending identity and breaks sending for every agent on it. Requires ?confirm=DELETE (irreversible).
     * Delete a domain
     * @param domain
     * @param confirm Must be the literal DELETE — this action is irreversible.
     */
    public deleteDomain(domain: string, confirm: 'DELETE', _options?: PromiseConfigurationOptions): Promise<void> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.deleteDomain(domain, confirm, observableOptions);
        return result.toPromise();
    }

    /**
     * Get a domain
     * @param domain
     */
    public getDomainWithHttpInfo(domain: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<DomainView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getDomainWithHttpInfo(domain, observableOptions);
        return result.toPromise();
    }

    /**
     * Get a domain
     * @param domain
     */
    public getDomain(domain: string, _options?: PromiseConfigurationOptions): Promise<DomainView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getDomain(domain, observableOptions);
        return result.toPromise();
    }

    /**
     * List the domains owned by the authenticated account, newest first, with cursor pagination.
     * List domains
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listDomainsWithHttpInfo(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<HttpInfo<PageDomainView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listDomainsWithHttpInfo(cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * List the domains owned by the authenticated account, newest first, with cursor pagination.
     * List domains
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listDomains(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<PageDomainView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listDomains(cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * Register a domain
     * @param registerDomainRequest
     */
    public registerDomainWithHttpInfo(registerDomainRequest: RegisterDomainRequest, _options?: PromiseConfigurationOptions): Promise<HttpInfo<DomainView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.registerDomainWithHttpInfo(registerDomainRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Register a domain
     * @param registerDomainRequest
     */
    public registerDomain(registerDomainRequest: RegisterDomainRequest, _options?: PromiseConfigurationOptions): Promise<DomainView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.registerDomain(registerDomainRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Probe the domain\'s published DNS and, when the verification TXT (and inbound MX) are present, mark it verified. Always returns 200 with the per-record diagnostic — branch on the `verified` boolean in the body, not the HTTP status. A not-yet-published record is the normal `verified:false` outcome, not an error.
     * Verify a domain
     * @param domain
     */
    public verifyDomainWithHttpInfo(domain: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<VerifyDomainView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.verifyDomainWithHttpInfo(domain, observableOptions);
        return result.toPromise();
    }

    /**
     * Probe the domain\'s published DNS and, when the verification TXT (and inbound MX) are present, mark it verified. Always returns 200 with the per-record diagnostic — branch on the `verified` boolean in the body, not the HTTP status. A not-yet-published record is the normal `verified:false` outcome, not an error.
     * Verify a domain
     * @param domain
     */
    public verifyDomain(domain: string, _options?: PromiseConfigurationOptions): Promise<VerifyDomainView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.verifyDomain(domain, observableOptions);
        return result.toPromise();
    }


}



import { ObservableEventsApi } from './ObservableAPI.js';

import { EventsApiRequestFactory, EventsApiResponseProcessor} from "../apis/EventsApi.js";
export class PromiseEventsApi {
    private api: ObservableEventsApi

    public constructor(
        configuration: Configuration,
        requestFactory?: EventsApiRequestFactory,
        responseProcessor?: EventsApiResponseProcessor
    ) {
        this.api = new ObservableEventsApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Get an event
     * @param id
     */
    public getEventWithHttpInfo(id: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<EventJSON>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getEventWithHttpInfo(id, observableOptions);
        return result.toPromise();
    }

    /**
     * Get an event
     * @param id
     */
    public getEvent(id: string, _options?: PromiseConfigurationOptions): Promise<EventJSON> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getEvent(id, observableOptions);
        return result.toPromise();
    }

    /**
     * The webhook-event delivery log, filterable by type/agent/conversation/message and time range, with cursor pagination.
     * List events
     * @param [type]
     * @param [agentEmail]
     * @param [conversationId]
     * @param [messageId]
     * @param [since] RFC3339.
     * @param [until] RFC3339.
     * @param [cursor]
     * @param [limit]
     */
    public listEventsWithHttpInfo(type?: string, agentEmail?: string, conversationId?: string, messageId?: string, since?: string, until?: string, cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<HttpInfo<PageEventJSON>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listEventsWithHttpInfo(type, agentEmail, conversationId, messageId, since, until, cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * The webhook-event delivery log, filterable by type/agent/conversation/message and time range, with cursor pagination.
     * List events
     * @param [type]
     * @param [agentEmail]
     * @param [conversationId]
     * @param [messageId]
     * @param [since] RFC3339.
     * @param [until] RFC3339.
     * @param [cursor]
     * @param [limit]
     */
    public listEvents(type?: string, agentEmail?: string, conversationId?: string, messageId?: string, since?: string, until?: string, cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<PageEventJSON> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listEvents(type, agentEmail, conversationId, messageId, since, until, cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * Re-enqueue webhook delivery for an event. With a webhook_id, replays to that subscriber; without, fans out to every originally-matched subscriber. Auto-deduplicated within a short window — receivers must dedup on event id. Returns 202 Accepted: the redelivery is durably enqueued for async submission, not delivered synchronously — the per-subscriber outcome surfaces via the delivery log, and each delivery\'s status is \'pending\' (or \'scheduled\' for the fan-out).
     * Redeliver an event
     * @param id
     * @param redeliverEventRequest
     */
    public redeliverEventWithHttpInfo(id: string, redeliverEventRequest: RedeliverEventRequest, _options?: PromiseConfigurationOptions): Promise<HttpInfo<RedeliverView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.redeliverEventWithHttpInfo(id, redeliverEventRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Re-enqueue webhook delivery for an event. With a webhook_id, replays to that subscriber; without, fans out to every originally-matched subscriber. Auto-deduplicated within a short window — receivers must dedup on event id. Returns 202 Accepted: the redelivery is durably enqueued for async submission, not delivered synchronously — the per-subscriber outcome surfaces via the delivery log, and each delivery\'s status is \'pending\' (or \'scheduled\' for the fan-out).
     * Redeliver an event
     * @param id
     * @param redeliverEventRequest
     */
    public redeliverEvent(id: string, redeliverEventRequest: RedeliverEventRequest, _options?: PromiseConfigurationOptions): Promise<RedeliverView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.redeliverEvent(id, redeliverEventRequest, observableOptions);
        return result.toPromise();
    }


}



import { ObservableMessagesApi } from './ObservableAPI.js';

import { MessagesApiRequestFactory, MessagesApiResponseProcessor} from "../apis/MessagesApi.js";
export class PromiseMessagesApi {
    private api: ObservableMessagesApi

    public constructor(
        configuration: Configuration,
        requestFactory?: MessagesApiRequestFactory,
        responseProcessor?: MessagesApiResponseProcessor
    ) {
        this.api = new ObservableMessagesApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Forward a message (inbound or outbound) to new recipients; the original is quoted and its attachments are carried over by default. Any attachments[] you supply are added on top of the originals. 202 when held for HITL. Attachment limits apply to the combined set (carried-over originals + supplied): at most 10 attachments, each ≤ 10 MB decoded, ≤ 25 MB decoded combined (over-count → 400 invalid_request; over-size → 413 payload_too_large).
     * Forward a message
     * @param email
     * @param id
     * @param forwardRequest
     * @param [idempotencyKey]
     * @param [wait] Sync-compat valve. wait&#x3D;sent holds the request until the message reaches a terminal-or-held state or a bounded timeout (≤20s), then returns that state; on timeout returns status&#x3D;accepted. Default: no wait. Always branch on body.status, not the HTTP code. No-op until the async pipeline ships — a synchronous server already has the outcome.
     */
    public forwardMessageWithHttpInfo(email: string, id: string, forwardRequest: ForwardRequest, idempotencyKey?: string, wait?: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<SendResultView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.forwardMessageWithHttpInfo(email, id, forwardRequest, idempotencyKey, wait, observableOptions);
        return result.toPromise();
    }

    /**
     * Forward a message (inbound or outbound) to new recipients; the original is quoted and its attachments are carried over by default. Any attachments[] you supply are added on top of the originals. 202 when held for HITL. Attachment limits apply to the combined set (carried-over originals + supplied): at most 10 attachments, each ≤ 10 MB decoded, ≤ 25 MB decoded combined (over-count → 400 invalid_request; over-size → 413 payload_too_large).
     * Forward a message
     * @param email
     * @param id
     * @param forwardRequest
     * @param [idempotencyKey]
     * @param [wait] Sync-compat valve. wait&#x3D;sent holds the request until the message reaches a terminal-or-held state or a bounded timeout (≤20s), then returns that state; on timeout returns status&#x3D;accepted. Default: no wait. Always branch on body.status, not the HTTP code. No-op until the async pipeline ships — a synchronous server already has the outcome.
     */
    public forwardMessage(email: string, id: string, forwardRequest: ForwardRequest, idempotencyKey?: string, wait?: string, _options?: PromiseConfigurationOptions): Promise<SendResultView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.forwardMessage(email, id, forwardRequest, idempotencyKey, wait, observableOptions);
        return result.toPromise();
    }

    /**
     * Returns one attachment\'s metadata plus a short-lived `download_url` (+ `expires_at`) to fetch the bytes out of band — so binary content never streams through an agent\'s context. Pass `?inline=true` to also receive base64 `data` for small attachments (<= 256 KB); larger inline requests are rejected. `index` is the 0-based attachment index from the message\'s `attachments[]`.
     * Get an attachment (metadata + short-lived download URL)
     * @param email
     * @param id
     * @param index
     * @param [inline] When true, also include the bytes as base64 in \&#39;data\&#39; — ONLY for attachments &lt;&#x3D; 256 KB; larger inline requests are rejected (413). Default false (use download_url).
     */
    public getAttachmentWithHttpInfo(email: string, id: string, index: number, inline?: boolean, _options?: PromiseConfigurationOptions): Promise<HttpInfo<AttachmentView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getAttachmentWithHttpInfo(email, id, index, inline, observableOptions);
        return result.toPromise();
    }

    /**
     * Returns one attachment\'s metadata plus a short-lived `download_url` (+ `expires_at`) to fetch the bytes out of band — so binary content never streams through an agent\'s context. Pass `?inline=true` to also receive base64 `data` for small attachments (<= 256 KB); larger inline requests are rejected. `index` is the 0-based attachment index from the message\'s `attachments[]`.
     * Get an attachment (metadata + short-lived download URL)
     * @param email
     * @param id
     * @param index
     * @param [inline] When true, also include the bytes as base64 in \&#39;data\&#39; — ONLY for attachments &lt;&#x3D; 256 KB; larger inline requests are rejected (413). Default false (use download_url).
     */
    public getAttachment(email: string, id: string, index: number, inline?: boolean, _options?: PromiseConfigurationOptions): Promise<AttachmentView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getAttachment(email, id, index, inline, observableOptions);
        return result.toPromise();
    }

    /**
     * Fetch a single message (inbound or outbound) by id, scoped to an agent the caller owns. Includes the raw message and inbound auth headers.
     * Get a message
     * @param email The agent\&#39;s full email address.
     * @param id The message id, e.g. msg_abc123.
     */
    public getMessageWithHttpInfo(email: string, id: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<MessageView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getMessageWithHttpInfo(email, id, observableOptions);
        return result.toPromise();
    }

    /**
     * Fetch a single message (inbound or outbound) by id, scoped to an agent the caller owns. Includes the raw message and inbound auth headers.
     * Get a message
     * @param email The agent\&#39;s full email address.
     * @param id The message id, e.g. msg_abc123.
     */
    public getMessage(email: string, id: string, _options?: PromiseConfigurationOptions): Promise<MessageView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getMessage(email, id, observableOptions);
        return result.toPromise();
    }

    /**
     * List an agent\'s messages (inbound + outbound) with filters and cursor pagination. Held outbound drafts appear as status=pending_review.
     * List messages
     * @param email
     * @param [direction] Defaults to inbound.
     * @param [readStatus] Inbound only. Filters by inbox read-state (MSG-1). Defaults to unread for inbound, all otherwise.
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
    public listMessagesWithHttpInfo(email: string, direction?: 'inbound' | 'outbound' | 'all', readStatus?: 'unread' | 'read' | 'all', sort?: 'asc' | 'desc', _from?: string, subjectContains?: string, conversationId?: string, labels?: Array<string>, since?: string, until?: string, cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<HttpInfo<PageMessageSummaryView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listMessagesWithHttpInfo(email, direction, readStatus, sort, _from, subjectContains, conversationId, labels, since, until, cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * List an agent\'s messages (inbound + outbound) with filters and cursor pagination. Held outbound drafts appear as status=pending_review.
     * List messages
     * @param email
     * @param [direction] Defaults to inbound.
     * @param [readStatus] Inbound only. Filters by inbox read-state (MSG-1). Defaults to unread for inbound, all otherwise.
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
    public listMessages(email: string, direction?: 'inbound' | 'outbound' | 'all', readStatus?: 'unread' | 'read' | 'all', sort?: 'asc' | 'desc', _from?: string, subjectContains?: string, conversationId?: string, labels?: Array<string>, since?: string, until?: string, cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<PageMessageSummaryView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listMessages(email, direction, readStatus, sort, _from, subjectContains, conversationId, labels, since, until, cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * Reply to a message (inbound or outbound); recipients and threading are derived from the original. Replying to a message the agent received targets its sender; replying to a message the agent sent continues the thread to its original recipients (`reply_all` also re-includes the original Cc). 202 when held for HITL. Attachment limits: at most 10 attachments, each ≤ 10 MB decoded, ≤ 25 MB decoded combined (over-count → 400 invalid_request; over-size → 413 payload_too_large).
     * Reply to a message
     * @param email
     * @param id
     * @param replyRequest
     * @param [idempotencyKey]
     * @param [wait] Sync-compat valve. wait&#x3D;sent holds the request until the message reaches a terminal-or-held state or a bounded timeout (≤20s), then returns that state; on timeout returns status&#x3D;accepted. Default: no wait. Always branch on body.status, not the HTTP code. No-op until the async pipeline ships — a synchronous server already has the outcome.
     */
    public replyToMessageWithHttpInfo(email: string, id: string, replyRequest: ReplyRequest, idempotencyKey?: string, wait?: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<SendResultView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.replyToMessageWithHttpInfo(email, id, replyRequest, idempotencyKey, wait, observableOptions);
        return result.toPromise();
    }

    /**
     * Reply to a message (inbound or outbound); recipients and threading are derived from the original. Replying to a message the agent received targets its sender; replying to a message the agent sent continues the thread to its original recipients (`reply_all` also re-includes the original Cc). 202 when held for HITL. Attachment limits: at most 10 attachments, each ≤ 10 MB decoded, ≤ 25 MB decoded combined (over-count → 400 invalid_request; over-size → 413 payload_too_large).
     * Reply to a message
     * @param email
     * @param id
     * @param replyRequest
     * @param [idempotencyKey]
     * @param [wait] Sync-compat valve. wait&#x3D;sent holds the request until the message reaches a terminal-or-held state or a bounded timeout (≤20s), then returns that state; on timeout returns status&#x3D;accepted. Default: no wait. Always branch on body.status, not the HTTP code. No-op until the async pipeline ships — a synchronous server already has the outcome.
     */
    public replyToMessage(email: string, id: string, replyRequest: ReplyRequest, idempotencyKey?: string, wait?: string, _options?: PromiseConfigurationOptions): Promise<SendResultView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.replyToMessage(email, id, replyRequest, idempotencyKey, wait, observableOptions);
        return result.toPromise();
    }

    /**
     * Send a new email from the agent named in the path (a new thread). The sender is the path agent — `reply`/`forward` are their own sub-resources. 202 + pending_review when the agent has HITL enabled. Honors Idempotency-Key. Attachment limits: at most 10 attachments, each ≤ 10 MB decoded, ≤ 25 MB decoded combined (over-count → 400 invalid_request; over-size → 413 payload_too_large). Two capacity limits apply and are permanently distinct — branch on the HTTP status: 402 limit_exceeded is a QUOTA (monthly-message / storage stock-or-flow cap; a retry will not clear it — surface an upgrade path), 429 rate_limited is a throughput/request-RATE cap (transient; back off Retry-After seconds and retry).
     * Send a new email
     * @param email
     * @param sendEmailRequest
     * @param [idempotencyKey]
     * @param [wait] Sync-compat valve. wait&#x3D;sent holds the request until the message reaches a terminal-or-held state or a bounded timeout (≤20s), then returns that state; on timeout returns status&#x3D;accepted. Default: no wait. Always branch on body.status, not the HTTP code. No-op until the async pipeline ships — a synchronous server already has the outcome.
     */
    public sendMessageWithHttpInfo(email: string, sendEmailRequest: SendEmailRequest, idempotencyKey?: string, wait?: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<SendResultView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.sendMessageWithHttpInfo(email, sendEmailRequest, idempotencyKey, wait, observableOptions);
        return result.toPromise();
    }

    /**
     * Send a new email from the agent named in the path (a new thread). The sender is the path agent — `reply`/`forward` are their own sub-resources. 202 + pending_review when the agent has HITL enabled. Honors Idempotency-Key. Attachment limits: at most 10 attachments, each ≤ 10 MB decoded, ≤ 25 MB decoded combined (over-count → 400 invalid_request; over-size → 413 payload_too_large). Two capacity limits apply and are permanently distinct — branch on the HTTP status: 402 limit_exceeded is a QUOTA (monthly-message / storage stock-or-flow cap; a retry will not clear it — surface an upgrade path), 429 rate_limited is a throughput/request-RATE cap (transient; back off Retry-After seconds and retry).
     * Send a new email
     * @param email
     * @param sendEmailRequest
     * @param [idempotencyKey]
     * @param [wait] Sync-compat valve. wait&#x3D;sent holds the request until the message reaches a terminal-or-held state or a bounded timeout (≤20s), then returns that state; on timeout returns status&#x3D;accepted. Default: no wait. Always branch on body.status, not the HTTP code. No-op until the async pipeline ships — a synchronous server already has the outcome.
     */
    public sendMessage(email: string, sendEmailRequest: SendEmailRequest, idempotencyKey?: string, wait?: string, _options?: PromiseConfigurationOptions): Promise<SendResultView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.sendMessage(email, sendEmailRequest, idempotencyKey, wait, observableOptions);
        return result.toPromise();
    }

    /**
     * Apply a labels delta (`add_labels` / `remove_labels`) to a message the caller owns; returns the post-update label set. Each list is capped at 50 entries; labels are lowercase `[a-z0-9:_-]+` up to 64 chars; the `e2a:` prefix is reserved for system labels. A message carries at most 100 labels. An empty delta is a read of the current labels.
     * Update a message (labels)
     * @param email
     * @param id
     * @param updateMessageRequest
     */
    public updateMessageWithHttpInfo(email: string, id: string, updateMessageRequest: UpdateMessageRequest, _options?: PromiseConfigurationOptions): Promise<HttpInfo<UpdateMessageResultView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.updateMessageWithHttpInfo(email, id, updateMessageRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Apply a labels delta (`add_labels` / `remove_labels`) to a message the caller owns; returns the post-update label set. Each list is capped at 50 entries; labels are lowercase `[a-z0-9:_-]+` up to 64 chars; the `e2a:` prefix is reserved for system labels. A message carries at most 100 labels. An empty delta is a read of the current labels.
     * Update a message (labels)
     * @param email
     * @param id
     * @param updateMessageRequest
     */
    public updateMessage(email: string, id: string, updateMessageRequest: UpdateMessageRequest, _options?: PromiseConfigurationOptions): Promise<UpdateMessageResultView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.updateMessage(email, id, updateMessageRequest, observableOptions);
        return result.toPromise();
    }


}



import { ObservableMetaApi } from './ObservableAPI.js';

import { MetaApiRequestFactory, MetaApiResponseProcessor} from "../apis/MetaApi.js";
export class PromiseMetaApi {
    private api: ObservableMetaApi

    public constructor(
        configuration: Configuration,
        requestFactory?: MetaApiRequestFactory,
        responseProcessor?: MetaApiResponseProcessor
    ) {
        this.api = new ObservableMetaApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Public deployment metadata: the shared agent domain (if slug registration is enabled) and the public base URL. Unauthenticated.
     * Deployment info
     */
    public getInfoWithHttpInfo(_options?: PromiseConfigurationOptions): Promise<HttpInfo<DeploymentInfoView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getInfoWithHttpInfo(observableOptions);
        return result.toPromise();
    }

    /**
     * Public deployment metadata: the shared agent domain (if slug registration is enabled) and the public base URL. Unauthenticated.
     * Deployment info
     */
    public getInfo(_options?: PromiseConfigurationOptions): Promise<DeploymentInfoView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getInfo(observableOptions);
        return result.toPromise();
    }


}



import { ObservableReviewsApi } from './ObservableAPI.js';

import { ReviewsApiRequestFactory, ReviewsApiResponseProcessor} from "../apis/ReviewsApi.js";
export class PromiseReviewsApi {
    private api: ObservableReviewsApi

    public constructor(
        configuration: Configuration,
        requestFactory?: ReviewsApiRequestFactory,
        responseProcessor?: ReviewsApiResponseProcessor
    ) {
        this.api = new ObservableReviewsApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Approve a hold. Branches on direction: an outbound draft is sent via SES (honoring Idempotency-Key + optional reviewer overrides); an inbound hold is released to the inbox. Account-scoped only — an agent cannot approve its own hold. Approving an outbound draft applies the same per-agent send-rate limit as a direct send: 429 rate_limited when the agent is over its throughput limit (back off Retry-After seconds and retry).
     * Approve a held message
     * @param id
     * @param approveRequest
     * @param [idempotencyKey]
     */
    public approveReviewWithHttpInfo(id: string, approveRequest: ApproveRequest, idempotencyKey?: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<SendResultView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.approveReviewWithHttpInfo(id, approveRequest, idempotencyKey, observableOptions);
        return result.toPromise();
    }

    /**
     * Approve a hold. Branches on direction: an outbound draft is sent via SES (honoring Idempotency-Key + optional reviewer overrides); an inbound hold is released to the inbox. Account-scoped only — an agent cannot approve its own hold. Approving an outbound draft applies the same per-agent send-rate limit as a direct send: 429 rate_limited when the agent is over its throughput limit (back off Retry-After seconds and retry).
     * Approve a held message
     * @param id
     * @param approveRequest
     * @param [idempotencyKey]
     */
    public approveReview(id: string, approveRequest: ApproveRequest, idempotencyKey?: string, _options?: PromiseConfigurationOptions): Promise<SendResultView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.approveReview(id, approveRequest, idempotencyKey, observableOptions);
        return result.toPromise();
    }

    /**
     * Full detail of one held message — body + recipients (and, for inbound, the screening/auth context) — for a reviewer to make a decision. Account-scoped only.
     * Get a held message (full detail)
     * @param id
     */
    public getReviewWithHttpInfo(id: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<MessageView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getReviewWithHttpInfo(id, observableOptions);
        return result.toPromise();
    }

    /**
     * Full detail of one held message — body + recipients (and, for inbound, the screening/auth context) — for a reviewer to make a decision. Account-scoped only.
     * Get a held message (full detail)
     * @param id
     */
    public getReview(id: string, _options?: PromiseConfigurationOptions): Promise<MessageView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getReview(id, observableOptions);
        return result.toPromise();
    }

    /**
     * The review queue: every message held in pending_review across the account\'s inboxes — outbound drafts awaiting send approval AND inbound messages held by a screening gate. Account-scoped credentials only; agents cannot see (or resolve) holds.
     * List messages awaiting review
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listReviewsWithHttpInfo(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<HttpInfo<PageReviewView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listReviewsWithHttpInfo(cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * The review queue: every message held in pending_review across the account\'s inboxes — outbound drafts awaiting send approval AND inbound messages held by a screening gate. Account-scoped credentials only; agents cannot see (or resolve) holds.
     * List messages awaiting review
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listReviews(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<PageReviewView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listReviews(cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * Reject a hold. An outbound draft is discarded (never sent); an inbound hold is dropped (never reaches the agent; payload retained hidden for forensics). Account-scoped only.
     * Reject a held message
     * @param id
     * @param rejectRequest
     */
    public rejectReviewWithHttpInfo(id: string, rejectRequest: RejectRequest, _options?: PromiseConfigurationOptions): Promise<HttpInfo<RejectResultView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.rejectReviewWithHttpInfo(id, rejectRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Reject a hold. An outbound draft is discarded (never sent); an inbound hold is dropped (never reaches the agent; payload retained hidden for forensics). Account-scoped only.
     * Reject a held message
     * @param id
     * @param rejectRequest
     */
    public rejectReview(id: string, rejectRequest: RejectRequest, _options?: PromiseConfigurationOptions): Promise<RejectResultView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.rejectReview(id, rejectRequest, observableOptions);
        return result.toPromise();
    }


}



import { ObservableTemplatesApi } from './ObservableAPI.js';

import { TemplatesApiRequestFactory, TemplatesApiResponseProcessor} from "../apis/TemplatesApi.js";
export class PromiseTemplatesApi {
    private api: ObservableTemplatesApi

    public constructor(
        configuration: Configuration,
        requestFactory?: TemplatesApiRequestFactory,
        responseProcessor?: TemplatesApiResponseProcessor
    ) {
        this.api = new ObservableTemplatesApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Create a reusable email template. subject and text (and html when present) must parse: {{variable}} interpolation with dot paths; {{{variable}}} renders raw in the HTML part. Alternatively set from_starter to copy a starter template verbatim. Beta: templates are unstable — their shape may change before they are declared stable.
     * Create a template (beta)
     * @param createTemplateRequest
     */
    public createTemplateWithHttpInfo(createTemplateRequest: CreateTemplateRequest, _options?: PromiseConfigurationOptions): Promise<HttpInfo<TemplateView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.createTemplateWithHttpInfo(createTemplateRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Create a reusable email template. subject and text (and html when present) must parse: {{variable}} interpolation with dot paths; {{{variable}}} renders raw in the HTML part. Alternatively set from_starter to copy a starter template verbatim. Beta: templates are unstable — their shape may change before they are declared stable.
     * Create a template (beta)
     * @param createTemplateRequest
     */
    public createTemplate(createTemplateRequest: CreateTemplateRequest, _options?: PromiseConfigurationOptions): Promise<TemplateView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.createTemplate(createTemplateRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Delete a template. In-flight sends are unaffected (rendering happens at send time). Requires ?confirm=DELETE. Beta: templates are unstable — their shape may change before they are declared stable.
     * Delete a template (beta)
     * @param id
     * @param confirm Must be the literal DELETE — this action is irreversible.
     */
    public deleteTemplateWithHttpInfo(id: string, confirm: 'DELETE', _options?: PromiseConfigurationOptions): Promise<HttpInfo<void>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.deleteTemplateWithHttpInfo(id, confirm, observableOptions);
        return result.toPromise();
    }

    /**
     * Delete a template. In-flight sends are unaffected (rendering happens at send time). Requires ?confirm=DELETE. Beta: templates are unstable — their shape may change before they are declared stable.
     * Delete a template (beta)
     * @param id
     * @param confirm Must be the literal DELETE — this action is irreversible.
     */
    public deleteTemplate(id: string, confirm: 'DELETE', _options?: PromiseConfigurationOptions): Promise<void> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.deleteTemplate(id, confirm, observableOptions);
        return result.toPromise();
    }

    /**
     * Fetch one starter template by alias, including its full plain-text and HTML body sources. Beta: templates are unstable — their shape may change before they are declared stable.
     * Get a starter template (beta)
     * @param alias The starter template\&#39;s alias, e.g. welcome.
     */
    public getStarterTemplateWithHttpInfo(alias: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<StarterTemplateDetailView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getStarterTemplateWithHttpInfo(alias, observableOptions);
        return result.toPromise();
    }

    /**
     * Fetch one starter template by alias, including its full plain-text and HTML body sources. Beta: templates are unstable — their shape may change before they are declared stable.
     * Get a starter template (beta)
     * @param alias The starter template\&#39;s alias, e.g. welcome.
     */
    public getStarterTemplate(alias: string, _options?: PromiseConfigurationOptions): Promise<StarterTemplateDetailView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getStarterTemplate(alias, observableOptions);
        return result.toPromise();
    }

    /**
     * Fetch one template by id. Beta: templates are unstable — their shape may change before they are declared stable.
     * Get a template (beta)
     * @param id
     */
    public getTemplateWithHttpInfo(id: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<TemplateView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getTemplateWithHttpInfo(id, observableOptions);
        return result.toPromise();
    }

    /**
     * Fetch one template by id. Beta: templates are unstable — their shape may change before they are declared stable.
     * Get a template (beta)
     * @param id
     */
    public getTemplate(id: string, _options?: PromiseConfigurationOptions): Promise<TemplateView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getTemplate(id, observableOptions);
        return result.toPromise();
    }

    /**
     * List the pre-built starter templates shipped with the deployment, sorted by alias. Returns catalog metadata only; fetch one by alias for the full body sources, or copy one into your library with from_starter on POST /v1/templates. Beta: templates are unstable — their shape may change before they are declared stable.
     * List starter templates (beta)
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listStarterTemplatesWithHttpInfo(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<HttpInfo<PageStarterTemplateView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listStarterTemplatesWithHttpInfo(cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * List the pre-built starter templates shipped with the deployment, sorted by alias. Returns catalog metadata only; fetch one by alias for the full body sources, or copy one into your library with from_starter on POST /v1/templates. Beta: templates are unstable — their shape may change before they are declared stable.
     * List starter templates (beta)
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listStarterTemplates(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<PageStarterTemplateView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listStarterTemplates(cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * List the account\'s templates, newest first. Returns metadata only (no text/html); fetch one by id for the full sources. Beta: templates are unstable — their shape may change before they are declared stable.
     * List templates (beta)
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listTemplatesWithHttpInfo(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<HttpInfo<PageTemplateSummaryView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listTemplatesWithHttpInfo(cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * List the account\'s templates, newest first. Returns metadata only (no text/html); fetch one by id for the full sources. Beta: templates are unstable — their shape may change before they are declared stable.
     * List templates (beta)
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listTemplates(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<PageTemplateSummaryView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listTemplates(cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * Partial update. Changed template parts are re-parsed; set alias or html to \"\" to clear them. Beta: templates are unstable — their shape may change before they are declared stable.
     * Update a template (beta)
     * @param id
     * @param updateTemplateRequest
     */
    public updateTemplateWithHttpInfo(id: string, updateTemplateRequest: UpdateTemplateRequest, _options?: PromiseConfigurationOptions): Promise<HttpInfo<TemplateView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.updateTemplateWithHttpInfo(id, updateTemplateRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Partial update. Changed template parts are re-parsed; set alias or html to \"\" to clear them. Beta: templates are unstable — their shape may change before they are declared stable.
     * Update a template (beta)
     * @param id
     * @param updateTemplateRequest
     */
    public updateTemplate(id: string, updateTemplateRequest: UpdateTemplateRequest, _options?: PromiseConfigurationOptions): Promise<TemplateView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.updateTemplate(id, updateTemplateRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Dry-run template source without persisting: reports per-part parse errors, a rendered preview (against test_data when provided), and suggested_data — a placeholder value for every variable the source references. Beta: templates are unstable — their shape may change before they are declared stable.
     * Validate template source (beta)
     * @param validateTemplateRequest
     */
    public validateTemplateWithHttpInfo(validateTemplateRequest: ValidateTemplateRequest, _options?: PromiseConfigurationOptions): Promise<HttpInfo<ValidateTemplateResponse>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.validateTemplateWithHttpInfo(validateTemplateRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Dry-run template source without persisting: reports per-part parse errors, a rendered preview (against test_data when provided), and suggested_data — a placeholder value for every variable the source references. Beta: templates are unstable — their shape may change before they are declared stable.
     * Validate template source (beta)
     * @param validateTemplateRequest
     */
    public validateTemplate(validateTemplateRequest: ValidateTemplateRequest, _options?: PromiseConfigurationOptions): Promise<ValidateTemplateResponse> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.validateTemplate(validateTemplateRequest, observableOptions);
        return result.toPromise();
    }


}



import { ObservableWebhooksApi } from './ObservableAPI.js';

import { WebhooksApiRequestFactory, WebhooksApiResponseProcessor} from "../apis/WebhooksApi.js";
export class PromiseWebhooksApi {
    private api: ObservableWebhooksApi

    public constructor(
        configuration: Configuration,
        requestFactory?: WebhooksApiRequestFactory,
        responseProcessor?: WebhooksApiResponseProcessor
    ) {
        this.api = new ObservableWebhooksApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Create a webhook
     * @param createWebhookRequest
     */
    public createWebhookWithHttpInfo(createWebhookRequest: CreateWebhookRequest, _options?: PromiseConfigurationOptions): Promise<HttpInfo<CreateWebhookResponse>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.createWebhookWithHttpInfo(createWebhookRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Create a webhook
     * @param createWebhookRequest
     */
    public createWebhook(createWebhookRequest: CreateWebhookRequest, _options?: PromiseConfigurationOptions): Promise<CreateWebhookResponse> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.createWebhook(createWebhookRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Delete a webhook subscriber by id. Requires ?confirm=DELETE.
     * Delete a webhook
     * @param id
     * @param confirm Must be the literal DELETE — this action is irreversible.
     */
    public deleteWebhookWithHttpInfo(id: string, confirm: 'DELETE', _options?: PromiseConfigurationOptions): Promise<HttpInfo<void>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.deleteWebhookWithHttpInfo(id, confirm, observableOptions);
        return result.toPromise();
    }

    /**
     * Delete a webhook subscriber by id. Requires ?confirm=DELETE.
     * Delete a webhook
     * @param id
     * @param confirm Must be the literal DELETE — this action is irreversible.
     */
    public deleteWebhook(id: string, confirm: 'DELETE', _options?: PromiseConfigurationOptions): Promise<void> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.deleteWebhook(id, confirm, observableOptions);
        return result.toPromise();
    }

    /**
     * Get a webhook
     * @param id
     */
    public getWebhookWithHttpInfo(id: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<WebhookView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getWebhookWithHttpInfo(id, observableOptions);
        return result.toPromise();
    }

    /**
     * Get a webhook
     * @param id
     */
    public getWebhook(id: string, _options?: PromiseConfigurationOptions): Promise<WebhookView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.getWebhook(id, observableOptions);
        return result.toPromise();
    }

    /**
     * The per-webhook delivery log (read-only debug view).
     * List webhook deliveries
     * @param id
     * @param [status]
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the status filter.
     * @param [limit]
     */
    public listWebhookDeliveriesWithHttpInfo(id: string, status?: 'pending' | 'delivered' | 'failed', cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<HttpInfo<PageWebhookDeliveryView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listWebhookDeliveriesWithHttpInfo(id, status, cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * The per-webhook delivery log (read-only debug view).
     * List webhook deliveries
     * @param id
     * @param [status]
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the status filter.
     * @param [limit]
     */
    public listWebhookDeliveries(id: string, status?: 'pending' | 'delivered' | 'failed', cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<PageWebhookDeliveryView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listWebhookDeliveries(id, status, cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * List the webhooks owned by the authenticated account, newest first, with cursor pagination.
     * List webhooks
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listWebhooksWithHttpInfo(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<HttpInfo<PageWebhookView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listWebhooksWithHttpInfo(cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * List the webhooks owned by the authenticated account, newest first, with cursor pagination.
     * List webhooks
     * @param [cursor] Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param [limit] Maximum number of items to return (1-100).
     */
    public listWebhooks(cursor?: string, limit?: number, _options?: PromiseConfigurationOptions): Promise<PageWebhookView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.listWebhooks(cursor, limit, observableOptions);
        return result.toPromise();
    }

    /**
     * Mint a new signing secret; the previous one stays valid for a 24h grace window. Returns the new secret (shown once). Honors Idempotency-Key so a retried rotate replays the same secret instead of rotating twice.
     * Rotate a webhook signing secret
     * @param id
     * @param [idempotencyKey]
     */
    public rotateWebhookSecretWithHttpInfo(id: string, idempotencyKey?: string, _options?: PromiseConfigurationOptions): Promise<HttpInfo<RotateSecretResponse>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.rotateWebhookSecretWithHttpInfo(id, idempotencyKey, observableOptions);
        return result.toPromise();
    }

    /**
     * Mint a new signing secret; the previous one stays valid for a 24h grace window. Returns the new secret (shown once). Honors Idempotency-Key so a retried rotate replays the same secret instead of rotating twice.
     * Rotate a webhook signing secret
     * @param id
     * @param [idempotencyKey]
     */
    public rotateWebhookSecret(id: string, idempotencyKey?: string, _options?: PromiseConfigurationOptions): Promise<RotateSecretResponse> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.rotateWebhookSecret(id, idempotencyKey, observableOptions);
        return result.toPromise();
    }

    /**
     * Schedule a one-off synthetic delivery to this webhook for development. Returns the delivery id.
     * Fire a synthetic event
     * @param id
     * @param testWebhookRequest
     */
    public testWebhookWithHttpInfo(id: string, testWebhookRequest: TestWebhookRequest, _options?: PromiseConfigurationOptions): Promise<HttpInfo<TestWebhookResponse>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.testWebhookWithHttpInfo(id, testWebhookRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Schedule a one-off synthetic delivery to this webhook for development. Returns the delivery id.
     * Fire a synthetic event
     * @param id
     * @param testWebhookRequest
     */
    public testWebhook(id: string, testWebhookRequest: TestWebhookRequest, _options?: PromiseConfigurationOptions): Promise<TestWebhookResponse> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.testWebhook(id, testWebhookRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Partial update. url/events/filters are full-replace when present. Re-enabling within the auto-disable cooldown returns 409.
     * Update a webhook
     * @param id
     * @param updateWebhookRequest
     */
    public updateWebhookWithHttpInfo(id: string, updateWebhookRequest: UpdateWebhookRequest, _options?: PromiseConfigurationOptions): Promise<HttpInfo<WebhookView>> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.updateWebhookWithHttpInfo(id, updateWebhookRequest, observableOptions);
        return result.toPromise();
    }

    /**
     * Partial update. url/events/filters are full-replace when present. Re-enabling within the auto-disable cooldown returns 409.
     * Update a webhook
     * @param id
     * @param updateWebhookRequest
     */
    public updateWebhook(id: string, updateWebhookRequest: UpdateWebhookRequest, _options?: PromiseConfigurationOptions): Promise<WebhookView> {
        const observableOptions = wrapOptions(_options);
        const result = this.api.updateWebhook(id, updateWebhookRequest, observableOptions);
        return result.toPromise();
    }


}



