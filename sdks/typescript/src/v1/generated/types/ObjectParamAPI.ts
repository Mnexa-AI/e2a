import { ResponseContext, RequestContext, HttpFile, HttpInfo } from '../http/http.js';
import { Configuration, ConfigurationOptions } from '../configuration.js'
import type { Middleware } from '../middleware.js';

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
import { CreateWebhookRequest } from '../models/CreateWebhookRequest.js';
import { CreateWebhookResponse } from '../models/CreateWebhookResponse.js';
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
import { PageSuppression } from '../models/PageSuppression.js';
import { PageWebhookDeliveryView } from '../models/PageWebhookDeliveryView.js';
import { PageWebhookView } from '../models/PageWebhookView.js';
import { ProtectionConfigView } from '../models/ProtectionConfigView.js';
import { ProtectionDirectionView } from '../models/ProtectionDirectionView.js';
import { ProtectionEventExportEntry } from '../models/ProtectionEventExportEntry.js';
import { ProtectionGateView } from '../models/ProtectionGateView.js';
import { ProtectionHoldsView } from '../models/ProtectionHoldsView.js';
import { ProtectionScanView } from '../models/ProtectionScanView.js';
import { RedeliverDelivery } from '../models/RedeliverDelivery.js';
import { RedeliverEventRequest } from '../models/RedeliverEventRequest.js';
import { RedeliverView } from '../models/RedeliverView.js';
import { RegisterDomainRequest } from '../models/RegisterDomainRequest.js';
import { RejectRequest } from '../models/RejectRequest.js';
import { RejectResultView } from '../models/RejectResultView.js';
import { ReplyRequest } from '../models/ReplyRequest.js';
import { Result } from '../models/Result.js';
import { RotateSecretResponse } from '../models/RotateSecretResponse.js';
import { SendEmailRequest } from '../models/SendEmailRequest.js';
import { SendResultView } from '../models/SendResultView.js';
import { SendingDNSRecordView } from '../models/SendingDNSRecordView.js';
import { Suppression } from '../models/Suppression.js';
import { SuppressionExportEntry } from '../models/SuppressionExportEntry.js';
import { TestWebhookRequest } from '../models/TestWebhookRequest.js';
import { TestWebhookResponse } from '../models/TestWebhookResponse.js';
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

import { ObservableAccountApi } from "./ObservableAPI.js";
import { AccountApiRequestFactory, AccountApiResponseProcessor} from "../apis/AccountApi.js";

export interface AccountApiCreateApiKeyRequest {
    /**
     * 
     * @type CreateAPIKeyRequest
     * @memberof AccountApicreateApiKey
     */
    createAPIKeyRequest: CreateAPIKeyRequest
}

export interface AccountApiDeleteAccountRequest {
    /**
     * Must be DELETE — this is irreversible.
     * Defaults to: undefined
     * @type string
     * @memberof AccountApideleteAccount
     */
    confirm?: string
}

export interface AccountApiDeleteApiKeyRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof AccountApideleteApiKey
     */
    id: string
}

export interface AccountApiDeleteSuppressionRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof AccountApideleteSuppression
     */
    address: string
}

export interface AccountApiExportAccountRequest {
}

export interface AccountApiGetAccountRequest {
}

export interface AccountApiListApiKeysRequest {
}

export interface AccountApiListSuppressionsRequest {
    /**
     * Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * Defaults to: undefined
     * @type string
     * @memberof AccountApilistSuppressions
     */
    cursor?: string
    /**
     * Maximum number of items to return (1-100).
     * Minimum: 1
     * Maximum: 100
     * Defaults to: 50
     * @type number
     * @memberof AccountApilistSuppressions
     */
    limit?: number
}

export class ObjectAccountApi {
    private api: ObservableAccountApi

    public constructor(configuration: Configuration, requestFactory?: AccountApiRequestFactory, responseProcessor?: AccountApiResponseProcessor) {
        this.api = new ObservableAccountApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Mint a new API key; the plaintext key is returned once. scope=account is workspace admin (agent/domain/key management); scope=agent binds the key to one inbox so it can act only as that agent. Account scope only.
     * Create an API key
     * @param param the request object
     */
    public createApiKeyWithHttpInfo(param: AccountApiCreateApiKeyRequest, options?: ConfigurationOptions): Promise<HttpInfo<CreateAPIKeyResponse>> {
        return this.api.createApiKeyWithHttpInfo(param.createAPIKeyRequest,  options).toPromise();
    }

    /**
     * Mint a new API key; the plaintext key is returned once. scope=account is workspace admin (agent/domain/key management); scope=agent binds the key to one inbox so it can act only as that agent. Account scope only.
     * Create an API key
     * @param param the request object
     */
    public createApiKey(param: AccountApiCreateApiKeyRequest, options?: ConfigurationOptions): Promise<CreateAPIKeyResponse> {
        return this.api.createApiKey(param.createAPIKeyRequest,  options).toPromise();
    }

    /**
     * Permanently deletes the account and cascades all owned data. Requires ?confirm=DELETE.
     * Delete your account + all data (irreversible)
     * @param param the request object
     */
    public deleteAccountWithHttpInfo(param: AccountApiDeleteAccountRequest = {}, options?: ConfigurationOptions): Promise<HttpInfo<DeleteUserDataResult>> {
        return this.api.deleteAccountWithHttpInfo(param.confirm,  options).toPromise();
    }

    /**
     * Permanently deletes the account and cascades all owned data. Requires ?confirm=DELETE.
     * Delete your account + all data (irreversible)
     * @param param the request object
     */
    public deleteAccount(param: AccountApiDeleteAccountRequest = {}, options?: ConfigurationOptions): Promise<DeleteUserDataResult> {
        return this.api.deleteAccount(param.confirm,  options).toPromise();
    }

    /**
     * Revoke a key by id. Integrations using it stop authenticating immediately. Account scope only.
     * Revoke an API key
     * @param param the request object
     */
    public deleteApiKeyWithHttpInfo(param: AccountApiDeleteApiKeyRequest, options?: ConfigurationOptions): Promise<HttpInfo<void>> {
        return this.api.deleteApiKeyWithHttpInfo(param.id,  options).toPromise();
    }

    /**
     * Revoke a key by id. Integrations using it stop authenticating immediately. Account scope only.
     * Revoke an API key
     * @param param the request object
     */
    public deleteApiKey(param: AccountApiDeleteApiKeyRequest, options?: ConfigurationOptions): Promise<void> {
        return this.api.deleteApiKey(param.id,  options).toPromise();
    }

    /**
     * Un-suppress a recipient. A previously-blocked send to it then succeeds (idempotency keys are released, so no fresh key is needed).
     * Remove an address from the suppression list
     * @param param the request object
     */
    public deleteSuppressionWithHttpInfo(param: AccountApiDeleteSuppressionRequest, options?: ConfigurationOptions): Promise<HttpInfo<void>> {
        return this.api.deleteSuppressionWithHttpInfo(param.address,  options).toPromise();
    }

    /**
     * Un-suppress a recipient. A previously-blocked send to it then succeeds (idempotency keys are released, so no fresh key is needed).
     * Remove an address from the suppression list
     * @param param the request object
     */
    public deleteSuppression(param: AccountApiDeleteSuppressionRequest, options?: ConfigurationOptions): Promise<void> {
        return this.api.deleteSuppression(param.address,  options).toPromise();
    }

    /**
     * A JSON dump of every record the authenticated account owns.
     * Export your data (GDPR right-of-access)
     * @param param the request object
     */
    public exportAccountWithHttpInfo(param: AccountApiExportAccountRequest = {}, options?: ConfigurationOptions): Promise<HttpInfo<UserExport>> {
        return this.api.exportAccountWithHttpInfo( options).toPromise();
    }

    /**
     * A JSON dump of every record the authenticated account owns.
     * Export your data (GDPR right-of-access)
     * @param param the request object
     */
    public exportAccount(param: AccountApiExportAccountRequest = {}, options?: ConfigurationOptions): Promise<UserExport> {
        return this.api.exportAccount( options).toPromise();
    }

    /**
     * The authenticated principal\'s identity (user + scope; agent_address for agent-scoped credentials), plan caps, and current usage. Works for both account- and agent-scoped credentials. (Deployment discovery — shared domain, slug registration — is the separate public GET /v1/info.)
     * Get account: identity + plan limits + usage (whoami)
     * @param param the request object
     */
    public getAccountWithHttpInfo(param: AccountApiGetAccountRequest = {}, options?: ConfigurationOptions): Promise<HttpInfo<AccountView>> {
        return this.api.getAccountWithHttpInfo( options).toPromise();
    }

    /**
     * The authenticated principal\'s identity (user + scope; agent_address for agent-scoped credentials), plan caps, and current usage. Works for both account- and agent-scoped credentials. (Deployment discovery — shared domain, slug registration — is the separate public GET /v1/info.)
     * Get account: identity + plan limits + usage (whoami)
     * @param param the request object
     */
    public getAccount(param: AccountApiGetAccountRequest = {}, options?: ConfigurationOptions): Promise<AccountView> {
        return this.api.getAccount( options).toPromise();
    }

    /**
     * API keys for the account (metadata only — secrets are shown once, at creation). Account scope only: an agent-scoped credential cannot manage keys.
     * List API keys
     * @param param the request object
     */
    public listApiKeysWithHttpInfo(param: AccountApiListApiKeysRequest = {}, options?: ConfigurationOptions): Promise<HttpInfo<PageAPIKeyView>> {
        return this.api.listApiKeysWithHttpInfo( options).toPromise();
    }

    /**
     * API keys for the account (metadata only — secrets are shown once, at creation). Account scope only: an agent-scoped credential cannot manage keys.
     * List API keys
     * @param param the request object
     */
    public listApiKeys(param: AccountApiListApiKeysRequest = {}, options?: ConfigurationOptions): Promise<PageAPIKeyView> {
        return this.api.listApiKeys( options).toPromise();
    }

    /**
     * Addresses e2a will refuse to send to (auto-added on a hard bounce or complaint, or added manually). Sends to a suppressed address fail with recipient_suppressed.
     * List suppressed recipient addresses
     * @param param the request object
     */
    public listSuppressionsWithHttpInfo(param: AccountApiListSuppressionsRequest = {}, options?: ConfigurationOptions): Promise<HttpInfo<PageSuppression>> {
        return this.api.listSuppressionsWithHttpInfo(param.cursor, param.limit,  options).toPromise();
    }

    /**
     * Addresses e2a will refuse to send to (auto-added on a hard bounce or complaint, or added manually). Sends to a suppressed address fail with recipient_suppressed.
     * List suppressed recipient addresses
     * @param param the request object
     */
    public listSuppressions(param: AccountApiListSuppressionsRequest = {}, options?: ConfigurationOptions): Promise<PageSuppression> {
        return this.api.listSuppressions(param.cursor, param.limit,  options).toPromise();
    }

}

import { ObservableAgentsApi } from "./ObservableAPI.js";
import { AgentsApiRequestFactory, AgentsApiResponseProcessor} from "../apis/AgentsApi.js";

export interface AgentsApiCreateAgentRequest {
    /**
     * 
     * @type CreateAgentRequest
     * @memberof AgentsApicreateAgent
     */
    createAgentRequest: CreateAgentRequest
}

export interface AgentsApiDeleteAgentRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof AgentsApideleteAgent
     */
    email: string
    /**
     * Must be DELETE — this is irreversible.
     * Defaults to: undefined
     * @type string
     * @memberof AgentsApideleteAgent
     */
    confirm?: string
}

export interface AgentsApiGetAgentRequest {
    /**
     * The agent\&#39;s full email address, e.g. support@acme.com.
     * Defaults to: undefined
     * @type string
     * @memberof AgentsApigetAgent
     */
    email: string
}

export interface AgentsApiGetAgentProtectionRequest {
    /**
     * The agent\&#39;s full email address.
     * Defaults to: undefined
     * @type string
     * @memberof AgentsApigetAgentProtection
     */
    email: string
}

export interface AgentsApiListAgentsRequest {
}

export interface AgentsApiPutAgentProtectionRequest {
    /**
     * The agent\&#39;s full email address.
     * Defaults to: undefined
     * @type string
     * @memberof AgentsApiputAgentProtection
     */
    email: string
    /**
     * 
     * @type ProtectionConfigView
     * @memberof AgentsApiputAgentProtection
     */
    protectionConfigView: ProtectionConfigView
}

export interface AgentsApiTestAgentRequest {
    /**
     * The agent\&#39;s full email address, e.g. support@acme.com.
     * Defaults to: undefined
     * @type string
     * @memberof AgentsApitestAgent
     */
    email: string
}

export interface AgentsApiUpdateAgentRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof AgentsApiupdateAgent
     */
    email: string
    /**
     * 
     * @type UpdateAgentRequest
     * @memberof AgentsApiupdateAgent
     */
    updateAgentRequest: UpdateAgentRequest
}

export class ObjectAgentsApi {
    private api: ObservableAgentsApi

    public constructor(configuration: Configuration, requestFactory?: AgentsApiRequestFactory, responseProcessor?: AgentsApiResponseProcessor) {
        this.api = new ObservableAgentsApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Register an agent by full email. A custom-domain agent\'s domain must be a verified domain the caller owns; an email on the deployment\'s shared domain (e.g. xyz@agents.e2a.dev) is registered as a shared-domain agent. Returns the full agent.
     * Create an agent
     * @param param the request object
     */
    public createAgentWithHttpInfo(param: AgentsApiCreateAgentRequest, options?: ConfigurationOptions): Promise<HttpInfo<AgentView>> {
        return this.api.createAgentWithHttpInfo(param.createAgentRequest,  options).toPromise();
    }

    /**
     * Register an agent by full email. A custom-domain agent\'s domain must be a verified domain the caller owns; an email on the deployment\'s shared domain (e.g. xyz@agents.e2a.dev) is registered as a shared-domain agent. Returns the full agent.
     * Create an agent
     * @param param the request object
     */
    public createAgent(param: AgentsApiCreateAgentRequest, options?: ConfigurationOptions): Promise<AgentView> {
        return this.api.createAgent(param.createAgentRequest,  options).toPromise();
    }

    /**
     * Delete an agent the caller owns.
     * Delete an agent
     * @param param the request object
     */
    public deleteAgentWithHttpInfo(param: AgentsApiDeleteAgentRequest, options?: ConfigurationOptions): Promise<HttpInfo<void>> {
        return this.api.deleteAgentWithHttpInfo(param.email, param.confirm,  options).toPromise();
    }

    /**
     * Delete an agent the caller owns.
     * Delete an agent
     * @param param the request object
     */
    public deleteAgent(param: AgentsApiDeleteAgentRequest, options?: ConfigurationOptions): Promise<void> {
        return this.api.deleteAgent(param.email, param.confirm,  options).toPromise();
    }

    /**
     * Fetch a single agent the authenticated account owns, by full email address.
     * Get an agent
     * @param param the request object
     */
    public getAgentWithHttpInfo(param: AgentsApiGetAgentRequest, options?: ConfigurationOptions): Promise<HttpInfo<AgentView>> {
        return this.api.getAgentWithHttpInfo(param.email,  options).toPromise();
    }

    /**
     * Fetch a single agent the authenticated account owns, by full email address.
     * Get an agent
     * @param param the request object
     */
    public getAgent(param: AgentsApiGetAgentRequest, options?: ConfigurationOptions): Promise<AgentView> {
        return this.api.getAgent(param.email,  options).toPromise();
    }

    /**
     * Read the agent\'s protection posture — inbound/outbound trust gate, content-scan sensitivity, and hold-queue mechanism. Account scope only: an agent-scoped credential cannot read its own protection config. Beta: the agent protection config is unstable — its shape may change before it is declared stable.
     * Get an agent\'s protection config (beta)
     * @param param the request object
     */
    public getAgentProtectionWithHttpInfo(param: AgentsApiGetAgentProtectionRequest, options?: ConfigurationOptions): Promise<HttpInfo<ProtectionConfigView>> {
        return this.api.getAgentProtectionWithHttpInfo(param.email,  options).toPromise();
    }

    /**
     * Read the agent\'s protection posture — inbound/outbound trust gate, content-scan sensitivity, and hold-queue mechanism. Account scope only: an agent-scoped credential cannot read its own protection config. Beta: the agent protection config is unstable — its shape may change before it is declared stable.
     * Get an agent\'s protection config (beta)
     * @param param the request object
     */
    public getAgentProtection(param: AgentsApiGetAgentProtectionRequest, options?: ConfigurationOptions): Promise<ProtectionConfigView> {
        return this.api.getAgentProtection(param.email,  options).toPromise();
    }

    /**
     * List the agents owned by the authenticated account.
     * List agents
     * @param param the request object
     */
    public listAgentsWithHttpInfo(param: AgentsApiListAgentsRequest = {}, options?: ConfigurationOptions): Promise<HttpInfo<PageAgentView>> {
        return this.api.listAgentsWithHttpInfo( options).toPromise();
    }

    /**
     * List the agents owned by the authenticated account.
     * List agents
     * @param param the request object
     */
    public listAgents(param: AgentsApiListAgentsRequest = {}, options?: ConfigurationOptions): Promise<PageAgentView> {
        return this.api.listAgents( options).toPromise();
    }

    /**
     * Replace the agent\'s protection posture wholesale. The three top-level keys (inbound, outbound, holds) are required; leaves default. Account scope only. Beta: the agent protection config is unstable — its shape may change before it is declared stable.
     * Replace an agent\'s protection config (beta)
     * @param param the request object
     */
    public putAgentProtectionWithHttpInfo(param: AgentsApiPutAgentProtectionRequest, options?: ConfigurationOptions): Promise<HttpInfo<ProtectionConfigView>> {
        return this.api.putAgentProtectionWithHttpInfo(param.email, param.protectionConfigView,  options).toPromise();
    }

    /**
     * Replace the agent\'s protection posture wholesale. The three top-level keys (inbound, outbound, holds) are required; leaves default. Account scope only. Beta: the agent protection config is unstable — its shape may change before it is declared stable.
     * Replace an agent\'s protection config (beta)
     * @param param the request object
     */
    public putAgentProtection(param: AgentsApiPutAgentProtectionRequest, options?: ConfigurationOptions): Promise<ProtectionConfigView> {
        return this.api.putAgentProtection(param.email, param.protectionConfigView,  options).toPromise();
    }

    /**
     * Send a platform test email to the agent\'s own address to confirm inbound delivery. 202 when held for HITL.
     * Send a test email to the agent\'s own address
     * @param param the request object
     */
    public testAgentWithHttpInfo(param: AgentsApiTestAgentRequest, options?: ConfigurationOptions): Promise<HttpInfo<SendResultView>> {
        return this.api.testAgentWithHttpInfo(param.email,  options).toPromise();
    }

    /**
     * Send a platform test email to the agent\'s own address to confirm inbound delivery. 202 when held for HITL.
     * Send a test email to the agent\'s own address
     * @param param the request object
     */
    public testAgent(param: AgentsApiTestAgentRequest, options?: ConfigurationOptions): Promise<SendResultView> {
        return this.api.testAgent(param.email,  options).toPromise();
    }

    /**
     * Update an agent\'s display name. The screening/protection config lives on the /v1/agents/{email}/protection sub-resource. Returns the post-update agent.
     * Update an agent
     * @param param the request object
     */
    public updateAgentWithHttpInfo(param: AgentsApiUpdateAgentRequest, options?: ConfigurationOptions): Promise<HttpInfo<AgentView>> {
        return this.api.updateAgentWithHttpInfo(param.email, param.updateAgentRequest,  options).toPromise();
    }

    /**
     * Update an agent\'s display name. The screening/protection config lives on the /v1/agents/{email}/protection sub-resource. Returns the post-update agent.
     * Update an agent
     * @param param the request object
     */
    public updateAgent(param: AgentsApiUpdateAgentRequest, options?: ConfigurationOptions): Promise<AgentView> {
        return this.api.updateAgent(param.email, param.updateAgentRequest,  options).toPromise();
    }

}

import { ObservableConversationsApi } from "./ObservableAPI.js";
import { ConversationsApiRequestFactory, ConversationsApiResponseProcessor} from "../apis/ConversationsApi.js";

export interface ConversationsApiGetConversationRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof ConversationsApigetConversation
     */
    email: string
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof ConversationsApigetConversation
     */
    id: string
}

export interface ConversationsApiListConversationsRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof ConversationsApilistConversations
     */
    email: string
    /**
     * RFC3339.
     * Defaults to: undefined
     * @type string
     * @memberof ConversationsApilistConversations
     */
    since?: string
    /**
     * RFC3339.
     * Defaults to: undefined
     * @type string
     * @memberof ConversationsApilistConversations
     */
    until?: string
    /**
     * Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change since/until.
     * Defaults to: undefined
     * @type string
     * @memberof ConversationsApilistConversations
     */
    cursor?: string
    /**
     * 
     * Minimum: 1
     * Maximum: 100
     * Defaults to: 100
     * @type number
     * @memberof ConversationsApilistConversations
     */
    limit?: number
}

export class ObjectConversationsApi {
    private api: ObservableConversationsApi

    public constructor(configuration: Configuration, requestFactory?: ConversationsApiRequestFactory, responseProcessor?: ConversationsApiResponseProcessor) {
        this.api = new ObservableConversationsApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Fetch a single conversation thread with its participants, labels, and member messages.
     * Get a conversation
     * @param param the request object
     */
    public getConversationWithHttpInfo(param: ConversationsApiGetConversationRequest, options?: ConfigurationOptions): Promise<HttpInfo<ConversationDetailView>> {
        return this.api.getConversationWithHttpInfo(param.email, param.id,  options).toPromise();
    }

    /**
     * Fetch a single conversation thread with its participants, labels, and member messages.
     * Get a conversation
     * @param param the request object
     */
    public getConversation(param: ConversationsApiGetConversationRequest, options?: ConfigurationOptions): Promise<ConversationDetailView> {
        return this.api.getConversation(param.email, param.id,  options).toPromise();
    }

    /**
     * List an agent\'s conversation threads (derived from messages.conversation_id).
     * List conversations
     * @param param the request object
     */
    public listConversationsWithHttpInfo(param: ConversationsApiListConversationsRequest, options?: ConfigurationOptions): Promise<HttpInfo<PageConversationSummaryView>> {
        return this.api.listConversationsWithHttpInfo(param.email, param.since, param.until, param.cursor, param.limit,  options).toPromise();
    }

    /**
     * List an agent\'s conversation threads (derived from messages.conversation_id).
     * List conversations
     * @param param the request object
     */
    public listConversations(param: ConversationsApiListConversationsRequest, options?: ConfigurationOptions): Promise<PageConversationSummaryView> {
        return this.api.listConversations(param.email, param.since, param.until, param.cursor, param.limit,  options).toPromise();
    }

}

import { ObservableDomainsApi } from "./ObservableAPI.js";
import { DomainsApiRequestFactory, DomainsApiResponseProcessor} from "../apis/DomainsApi.js";

export interface DomainsApiDeleteDomainRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof DomainsApideleteDomain
     */
    domain: string
    /**
     * Must be DELETE — this is irreversible (deprovisions the domain\&#39;s sending identity).
     * Defaults to: undefined
     * @type string
     * @memberof DomainsApideleteDomain
     */
    confirm?: string
}

export interface DomainsApiGetDomainRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof DomainsApigetDomain
     */
    domain: string
}

export interface DomainsApiListDomainsRequest {
}

export interface DomainsApiRegisterDomainRequest {
    /**
     * 
     * @type RegisterDomainRequest
     * @memberof DomainsApiregisterDomain
     */
    registerDomainRequest: RegisterDomainRequest
}

export interface DomainsApiUpdateDomainRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof DomainsApiupdateDomain
     */
    domain: string
    /**
     * 
     * @type UpdateDomainRequest
     * @memberof DomainsApiupdateDomain
     */
    updateDomainRequest: UpdateDomainRequest
}

export interface DomainsApiVerifyDomainRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof DomainsApiverifyDomain
     */
    domain: string
}

export class ObjectDomainsApi {
    private api: ObservableDomainsApi

    public constructor(configuration: Configuration, requestFactory?: DomainsApiRequestFactory, responseProcessor?: DomainsApiResponseProcessor) {
        this.api = new ObservableDomainsApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Delete a domain
     * @param param the request object
     */
    public deleteDomainWithHttpInfo(param: DomainsApiDeleteDomainRequest, options?: ConfigurationOptions): Promise<HttpInfo<void>> {
        return this.api.deleteDomainWithHttpInfo(param.domain, param.confirm,  options).toPromise();
    }

    /**
     * Delete a domain
     * @param param the request object
     */
    public deleteDomain(param: DomainsApiDeleteDomainRequest, options?: ConfigurationOptions): Promise<void> {
        return this.api.deleteDomain(param.domain, param.confirm,  options).toPromise();
    }

    /**
     * Get a domain
     * @param param the request object
     */
    public getDomainWithHttpInfo(param: DomainsApiGetDomainRequest, options?: ConfigurationOptions): Promise<HttpInfo<DomainView>> {
        return this.api.getDomainWithHttpInfo(param.domain,  options).toPromise();
    }

    /**
     * Get a domain
     * @param param the request object
     */
    public getDomain(param: DomainsApiGetDomainRequest, options?: ConfigurationOptions): Promise<DomainView> {
        return this.api.getDomain(param.domain,  options).toPromise();
    }

    /**
     * List domains
     * @param param the request object
     */
    public listDomainsWithHttpInfo(param: DomainsApiListDomainsRequest = {}, options?: ConfigurationOptions): Promise<HttpInfo<PageDomainView>> {
        return this.api.listDomainsWithHttpInfo( options).toPromise();
    }

    /**
     * List domains
     * @param param the request object
     */
    public listDomains(param: DomainsApiListDomainsRequest = {}, options?: ConfigurationOptions): Promise<PageDomainView> {
        return this.api.listDomains( options).toPromise();
    }

    /**
     * Register a domain
     * @param param the request object
     */
    public registerDomainWithHttpInfo(param: DomainsApiRegisterDomainRequest, options?: ConfigurationOptions): Promise<HttpInfo<DomainView>> {
        return this.api.registerDomainWithHttpInfo(param.registerDomainRequest,  options).toPromise();
    }

    /**
     * Register a domain
     * @param param the request object
     */
    public registerDomain(param: DomainsApiRegisterDomainRequest, options?: ConfigurationOptions): Promise<DomainView> {
        return this.api.registerDomain(param.registerDomainRequest,  options).toPromise();
    }

    /**
     * Update a domain (set primary)
     * @param param the request object
     */
    public updateDomainWithHttpInfo(param: DomainsApiUpdateDomainRequest, options?: ConfigurationOptions): Promise<HttpInfo<DomainView>> {
        return this.api.updateDomainWithHttpInfo(param.domain, param.updateDomainRequest,  options).toPromise();
    }

    /**
     * Update a domain (set primary)
     * @param param the request object
     */
    public updateDomain(param: DomainsApiUpdateDomainRequest, options?: ConfigurationOptions): Promise<DomainView> {
        return this.api.updateDomain(param.domain, param.updateDomainRequest,  options).toPromise();
    }

    /**
     * Probe the domain\'s published DNS and, when the verification TXT is present, mark it verified. Returns the per-record diagnostic; a missing TXT yields 412.
     * Verify a domain
     * @param param the request object
     */
    public verifyDomainWithHttpInfo(param: DomainsApiVerifyDomainRequest, options?: ConfigurationOptions): Promise<HttpInfo<VerifyDomainView>> {
        return this.api.verifyDomainWithHttpInfo(param.domain,  options).toPromise();
    }

    /**
     * Probe the domain\'s published DNS and, when the verification TXT is present, mark it verified. Returns the per-record diagnostic; a missing TXT yields 412.
     * Verify a domain
     * @param param the request object
     */
    public verifyDomain(param: DomainsApiVerifyDomainRequest, options?: ConfigurationOptions): Promise<VerifyDomainView> {
        return this.api.verifyDomain(param.domain,  options).toPromise();
    }

}

import { ObservableEventsApi } from "./ObservableAPI.js";
import { EventsApiRequestFactory, EventsApiResponseProcessor} from "../apis/EventsApi.js";

export interface EventsApiGetEventRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof EventsApigetEvent
     */
    id: string
}

export interface EventsApiListEventsRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof EventsApilistEvents
     */
    type?: string
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof EventsApilistEvents
     */
    agentId?: string
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof EventsApilistEvents
     */
    conversationId?: string
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof EventsApilistEvents
     */
    messageId?: string
    /**
     * RFC3339.
     * Defaults to: undefined
     * @type string
     * @memberof EventsApilistEvents
     */
    since?: string
    /**
     * RFC3339.
     * Defaults to: undefined
     * @type string
     * @memberof EventsApilistEvents
     */
    until?: string
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof EventsApilistEvents
     */
    cursor?: string
    /**
     * 
     * Minimum: 1
     * Maximum: 200
     * Defaults to: 50
     * @type number
     * @memberof EventsApilistEvents
     */
    limit?: number
}

export interface EventsApiRedeliverEventRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof EventsApiredeliverEvent
     */
    id: string
    /**
     * 
     * @type RedeliverEventRequest
     * @memberof EventsApiredeliverEvent
     */
    redeliverEventRequest: RedeliverEventRequest
}

export class ObjectEventsApi {
    private api: ObservableEventsApi

    public constructor(configuration: Configuration, requestFactory?: EventsApiRequestFactory, responseProcessor?: EventsApiResponseProcessor) {
        this.api = new ObservableEventsApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Get an event
     * @param param the request object
     */
    public getEventWithHttpInfo(param: EventsApiGetEventRequest, options?: ConfigurationOptions): Promise<HttpInfo<EventJSON>> {
        return this.api.getEventWithHttpInfo(param.id,  options).toPromise();
    }

    /**
     * Get an event
     * @param param the request object
     */
    public getEvent(param: EventsApiGetEventRequest, options?: ConfigurationOptions): Promise<EventJSON> {
        return this.api.getEvent(param.id,  options).toPromise();
    }

    /**
     * The webhook-event delivery log, filterable by type/agent/conversation/message and time range, with cursor pagination.
     * List events
     * @param param the request object
     */
    public listEventsWithHttpInfo(param: EventsApiListEventsRequest = {}, options?: ConfigurationOptions): Promise<HttpInfo<PageEventJSON>> {
        return this.api.listEventsWithHttpInfo(param.type, param.agentId, param.conversationId, param.messageId, param.since, param.until, param.cursor, param.limit,  options).toPromise();
    }

    /**
     * The webhook-event delivery log, filterable by type/agent/conversation/message and time range, with cursor pagination.
     * List events
     * @param param the request object
     */
    public listEvents(param: EventsApiListEventsRequest = {}, options?: ConfigurationOptions): Promise<PageEventJSON> {
        return this.api.listEvents(param.type, param.agentId, param.conversationId, param.messageId, param.since, param.until, param.cursor, param.limit,  options).toPromise();
    }

    /**
     * Re-enqueue webhook delivery for an event. With a webhook_id, replays to that subscriber; without, fans out to every originally-matched subscriber. Auto-deduplicated within a short window — receivers must dedup on event id.
     * Redeliver an event
     * @param param the request object
     */
    public redeliverEventWithHttpInfo(param: EventsApiRedeliverEventRequest, options?: ConfigurationOptions): Promise<HttpInfo<RedeliverView>> {
        return this.api.redeliverEventWithHttpInfo(param.id, param.redeliverEventRequest,  options).toPromise();
    }

    /**
     * Re-enqueue webhook delivery for an event. With a webhook_id, replays to that subscriber; without, fans out to every originally-matched subscriber. Auto-deduplicated within a short window — receivers must dedup on event id.
     * Redeliver an event
     * @param param the request object
     */
    public redeliverEvent(param: EventsApiRedeliverEventRequest, options?: ConfigurationOptions): Promise<RedeliverView> {
        return this.api.redeliverEvent(param.id, param.redeliverEventRequest,  options).toPromise();
    }

}

import { ObservableMessagesApi } from "./ObservableAPI.js";
import { MessagesApiRequestFactory, MessagesApiResponseProcessor} from "../apis/MessagesApi.js";

export interface MessagesApiApproveMessageRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApiapproveMessage
     */
    email: string
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApiapproveMessage
     */
    id: string
    /**
     * 
     * @type ApproveRequest
     * @memberof MessagesApiapproveMessage
     */
    approveRequest: ApproveRequest
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApiapproveMessage
     */
    idempotencyKey?: string
}

export interface MessagesApiForwardMessageRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApiforwardMessage
     */
    email: string
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApiforwardMessage
     */
    id: string
    /**
     * 
     * @type ForwardRequest
     * @memberof MessagesApiforwardMessage
     */
    forwardRequest: ForwardRequest
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApiforwardMessage
     */
    idempotencyKey?: string
}

export interface MessagesApiGetAttachmentRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApigetAttachment
     */
    email: string
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApigetAttachment
     */
    id: string
    /**
     * 
     * Minimum: 0
     * Defaults to: undefined
     * @type number
     * @memberof MessagesApigetAttachment
     */
    index: number
    /**
     * When true, also include the bytes as base64 in \&#39;data\&#39; — ONLY for attachments &lt;&#x3D; 256 KB; larger inline requests are rejected (413). Default false (use download_url).
     * Defaults to: undefined
     * @type boolean
     * @memberof MessagesApigetAttachment
     */
    inline?: boolean
}

export interface MessagesApiGetMessageRequest {
    /**
     * The agent\&#39;s full email address.
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApigetMessage
     */
    email: string
    /**
     * The message id, e.g. msg_abc123.
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApigetMessage
     */
    id: string
}

export interface MessagesApiListMessagesRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApilistMessages
     */
    email: string
    /**
     * Defaults to inbound.
     * Defaults to: undefined
     * @type &#39;inbound&#39; | &#39;outbound&#39; | &#39;all&#39;
     * @memberof MessagesApilistMessages
     */
    direction?: 'inbound' | 'outbound' | 'all'
    /**
     * Inbound only. Filters by inbox read-state (MSG-1). Defaults to unread for inbound, all otherwise.
     * Defaults to: undefined
     * @type &#39;unread&#39; | &#39;read&#39; | &#39;all&#39;
     * @memberof MessagesApilistMessages
     */
    readStatus?: 'unread' | 'read' | 'all'
    /**
     * Defaults to desc (newest first).
     * Defaults to: undefined
     * @type &#39;asc&#39; | &#39;desc&#39;
     * @memberof MessagesApilistMessages
     */
    sort?: 'asc' | 'desc'
    /**
     * Case-insensitive substring match on sender.
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApilistMessages
     */
    _from?: string
    /**
     * Case-insensitive substring match on subject.
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApilistMessages
     */
    subjectContains?: string
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApilistMessages
     */
    conversationId?: string
    /**
     * Repeatable; AND-matched.
     * Defaults to: undefined
     * @type Array&lt;string&gt;
     * @memberof MessagesApilistMessages
     */
    labels?: Array<string>
    /**
     * RFC3339; created_at &gt;&#x3D; since.
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApilistMessages
     */
    since?: string
    /**
     * RFC3339; created_at &lt; until.
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApilistMessages
     */
    until?: string
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApilistMessages
     */
    cursor?: string
    /**
     * 
     * Minimum: 1
     * Maximum: 100
     * Defaults to: 50
     * @type number
     * @memberof MessagesApilistMessages
     */
    limit?: number
}

export interface MessagesApiRejectMessageRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApirejectMessage
     */
    email: string
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApirejectMessage
     */
    id: string
    /**
     * 
     * @type RejectRequest
     * @memberof MessagesApirejectMessage
     */
    rejectRequest: RejectRequest
}

export interface MessagesApiReplyToMessageRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApireplyToMessage
     */
    email: string
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApireplyToMessage
     */
    id: string
    /**
     * 
     * @type ReplyRequest
     * @memberof MessagesApireplyToMessage
     */
    replyRequest: ReplyRequest
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApireplyToMessage
     */
    idempotencyKey?: string
}

export interface MessagesApiSendMessageRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApisendMessage
     */
    email: string
    /**
     * 
     * @type SendEmailRequest
     * @memberof MessagesApisendMessage
     */
    sendEmailRequest: SendEmailRequest
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApisendMessage
     */
    idempotencyKey?: string
}

export interface MessagesApiUpdateMessageRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApiupdateMessage
     */
    email: string
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof MessagesApiupdateMessage
     */
    id: string
    /**
     * 
     * @type UpdateMessageRequest
     * @memberof MessagesApiupdateMessage
     */
    updateMessageRequest: UpdateMessageRequest
}

export class ObjectMessagesApi {
    private api: ObservableMessagesApi

    public constructor(configuration: Configuration, requestFactory?: MessagesApiRequestFactory, responseProcessor?: MessagesApiResponseProcessor) {
        this.api = new ObservableMessagesApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Approve a message held in pending_review. The action branches on the message\'s direction: an **outbound** hold is sent via SES (honoring Idempotency-Key and optional reviewer overrides; the response carries the send result), and an **inbound** hold is released to the agent\'s inbox (it becomes readable; the response status is review_approved). Account-scoped credentials only — an agent-scoped credential cannot release its own hold (self-approval would defeat the review gate).
     * Approve a held message
     * @param param the request object
     */
    public approveMessageWithHttpInfo(param: MessagesApiApproveMessageRequest, options?: ConfigurationOptions): Promise<HttpInfo<SendResultView>> {
        return this.api.approveMessageWithHttpInfo(param.email, param.id, param.approveRequest, param.idempotencyKey,  options).toPromise();
    }

    /**
     * Approve a message held in pending_review. The action branches on the message\'s direction: an **outbound** hold is sent via SES (honoring Idempotency-Key and optional reviewer overrides; the response carries the send result), and an **inbound** hold is released to the agent\'s inbox (it becomes readable; the response status is review_approved). Account-scoped credentials only — an agent-scoped credential cannot release its own hold (self-approval would defeat the review gate).
     * Approve a held message
     * @param param the request object
     */
    public approveMessage(param: MessagesApiApproveMessageRequest, options?: ConfigurationOptions): Promise<SendResultView> {
        return this.api.approveMessage(param.email, param.id, param.approveRequest, param.idempotencyKey,  options).toPromise();
    }

    /**
     * Forward an inbound message to new recipients; the original is quoted. 202 when held for HITL.
     * Forward a message
     * @param param the request object
     */
    public forwardMessageWithHttpInfo(param: MessagesApiForwardMessageRequest, options?: ConfigurationOptions): Promise<HttpInfo<SendResultView>> {
        return this.api.forwardMessageWithHttpInfo(param.email, param.id, param.forwardRequest, param.idempotencyKey,  options).toPromise();
    }

    /**
     * Forward an inbound message to new recipients; the original is quoted. 202 when held for HITL.
     * Forward a message
     * @param param the request object
     */
    public forwardMessage(param: MessagesApiForwardMessageRequest, options?: ConfigurationOptions): Promise<SendResultView> {
        return this.api.forwardMessage(param.email, param.id, param.forwardRequest, param.idempotencyKey,  options).toPromise();
    }

    /**
     * Returns one attachment\'s metadata plus a short-lived `download_url` (+ `expires_at`) to fetch the bytes out of band — so binary content never streams through an agent\'s context. Pass `?inline=true` to also receive base64 `data` for small attachments (<= 256 KB); larger inline requests are rejected. `index` is the 0-based attachment index from the message\'s `attachments[]`.
     * Get an attachment (metadata + short-lived download URL)
     * @param param the request object
     */
    public getAttachmentWithHttpInfo(param: MessagesApiGetAttachmentRequest, options?: ConfigurationOptions): Promise<HttpInfo<AttachmentView>> {
        return this.api.getAttachmentWithHttpInfo(param.email, param.id, param.index, param.inline,  options).toPromise();
    }

    /**
     * Returns one attachment\'s metadata plus a short-lived `download_url` (+ `expires_at`) to fetch the bytes out of band — so binary content never streams through an agent\'s context. Pass `?inline=true` to also receive base64 `data` for small attachments (<= 256 KB); larger inline requests are rejected. `index` is the 0-based attachment index from the message\'s `attachments[]`.
     * Get an attachment (metadata + short-lived download URL)
     * @param param the request object
     */
    public getAttachment(param: MessagesApiGetAttachmentRequest, options?: ConfigurationOptions): Promise<AttachmentView> {
        return this.api.getAttachment(param.email, param.id, param.index, param.inline,  options).toPromise();
    }

    /**
     * Fetch a single message (inbound or outbound) by id, scoped to an agent the caller owns. Includes the raw message and inbound auth headers.
     * Get a message
     * @param param the request object
     */
    public getMessageWithHttpInfo(param: MessagesApiGetMessageRequest, options?: ConfigurationOptions): Promise<HttpInfo<MessageView>> {
        return this.api.getMessageWithHttpInfo(param.email, param.id,  options).toPromise();
    }

    /**
     * Fetch a single message (inbound or outbound) by id, scoped to an agent the caller owns. Includes the raw message and inbound auth headers.
     * Get a message
     * @param param the request object
     */
    public getMessage(param: MessagesApiGetMessageRequest, options?: ConfigurationOptions): Promise<MessageView> {
        return this.api.getMessage(param.email, param.id,  options).toPromise();
    }

    /**
     * List an agent\'s messages (inbound + outbound) with filters and cursor pagination. Held outbound drafts appear as status=pending_review.
     * List messages
     * @param param the request object
     */
    public listMessagesWithHttpInfo(param: MessagesApiListMessagesRequest, options?: ConfigurationOptions): Promise<HttpInfo<PageMessageSummaryView>> {
        return this.api.listMessagesWithHttpInfo(param.email, param.direction, param.readStatus, param.sort, param._from, param.subjectContains, param.conversationId, param.labels, param.since, param.until, param.cursor, param.limit,  options).toPromise();
    }

    /**
     * List an agent\'s messages (inbound + outbound) with filters and cursor pagination. Held outbound drafts appear as status=pending_review.
     * List messages
     * @param param the request object
     */
    public listMessages(param: MessagesApiListMessagesRequest, options?: ConfigurationOptions): Promise<PageMessageSummaryView> {
        return this.api.listMessages(param.email, param.direction, param.readStatus, param.sort, param._from, param.subjectContains, param.conversationId, param.labels, param.since, param.until, param.cursor, param.limit,  options).toPromise();
    }

    /**
     * Reject a message held in pending_review. An **outbound** hold is discarded so it is never sent; an **inbound** hold is dropped so it never reaches the agent (its raw payload is retained, hidden, for forensics). Account-scoped credentials only.
     * Reject a held message
     * @param param the request object
     */
    public rejectMessageWithHttpInfo(param: MessagesApiRejectMessageRequest, options?: ConfigurationOptions): Promise<HttpInfo<RejectResultView>> {
        return this.api.rejectMessageWithHttpInfo(param.email, param.id, param.rejectRequest,  options).toPromise();
    }

    /**
     * Reject a message held in pending_review. An **outbound** hold is discarded so it is never sent; an **inbound** hold is dropped so it never reaches the agent (its raw payload is retained, hidden, for forensics). Account-scoped credentials only.
     * Reject a held message
     * @param param the request object
     */
    public rejectMessage(param: MessagesApiRejectMessageRequest, options?: ConfigurationOptions): Promise<RejectResultView> {
        return this.api.rejectMessage(param.email, param.id, param.rejectRequest,  options).toPromise();
    }

    /**
     * Reply to an inbound message; recipients/threading are derived from the original. 202 when held for HITL.
     * Reply to a message
     * @param param the request object
     */
    public replyToMessageWithHttpInfo(param: MessagesApiReplyToMessageRequest, options?: ConfigurationOptions): Promise<HttpInfo<SendResultView>> {
        return this.api.replyToMessageWithHttpInfo(param.email, param.id, param.replyRequest, param.idempotencyKey,  options).toPromise();
    }

    /**
     * Reply to an inbound message; recipients/threading are derived from the original. 202 when held for HITL.
     * Reply to a message
     * @param param the request object
     */
    public replyToMessage(param: MessagesApiReplyToMessageRequest, options?: ConfigurationOptions): Promise<SendResultView> {
        return this.api.replyToMessage(param.email, param.id, param.replyRequest, param.idempotencyKey,  options).toPromise();
    }

    /**
     * Send a new email from the agent named in the path (a new thread). The sender is the path agent — `reply`/`forward` are their own sub-resources. 202 + pending_review when the agent has HITL enabled. Honors Idempotency-Key.
     * Send a new email
     * @param param the request object
     */
    public sendMessageWithHttpInfo(param: MessagesApiSendMessageRequest, options?: ConfigurationOptions): Promise<HttpInfo<SendResultView>> {
        return this.api.sendMessageWithHttpInfo(param.email, param.sendEmailRequest, param.idempotencyKey,  options).toPromise();
    }

    /**
     * Send a new email from the agent named in the path (a new thread). The sender is the path agent — `reply`/`forward` are their own sub-resources. 202 + pending_review when the agent has HITL enabled. Honors Idempotency-Key.
     * Send a new email
     * @param param the request object
     */
    public sendMessage(param: MessagesApiSendMessageRequest, options?: ConfigurationOptions): Promise<SendResultView> {
        return this.api.sendMessage(param.email, param.sendEmailRequest, param.idempotencyKey,  options).toPromise();
    }

    /**
     * Apply a labels delta (`add_labels` / `remove_labels`) to a message the caller owns; returns the post-update label set. Each list is capped at 50 entries; labels are lowercase `[a-z0-9:_-]+` up to 64 chars; the `e2a:` prefix is reserved for system labels. A message carries at most 100 labels. An empty delta is a read of the current labels.
     * Update a message (labels)
     * @param param the request object
     */
    public updateMessageWithHttpInfo(param: MessagesApiUpdateMessageRequest, options?: ConfigurationOptions): Promise<HttpInfo<UpdateMessageResultView>> {
        return this.api.updateMessageWithHttpInfo(param.email, param.id, param.updateMessageRequest,  options).toPromise();
    }

    /**
     * Apply a labels delta (`add_labels` / `remove_labels`) to a message the caller owns; returns the post-update label set. Each list is capped at 50 entries; labels are lowercase `[a-z0-9:_-]+` up to 64 chars; the `e2a:` prefix is reserved for system labels. A message carries at most 100 labels. An empty delta is a read of the current labels.
     * Update a message (labels)
     * @param param the request object
     */
    public updateMessage(param: MessagesApiUpdateMessageRequest, options?: ConfigurationOptions): Promise<UpdateMessageResultView> {
        return this.api.updateMessage(param.email, param.id, param.updateMessageRequest,  options).toPromise();
    }

}

import { ObservableMetaApi } from "./ObservableAPI.js";
import { MetaApiRequestFactory, MetaApiResponseProcessor} from "../apis/MetaApi.js";

export interface MetaApiGetInfoRequest {
}

export class ObjectMetaApi {
    private api: ObservableMetaApi

    public constructor(configuration: Configuration, requestFactory?: MetaApiRequestFactory, responseProcessor?: MetaApiResponseProcessor) {
        this.api = new ObservableMetaApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Public deployment metadata: the shared agent domain (if slug registration is enabled) and the public base URL. Unauthenticated.
     * Deployment info
     * @param param the request object
     */
    public getInfoWithHttpInfo(param: MetaApiGetInfoRequest = {}, options?: ConfigurationOptions): Promise<HttpInfo<DeploymentInfoView>> {
        return this.api.getInfoWithHttpInfo( options).toPromise();
    }

    /**
     * Public deployment metadata: the shared agent domain (if slug registration is enabled) and the public base URL. Unauthenticated.
     * Deployment info
     * @param param the request object
     */
    public getInfo(param: MetaApiGetInfoRequest = {}, options?: ConfigurationOptions): Promise<DeploymentInfoView> {
        return this.api.getInfo( options).toPromise();
    }

}

import { ObservableWebhooksApi } from "./ObservableAPI.js";
import { WebhooksApiRequestFactory, WebhooksApiResponseProcessor} from "../apis/WebhooksApi.js";

export interface WebhooksApiCreateWebhookRequest {
    /**
     * 
     * @type CreateWebhookRequest
     * @memberof WebhooksApicreateWebhook
     */
    createWebhookRequest: CreateWebhookRequest
}

export interface WebhooksApiDeleteWebhookRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof WebhooksApideleteWebhook
     */
    id: string
}

export interface WebhooksApiGetWebhookRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof WebhooksApigetWebhook
     */
    id: string
}

export interface WebhooksApiListWebhookDeliveriesRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof WebhooksApilistWebhookDeliveries
     */
    id: string
    /**
     * 
     * Defaults to: undefined
     * @type &#39;pending&#39; | &#39;delivered&#39; | &#39;failed&#39;
     * @memberof WebhooksApilistWebhookDeliveries
     */
    status?: 'pending' | 'delivered' | 'failed'
    /**
     * 
     * Minimum: 1
     * Maximum: 500
     * Defaults to: 100
     * @type number
     * @memberof WebhooksApilistWebhookDeliveries
     */
    limit?: number
}

export interface WebhooksApiListWebhooksRequest {
}

export interface WebhooksApiRotateWebhookSecretRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof WebhooksApirotateWebhookSecret
     */
    id: string
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof WebhooksApirotateWebhookSecret
     */
    idempotencyKey?: string
}

export interface WebhooksApiTestWebhookRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof WebhooksApitestWebhook
     */
    id: string
    /**
     * 
     * @type TestWebhookRequest
     * @memberof WebhooksApitestWebhook
     */
    testWebhookRequest: TestWebhookRequest
}

export interface WebhooksApiUpdateWebhookRequest {
    /**
     * 
     * Defaults to: undefined
     * @type string
     * @memberof WebhooksApiupdateWebhook
     */
    id: string
    /**
     * 
     * @type UpdateWebhookRequest
     * @memberof WebhooksApiupdateWebhook
     */
    updateWebhookRequest: UpdateWebhookRequest
}

export class ObjectWebhooksApi {
    private api: ObservableWebhooksApi

    public constructor(configuration: Configuration, requestFactory?: WebhooksApiRequestFactory, responseProcessor?: WebhooksApiResponseProcessor) {
        this.api = new ObservableWebhooksApi(configuration, requestFactory, responseProcessor);
    }

    /**
     * Create a webhook
     * @param param the request object
     */
    public createWebhookWithHttpInfo(param: WebhooksApiCreateWebhookRequest, options?: ConfigurationOptions): Promise<HttpInfo<CreateWebhookResponse>> {
        return this.api.createWebhookWithHttpInfo(param.createWebhookRequest,  options).toPromise();
    }

    /**
     * Create a webhook
     * @param param the request object
     */
    public createWebhook(param: WebhooksApiCreateWebhookRequest, options?: ConfigurationOptions): Promise<CreateWebhookResponse> {
        return this.api.createWebhook(param.createWebhookRequest,  options).toPromise();
    }

    /**
     * Delete a webhook
     * @param param the request object
     */
    public deleteWebhookWithHttpInfo(param: WebhooksApiDeleteWebhookRequest, options?: ConfigurationOptions): Promise<HttpInfo<void>> {
        return this.api.deleteWebhookWithHttpInfo(param.id,  options).toPromise();
    }

    /**
     * Delete a webhook
     * @param param the request object
     */
    public deleteWebhook(param: WebhooksApiDeleteWebhookRequest, options?: ConfigurationOptions): Promise<void> {
        return this.api.deleteWebhook(param.id,  options).toPromise();
    }

    /**
     * Get a webhook
     * @param param the request object
     */
    public getWebhookWithHttpInfo(param: WebhooksApiGetWebhookRequest, options?: ConfigurationOptions): Promise<HttpInfo<WebhookView>> {
        return this.api.getWebhookWithHttpInfo(param.id,  options).toPromise();
    }

    /**
     * Get a webhook
     * @param param the request object
     */
    public getWebhook(param: WebhooksApiGetWebhookRequest, options?: ConfigurationOptions): Promise<WebhookView> {
        return this.api.getWebhook(param.id,  options).toPromise();
    }

    /**
     * The per-webhook delivery log (read-only debug view).
     * List webhook deliveries
     * @param param the request object
     */
    public listWebhookDeliveriesWithHttpInfo(param: WebhooksApiListWebhookDeliveriesRequest, options?: ConfigurationOptions): Promise<HttpInfo<PageWebhookDeliveryView>> {
        return this.api.listWebhookDeliveriesWithHttpInfo(param.id, param.status, param.limit,  options).toPromise();
    }

    /**
     * The per-webhook delivery log (read-only debug view).
     * List webhook deliveries
     * @param param the request object
     */
    public listWebhookDeliveries(param: WebhooksApiListWebhookDeliveriesRequest, options?: ConfigurationOptions): Promise<PageWebhookDeliveryView> {
        return this.api.listWebhookDeliveries(param.id, param.status, param.limit,  options).toPromise();
    }

    /**
     * List webhooks
     * @param param the request object
     */
    public listWebhooksWithHttpInfo(param: WebhooksApiListWebhooksRequest = {}, options?: ConfigurationOptions): Promise<HttpInfo<PageWebhookView>> {
        return this.api.listWebhooksWithHttpInfo( options).toPromise();
    }

    /**
     * List webhooks
     * @param param the request object
     */
    public listWebhooks(param: WebhooksApiListWebhooksRequest = {}, options?: ConfigurationOptions): Promise<PageWebhookView> {
        return this.api.listWebhooks( options).toPromise();
    }

    /**
     * Mint a new signing secret; the previous one stays valid for a 24h grace window. Returns the new secret (shown once). Honors Idempotency-Key so a retried rotate replays the same secret instead of rotating twice.
     * Rotate a webhook signing secret
     * @param param the request object
     */
    public rotateWebhookSecretWithHttpInfo(param: WebhooksApiRotateWebhookSecretRequest, options?: ConfigurationOptions): Promise<HttpInfo<RotateSecretResponse>> {
        return this.api.rotateWebhookSecretWithHttpInfo(param.id, param.idempotencyKey,  options).toPromise();
    }

    /**
     * Mint a new signing secret; the previous one stays valid for a 24h grace window. Returns the new secret (shown once). Honors Idempotency-Key so a retried rotate replays the same secret instead of rotating twice.
     * Rotate a webhook signing secret
     * @param param the request object
     */
    public rotateWebhookSecret(param: WebhooksApiRotateWebhookSecretRequest, options?: ConfigurationOptions): Promise<RotateSecretResponse> {
        return this.api.rotateWebhookSecret(param.id, param.idempotencyKey,  options).toPromise();
    }

    /**
     * Schedule a one-off synthetic delivery to this webhook for development. Returns the delivery id.
     * Fire a synthetic event
     * @param param the request object
     */
    public testWebhookWithHttpInfo(param: WebhooksApiTestWebhookRequest, options?: ConfigurationOptions): Promise<HttpInfo<TestWebhookResponse>> {
        return this.api.testWebhookWithHttpInfo(param.id, param.testWebhookRequest,  options).toPromise();
    }

    /**
     * Schedule a one-off synthetic delivery to this webhook for development. Returns the delivery id.
     * Fire a synthetic event
     * @param param the request object
     */
    public testWebhook(param: WebhooksApiTestWebhookRequest, options?: ConfigurationOptions): Promise<TestWebhookResponse> {
        return this.api.testWebhook(param.id, param.testWebhookRequest,  options).toPromise();
    }

    /**
     * Partial update. url/events/filters are full-replace when present. Re-enabling within the auto-disable cooldown returns 409.
     * Update a webhook
     * @param param the request object
     */
    public updateWebhookWithHttpInfo(param: WebhooksApiUpdateWebhookRequest, options?: ConfigurationOptions): Promise<HttpInfo<WebhookView>> {
        return this.api.updateWebhookWithHttpInfo(param.id, param.updateWebhookRequest,  options).toPromise();
    }

    /**
     * Partial update. url/events/filters are full-replace when present. Re-enabling within the auto-disable cooldown returns 409.
     * Update a webhook
     * @param param the request object
     */
    public updateWebhook(param: WebhooksApiUpdateWebhookRequest, options?: ConfigurationOptions): Promise<WebhookView> {
        return this.api.updateWebhook(param.id, param.updateWebhookRequest,  options).toPromise();
    }

}
