// TODO: better import syntax?
import {BaseAPIRequestFactory, RequiredError, COLLECTION_FORMATS} from './baseapi.js';
import {Configuration} from '../configuration.js';
import {RequestContext, HttpMethod, ResponseContext, HttpFile, HttpInfo} from '../http/http.js';
import {ObjectSerializer} from '../models/ObjectSerializer.js';
import {ApiException} from './exception.js';
import {canConsumeForm, isCodeInRange} from '../util.js';
import {SecurityAuthentication} from '../auth/auth.js';


import { AttachmentView } from '../models/AttachmentView.js';
import { DeleteMessageResult } from '../models/DeleteMessageResult.js';
import { ErrorEnvelope } from '../models/ErrorEnvelope.js';
import { ForwardRequest } from '../models/ForwardRequest.js';
import { LimitExceededEnvelope } from '../models/LimitExceededEnvelope.js';
import { MessageView } from '../models/MessageView.js';
import { PageMessageSummaryView } from '../models/PageMessageSummaryView.js';
import { RateLimitedEnvelope } from '../models/RateLimitedEnvelope.js';
import { ReplyRequest } from '../models/ReplyRequest.js';
import { SendEmailRequest } from '../models/SendEmailRequest.js';
import { SendResultView } from '../models/SendResultView.js';
import { UpdateMessageRequest } from '../models/UpdateMessageRequest.js';
import { UpdateMessageResultView } from '../models/UpdateMessageResultView.js';

/**
 * no description
 */
export class MessagesApiRequestFactory extends BaseAPIRequestFactory {

    /**
     * Move a message to the trash. Trashed messages disappear from lists, threads, and reply targets, but can be restored via POST …/messages/{id}/restore until they are purged ~30 days after deletion. No confirmation is required because the default delete is reversible. Pass permanent=true with confirm=DELETE to permanently delete a message that is ALREADY in the trash (\"delete forever\"). A message held for review (review_status=pending_review) cannot be deleted — resolve it in the review queue first (409 message_held).
     * Delete a message (move to trash)
     * @param email The agent\&#39;s full email address.
     * @param id The message id, e.g. msg_abc123.
     * @param permanent Permanently delete a message that is already in the trash (irreversible). Requires confirm&#x3D;DELETE and an account-scoped credential.
     * @param confirm Must be the literal DELETE when permanent&#x3D;true.
     */
    public async deleteMessage(email: string, id: string, permanent?: boolean, confirm?: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'email' is not null or undefined
        if (email === null || email === undefined) {
            throw new RequiredError("MessagesApi", "deleteMessage", "email");
        }


        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("MessagesApi", "deleteMessage", "id");
        }




        // Path Params
        const localVarPath = '/v1/agents/{email}/messages/{id}'
            .replace('{' + 'email' + '}', encodeURIComponent(String(email)))
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.DELETE);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Query Params
        if (permanent !== undefined) {
            requestContext.setQueryParam("permanent", ObjectSerializer.serialize(permanent, "boolean", ""));
        }

        // Query Params
        if (confirm !== undefined) {
            requestContext.setQueryParam("confirm", ObjectSerializer.serialize(confirm, "string", ""));
        }


        let authMethod: SecurityAuthentication | undefined;
        // Apply auth methods
        authMethod = _config.authMethods["bearer"]
        if (authMethod?.applySecurityAuthentication) {
            await authMethod?.applySecurityAuthentication(requestContext);
        }
        
        const defaultAuth: SecurityAuthentication | undefined = _config?.authMethods?.default
        if (defaultAuth?.applySecurityAuthentication) {
            await defaultAuth?.applySecurityAuthentication(requestContext);
        }

        return requestContext;
    }

    /**
     * Forward a message (inbound or outbound) to new recipients; the original is quoted and its attachments are carried over by default. Any attachments[] you supply are added on top of the originals. 202 when held for HITL. Attachment limits apply to the combined set (carried-over originals + supplied): at most 10 attachments, each ≤ 10 MB decoded, ≤ 25 MB decoded combined (over-count → 400 invalid_request; over-size → 413 payload_too_large).
     * Forward a message
     * @param email 
     * @param id 
     * @param forwardRequest 
     * @param idempotencyKey Optional idempotency key for safe retries (unique per logical request). A retry with the same key and byte-identical body replays the first request\&#39;s response instead of re-executing it. Completed keys are remembered for at least 24 hours (the published minimum dedup window). Within the window: same key + different body → 422 idempotency_key_reuse (do not retry as-is); same key while the first request is still executing → 409 idempotency_in_flight (wait, then retry unchanged). Dedup is best-effort: under idempotency-store degradation or a mid-request crash the guarantee degrades to at-least-once — a keyed retry may re-execute rather than replay.
     * @param wait Sync-compat valve. wait&#x3D;sent holds the request until the message reaches a terminal-or-held state or a bounded timeout (≤20s), then returns that state; on timeout returns status&#x3D;accepted. Default: no wait. Always branch on body.status, not the HTTP code. No-op until the async pipeline ships — a synchronous server already has the outcome.
     */
    public async forwardMessage(email: string, id: string, forwardRequest: ForwardRequest, idempotencyKey?: string, wait?: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'email' is not null or undefined
        if (email === null || email === undefined) {
            throw new RequiredError("MessagesApi", "forwardMessage", "email");
        }


        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("MessagesApi", "forwardMessage", "id");
        }


        // verify required parameter 'forwardRequest' is not null or undefined
        if (forwardRequest === null || forwardRequest === undefined) {
            throw new RequiredError("MessagesApi", "forwardMessage", "forwardRequest");
        }




        // Path Params
        const localVarPath = '/v1/agents/{email}/messages/{id}/forward'
            .replace('{' + 'email' + '}', encodeURIComponent(String(email)))
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.POST);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Query Params
        if (wait !== undefined) {
            requestContext.setQueryParam("wait", ObjectSerializer.serialize(wait, "string", ""));
        }

        // Header Params
        requestContext.setHeaderParam("Idempotency-Key", ObjectSerializer.serialize(idempotencyKey, "string", ""));


        // Body Params
        const contentType = ObjectSerializer.getPreferredMediaType([
            "application/json"
        ]);
        requestContext.setHeaderParam("Content-Type", contentType);
        const serializedBody = ObjectSerializer.stringify(
            ObjectSerializer.serialize(forwardRequest, "ForwardRequest", ""),
            contentType
        );
        requestContext.setBody(serializedBody);

        let authMethod: SecurityAuthentication | undefined;
        // Apply auth methods
        authMethod = _config.authMethods["bearer"]
        if (authMethod?.applySecurityAuthentication) {
            await authMethod?.applySecurityAuthentication(requestContext);
        }
        
        const defaultAuth: SecurityAuthentication | undefined = _config?.authMethods?.default
        if (defaultAuth?.applySecurityAuthentication) {
            await defaultAuth?.applySecurityAuthentication(requestContext);
        }

        return requestContext;
    }

    /**
     * Returns one attachment\'s metadata plus a short-lived `download_url` (+ `expires_at`) to fetch the bytes out of band — so binary content never streams through an agent\'s context. Pass `?inline=true` to also receive base64 `data` for small attachments (<= 256 KB); larger inline requests are rejected with 413 attachment_too_large. `index` is the 0-based attachment index from the message\'s `attachments[]`.
     * Get an attachment (metadata + short-lived download URL)
     * @param email 
     * @param id 
     * @param index 
     * @param inline When true, also include the bytes as base64 in \&#39;data\&#39; — ONLY for attachments &lt;&#x3D; 256 KB; larger inline requests are rejected (413). Default false (use download_url).
     */
    public async getAttachment(email: string, id: string, index: number, inline?: boolean, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'email' is not null or undefined
        if (email === null || email === undefined) {
            throw new RequiredError("MessagesApi", "getAttachment", "email");
        }


        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("MessagesApi", "getAttachment", "id");
        }


        // verify required parameter 'index' is not null or undefined
        if (index === null || index === undefined) {
            throw new RequiredError("MessagesApi", "getAttachment", "index");
        }



        // Path Params
        const localVarPath = '/v1/agents/{email}/messages/{id}/attachments/{index}'
            .replace('{' + 'email' + '}', encodeURIComponent(String(email)))
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)))
            .replace('{' + 'index' + '}', encodeURIComponent(String(index)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.GET);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Query Params
        if (inline !== undefined) {
            requestContext.setQueryParam("inline", ObjectSerializer.serialize(inline, "boolean", ""));
        }


        let authMethod: SecurityAuthentication | undefined;
        // Apply auth methods
        authMethod = _config.authMethods["bearer"]
        if (authMethod?.applySecurityAuthentication) {
            await authMethod?.applySecurityAuthentication(requestContext);
        }
        
        const defaultAuth: SecurityAuthentication | undefined = _config?.authMethods?.default
        if (defaultAuth?.applySecurityAuthentication) {
            await defaultAuth?.applySecurityAuthentication(requestContext);
        }

        return requestContext;
    }

    /**
     * Fetch a single message (inbound or outbound) by id, scoped to an agent the caller owns. Includes the raw message and inbound auth headers.
     * Get a message
     * @param email The agent\&#39;s full email address.
     * @param id The message id, e.g. msg_abc123.
     */
    public async getMessage(email: string, id: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'email' is not null or undefined
        if (email === null || email === undefined) {
            throw new RequiredError("MessagesApi", "getMessage", "email");
        }


        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("MessagesApi", "getMessage", "id");
        }


        // Path Params
        const localVarPath = '/v1/agents/{email}/messages/{id}'
            .replace('{' + 'email' + '}', encodeURIComponent(String(email)))
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.GET);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")


        let authMethod: SecurityAuthentication | undefined;
        // Apply auth methods
        authMethod = _config.authMethods["bearer"]
        if (authMethod?.applySecurityAuthentication) {
            await authMethod?.applySecurityAuthentication(requestContext);
        }
        
        const defaultAuth: SecurityAuthentication | undefined = _config?.authMethods?.default
        if (defaultAuth?.applySecurityAuthentication) {
            await defaultAuth?.applySecurityAuthentication(requestContext);
        }

        return requestContext;
    }

    /**
     * List an agent\'s messages (inbound + outbound) with filters and cursor pagination. Held outbound drafts appear as status=pending_review. Pass deleted=true for the trash (soft-deleted messages, restorable until purged ~30 days after deletion); the trash view defaults to direction=all and read_status=all.
     * List messages
     * @param email 
     * @param direction Defaults to inbound.
     * @param readStatus Inbound only. Filters by inbox read-state (MSG-1). Defaults to unread for inbound, all otherwise.
     * @param sort Defaults to desc (newest first).
     * @param from_ Case-insensitive substring match on sender.
     * @param subjectContains Case-insensitive substring match on subject.
     * @param conversationId 
     * @param labels Repeatable; AND-matched.
     * @param since RFC3339; created_at &gt;&#x3D; since.
     * @param until RFC3339; created_at &lt; until.
     * @param cursor 
     * @param limit 
     * @param deleted List the trash instead: messages that were soft-deleted and are restorable until purged (~30 days after deletion). Defaults to false (live messages only).
     */
    public async listMessages(email: string, direction?: 'inbound' | 'outbound' | 'all', readStatus?: 'unread' | 'read' | 'all', sort?: 'asc' | 'desc', from_?: string, subjectContains?: string, conversationId?: string, labels?: Array<string>, since?: string, until?: string, cursor?: string, limit?: number, deleted?: boolean, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'email' is not null or undefined
        if (email === null || email === undefined) {
            throw new RequiredError("MessagesApi", "listMessages", "email");
        }














        // Path Params
        const localVarPath = '/v1/agents/{email}/messages'
            .replace('{' + 'email' + '}', encodeURIComponent(String(email)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.GET);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Query Params
        if (direction !== undefined) {
            requestContext.setQueryParam("direction", ObjectSerializer.serialize(direction, "'inbound' | 'outbound' | 'all'", ""));
        }

        // Query Params
        if (readStatus !== undefined) {
            requestContext.setQueryParam("read_status", ObjectSerializer.serialize(readStatus, "'unread' | 'read' | 'all'", ""));
        }

        // Query Params
        if (sort !== undefined) {
            requestContext.setQueryParam("sort", ObjectSerializer.serialize(sort, "'asc' | 'desc'", ""));
        }

        // Query Params
        if (from_ !== undefined) {
            requestContext.setQueryParam("from", ObjectSerializer.serialize(from_, "string", ""));
        }

        // Query Params
        if (subjectContains !== undefined) {
            requestContext.setQueryParam("subject_contains", ObjectSerializer.serialize(subjectContains, "string", ""));
        }

        // Query Params
        if (conversationId !== undefined) {
            requestContext.setQueryParam("conversation_id", ObjectSerializer.serialize(conversationId, "string", ""));
        }

        // Query Params
        if (labels !== undefined) {
            requestContext.setQueryParam("labels", ObjectSerializer.serialize(labels, "Array<string>", ""));
        }

        // Query Params
        if (since !== undefined) {
            requestContext.setQueryParam("since", ObjectSerializer.serialize(since, "string", ""));
        }

        // Query Params
        if (until !== undefined) {
            requestContext.setQueryParam("until", ObjectSerializer.serialize(until, "string", ""));
        }

        // Query Params
        if (cursor !== undefined) {
            requestContext.setQueryParam("cursor", ObjectSerializer.serialize(cursor, "string", ""));
        }

        // Query Params
        if (limit !== undefined) {
            requestContext.setQueryParam("limit", ObjectSerializer.serialize(limit, "number", "int64"));
        }

        // Query Params
        if (deleted !== undefined) {
            requestContext.setQueryParam("deleted", ObjectSerializer.serialize(deleted, "boolean", ""));
        }


        let authMethod: SecurityAuthentication | undefined;
        // Apply auth methods
        authMethod = _config.authMethods["bearer"]
        if (authMethod?.applySecurityAuthentication) {
            await authMethod?.applySecurityAuthentication(requestContext);
        }
        
        const defaultAuth: SecurityAuthentication | undefined = _config?.authMethods?.default
        if (defaultAuth?.applySecurityAuthentication) {
            await defaultAuth?.applySecurityAuthentication(requestContext);
        }

        return requestContext;
    }

    /**
     * Reply to a message (inbound or outbound); recipients and threading are derived from the original. Replying to a message the agent received targets its sender; replying to a message the agent sent continues the thread to its original recipients (`reply_all` also re-includes the original Cc). 202 when held for HITL. Attachment limits: at most 10 attachments, each ≤ 10 MB decoded, ≤ 25 MB decoded combined (over-count → 400 invalid_request; over-size → 413 payload_too_large).
     * Reply to a message
     * @param email 
     * @param id 
     * @param replyRequest 
     * @param idempotencyKey Optional idempotency key for safe retries (unique per logical request). A retry with the same key and byte-identical body replays the first request\&#39;s response instead of re-executing it. Completed keys are remembered for at least 24 hours (the published minimum dedup window). Within the window: same key + different body → 422 idempotency_key_reuse (do not retry as-is); same key while the first request is still executing → 409 idempotency_in_flight (wait, then retry unchanged). Dedup is best-effort: under idempotency-store degradation or a mid-request crash the guarantee degrades to at-least-once — a keyed retry may re-execute rather than replay.
     * @param wait Sync-compat valve. wait&#x3D;sent holds the request until the message reaches a terminal-or-held state or a bounded timeout (≤20s), then returns that state; on timeout returns status&#x3D;accepted. Default: no wait. Always branch on body.status, not the HTTP code. No-op until the async pipeline ships — a synchronous server already has the outcome.
     */
    public async replyToMessage(email: string, id: string, replyRequest: ReplyRequest, idempotencyKey?: string, wait?: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'email' is not null or undefined
        if (email === null || email === undefined) {
            throw new RequiredError("MessagesApi", "replyToMessage", "email");
        }


        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("MessagesApi", "replyToMessage", "id");
        }


        // verify required parameter 'replyRequest' is not null or undefined
        if (replyRequest === null || replyRequest === undefined) {
            throw new RequiredError("MessagesApi", "replyToMessage", "replyRequest");
        }




        // Path Params
        const localVarPath = '/v1/agents/{email}/messages/{id}/reply'
            .replace('{' + 'email' + '}', encodeURIComponent(String(email)))
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.POST);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Query Params
        if (wait !== undefined) {
            requestContext.setQueryParam("wait", ObjectSerializer.serialize(wait, "string", ""));
        }

        // Header Params
        requestContext.setHeaderParam("Idempotency-Key", ObjectSerializer.serialize(idempotencyKey, "string", ""));


        // Body Params
        const contentType = ObjectSerializer.getPreferredMediaType([
            "application/json"
        ]);
        requestContext.setHeaderParam("Content-Type", contentType);
        const serializedBody = ObjectSerializer.stringify(
            ObjectSerializer.serialize(replyRequest, "ReplyRequest", ""),
            contentType
        );
        requestContext.setBody(serializedBody);

        let authMethod: SecurityAuthentication | undefined;
        // Apply auth methods
        authMethod = _config.authMethods["bearer"]
        if (authMethod?.applySecurityAuthentication) {
            await authMethod?.applySecurityAuthentication(requestContext);
        }
        
        const defaultAuth: SecurityAuthentication | undefined = _config?.authMethods?.default
        if (defaultAuth?.applySecurityAuthentication) {
            await defaultAuth?.applySecurityAuthentication(requestContext);
        }

        return requestContext;
    }

    /**
     * Bring a trashed (soft-deleted) message back to the inbox. Its remaining retention resumes where it left off — time spent in the trash does not count against the message\'s normal lifetime. Returns the restored message. 409 not_in_trash when the message is not in the trash.
     * Restore a message from the trash
     * @param email The agent\&#39;s full email address.
     * @param id The message id, e.g. msg_abc123.
     */
    public async restoreMessage(email: string, id: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'email' is not null or undefined
        if (email === null || email === undefined) {
            throw new RequiredError("MessagesApi", "restoreMessage", "email");
        }


        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("MessagesApi", "restoreMessage", "id");
        }


        // Path Params
        const localVarPath = '/v1/agents/{email}/messages/{id}/restore'
            .replace('{' + 'email' + '}', encodeURIComponent(String(email)))
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.POST);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")


        let authMethod: SecurityAuthentication | undefined;
        // Apply auth methods
        authMethod = _config.authMethods["bearer"]
        if (authMethod?.applySecurityAuthentication) {
            await authMethod?.applySecurityAuthentication(requestContext);
        }
        
        const defaultAuth: SecurityAuthentication | undefined = _config?.authMethods?.default
        if (defaultAuth?.applySecurityAuthentication) {
            await defaultAuth?.applySecurityAuthentication(requestContext);
        }

        return requestContext;
    }

    /**
     * Send a new email from the agent named in the path (a new thread). The sender is the path agent — `reply`/`forward` are their own sub-resources. 202 + pending_review when the agent has HITL enabled. Honors Idempotency-Key. Attachment limits: at most 10 attachments, each ≤ 10 MB decoded, ≤ 25 MB decoded combined (over-count → 400 invalid_request; over-size → 413 payload_too_large). Two capacity limits apply and are permanently distinct — branch on the HTTP status: 402 limit_exceeded is a QUOTA (monthly-message / storage stock-or-flow cap; a retry will not clear it — surface an upgrade path), 429 rate_limited is a throughput/request-RATE cap (transient; back off Retry-After seconds and retry).
     * Send a new email
     * @param email 
     * @param sendEmailRequest 
     * @param idempotencyKey Optional idempotency key for safe retries (unique per logical request). A retry with the same key and byte-identical body replays the first request\&#39;s response instead of re-executing it. Completed keys are remembered for at least 24 hours (the published minimum dedup window). Within the window: same key + different body → 422 idempotency_key_reuse (do not retry as-is); same key while the first request is still executing → 409 idempotency_in_flight (wait, then retry unchanged). Dedup is best-effort: under idempotency-store degradation or a mid-request crash the guarantee degrades to at-least-once — a keyed retry may re-execute rather than replay.
     * @param wait Sync-compat valve. wait&#x3D;sent holds the request until the message reaches a terminal-or-held state or a bounded timeout (≤20s), then returns that state; on timeout returns status&#x3D;accepted. Default: no wait. Always branch on body.status, not the HTTP code. No-op until the async pipeline ships — a synchronous server already has the outcome.
     */
    public async sendMessage(email: string, sendEmailRequest: SendEmailRequest, idempotencyKey?: string, wait?: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'email' is not null or undefined
        if (email === null || email === undefined) {
            throw new RequiredError("MessagesApi", "sendMessage", "email");
        }


        // verify required parameter 'sendEmailRequest' is not null or undefined
        if (sendEmailRequest === null || sendEmailRequest === undefined) {
            throw new RequiredError("MessagesApi", "sendMessage", "sendEmailRequest");
        }




        // Path Params
        const localVarPath = '/v1/agents/{email}/messages'
            .replace('{' + 'email' + '}', encodeURIComponent(String(email)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.POST);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Query Params
        if (wait !== undefined) {
            requestContext.setQueryParam("wait", ObjectSerializer.serialize(wait, "string", ""));
        }

        // Header Params
        requestContext.setHeaderParam("Idempotency-Key", ObjectSerializer.serialize(idempotencyKey, "string", ""));


        // Body Params
        const contentType = ObjectSerializer.getPreferredMediaType([
            "application/json"
        ]);
        requestContext.setHeaderParam("Content-Type", contentType);
        const serializedBody = ObjectSerializer.stringify(
            ObjectSerializer.serialize(sendEmailRequest, "SendEmailRequest", ""),
            contentType
        );
        requestContext.setBody(serializedBody);

        let authMethod: SecurityAuthentication | undefined;
        // Apply auth methods
        authMethod = _config.authMethods["bearer"]
        if (authMethod?.applySecurityAuthentication) {
            await authMethod?.applySecurityAuthentication(requestContext);
        }
        
        const defaultAuth: SecurityAuthentication | undefined = _config?.authMethods?.default
        if (defaultAuth?.applySecurityAuthentication) {
            await defaultAuth?.applySecurityAuthentication(requestContext);
        }

        return requestContext;
    }

    /**
     * Apply a labels delta (`add_labels` / `remove_labels`) to a message the caller owns; returns the post-update label set. Each list is capped at 50 entries; labels are lowercase `[a-z0-9:_-]+` up to 64 chars; the `e2a:` prefix is reserved for system labels. A message carries at most 100 labels. An empty delta is a read of the current labels.
     * Update a message (labels)
     * @param email 
     * @param id 
     * @param updateMessageRequest 
     */
    public async updateMessage(email: string, id: string, updateMessageRequest: UpdateMessageRequest, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'email' is not null or undefined
        if (email === null || email === undefined) {
            throw new RequiredError("MessagesApi", "updateMessage", "email");
        }


        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("MessagesApi", "updateMessage", "id");
        }


        // verify required parameter 'updateMessageRequest' is not null or undefined
        if (updateMessageRequest === null || updateMessageRequest === undefined) {
            throw new RequiredError("MessagesApi", "updateMessage", "updateMessageRequest");
        }


        // Path Params
        const localVarPath = '/v1/agents/{email}/messages/{id}'
            .replace('{' + 'email' + '}', encodeURIComponent(String(email)))
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.PATCH);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")


        // Body Params
        const contentType = ObjectSerializer.getPreferredMediaType([
            "application/json"
        ]);
        requestContext.setHeaderParam("Content-Type", contentType);
        const serializedBody = ObjectSerializer.stringify(
            ObjectSerializer.serialize(updateMessageRequest, "UpdateMessageRequest", ""),
            contentType
        );
        requestContext.setBody(serializedBody);

        let authMethod: SecurityAuthentication | undefined;
        // Apply auth methods
        authMethod = _config.authMethods["bearer"]
        if (authMethod?.applySecurityAuthentication) {
            await authMethod?.applySecurityAuthentication(requestContext);
        }
        
        const defaultAuth: SecurityAuthentication | undefined = _config?.authMethods?.default
        if (defaultAuth?.applySecurityAuthentication) {
            await defaultAuth?.applySecurityAuthentication(requestContext);
        }

        return requestContext;
    }

}

export class MessagesApiResponseProcessor {

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to deleteMessage
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async deleteMessageWithHttpInfo(response: ResponseContext): Promise<HttpInfo<DeleteMessageResult >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: DeleteMessageResult = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "DeleteMessageResult", ""
            ) as DeleteMessageResult;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }
        if (isCodeInRange("0", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Error", body, response.headers);
        }

        // Work around for missing responses in specification, e.g. for petstore.yaml
        if (response.httpStatusCode >= 200 && response.httpStatusCode <= 299) {
            const body: DeleteMessageResult = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "DeleteMessageResult", ""
            ) as DeleteMessageResult;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to forwardMessage
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async forwardMessageWithHttpInfo(response: ResponseContext): Promise<HttpInfo<SendResultView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: SendResultView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "SendResultView", ""
            ) as SendResultView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }
        if (isCodeInRange("202", response.httpStatusCode)) {
            const body: SendResultView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "SendResultView", ""
            ) as SendResultView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }
        if (isCodeInRange("400", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Bad Request — request-shape/validation failure. error.code includes invalid_request (e.g. more than 10 attachments), too_many_recipients, invalid_recipient, invalid_attachment (undecodable base64).", body, response.headers);
        }
        if (isCodeInRange("402", response.httpStatusCode)) {
            const body: LimitExceededEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "LimitExceededEnvelope", ""
            ) as LimitExceededEnvelope;
            throw new ApiException<LimitExceededEnvelope>(response.httpStatusCode, "Payment required — a per-account resource cap was hit (code limit_exceeded). error.details.resource is the AccountView usage/limits field stem (agents, domains, messages_month, storage_bytes), so the client can key it to usage.&lt;resource&gt; / limits.max_&lt;resource&gt;. This is a QUOTA (stock/flow) cap — distinct from a 429 rate_limited (throughput). A retry alone will not clear it; surface a quota/upgrade path.", body, response.headers);
        }
        if (isCodeInRange("409", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Conflict — code idempotency_in_flight: a request with this Idempotency-Key is still executing. Retry-able: wait for the first request to finish, then retry with the SAME key and byte-identical body — the retry replays the first request\&#39;s response instead of re-executing the side effect.", body, response.headers);
        }
        if (isCodeInRange("413", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Payload Too Large — an attachment exceeds 10 MB decoded, or the combined attachments exceed 25 MB decoded. error.code &#x3D; payload_too_large; error.details carries the offending size and the limit.", body, response.headers);
        }
        if (isCodeInRange("422", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Unprocessable — branch on error.code. idempotency_key_reuse: this Idempotency-Key was already used with a DIFFERENT request body (the dedup hash covers the route + the raw body bytes) — do NOT retry as-is; a legitimate retry must resend the byte-identical body, and a genuinely new request needs a fresh key. invalid_request: a semantic validation failure in the request body.", body, response.headers);
        }
        if (isCodeInRange("429", response.httpStatusCode)) {
            const body: RateLimitedEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "RateLimitedEnvelope", ""
            ) as RateLimitedEnvelope;
            throw new ApiException<RateLimitedEnvelope>(response.httpStatusCode, "Too Many Requests — a request-RATE / throughput limit was hit (code rate_limited). This is distinct from a 402 limit_exceeded (a QUOTA cap): a 429 is transient and retry-able — wait error.details.retry_after_seconds (mirrored on the Retry-After header), then the same request succeeds. Branch on the HTTP status: 429 → back off and retry; 402 → surface a quota/upgrade path.", body, response.headers);
        }
        if (isCodeInRange("0", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Error — the standard envelope; branch on error.code.", body, response.headers);
        }

        // Work around for missing responses in specification, e.g. for petstore.yaml
        if (response.httpStatusCode >= 200 && response.httpStatusCode <= 299) {
            const body: SendResultView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "SendResultView", ""
            ) as SendResultView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to getAttachment
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async getAttachmentWithHttpInfo(response: ResponseContext): Promise<HttpInfo<AttachmentView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: AttachmentView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "AttachmentView", ""
            ) as AttachmentView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }
        if (isCodeInRange("413", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Payload Too Large — code attachment_too_large: &#x60;?inline&#x3D;true&#x60; was requested for an attachment over the 256 KB inline cap. Fetch the bytes via download_url instead; the metadata (and this error) tells you the size.", body, response.headers);
        }
        if (isCodeInRange("0", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Error — the standard envelope; branch on error.code.", body, response.headers);
        }

        // Work around for missing responses in specification, e.g. for petstore.yaml
        if (response.httpStatusCode >= 200 && response.httpStatusCode <= 299) {
            const body: AttachmentView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "AttachmentView", ""
            ) as AttachmentView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to getMessage
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async getMessageWithHttpInfo(response: ResponseContext): Promise<HttpInfo<MessageView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: MessageView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "MessageView", ""
            ) as MessageView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }
        if (isCodeInRange("0", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Error", body, response.headers);
        }

        // Work around for missing responses in specification, e.g. for petstore.yaml
        if (response.httpStatusCode >= 200 && response.httpStatusCode <= 299) {
            const body: MessageView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "MessageView", ""
            ) as MessageView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to listMessages
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async listMessagesWithHttpInfo(response: ResponseContext): Promise<HttpInfo<PageMessageSummaryView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: PageMessageSummaryView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "PageMessageSummaryView", ""
            ) as PageMessageSummaryView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }
        if (isCodeInRange("0", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Error", body, response.headers);
        }

        // Work around for missing responses in specification, e.g. for petstore.yaml
        if (response.httpStatusCode >= 200 && response.httpStatusCode <= 299) {
            const body: PageMessageSummaryView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "PageMessageSummaryView", ""
            ) as PageMessageSummaryView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to replyToMessage
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async replyToMessageWithHttpInfo(response: ResponseContext): Promise<HttpInfo<SendResultView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: SendResultView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "SendResultView", ""
            ) as SendResultView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }
        if (isCodeInRange("202", response.httpStatusCode)) {
            const body: SendResultView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "SendResultView", ""
            ) as SendResultView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }
        if (isCodeInRange("400", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Bad Request — request-shape/validation failure. error.code includes invalid_request (e.g. more than 10 attachments), too_many_recipients, invalid_recipient, invalid_attachment (undecodable base64).", body, response.headers);
        }
        if (isCodeInRange("402", response.httpStatusCode)) {
            const body: LimitExceededEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "LimitExceededEnvelope", ""
            ) as LimitExceededEnvelope;
            throw new ApiException<LimitExceededEnvelope>(response.httpStatusCode, "Payment required — a per-account resource cap was hit (code limit_exceeded). error.details.resource is the AccountView usage/limits field stem (agents, domains, messages_month, storage_bytes), so the client can key it to usage.&lt;resource&gt; / limits.max_&lt;resource&gt;. This is a QUOTA (stock/flow) cap — distinct from a 429 rate_limited (throughput). A retry alone will not clear it; surface a quota/upgrade path.", body, response.headers);
        }
        if (isCodeInRange("409", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Conflict — code idempotency_in_flight: a request with this Idempotency-Key is still executing. Retry-able: wait for the first request to finish, then retry with the SAME key and byte-identical body — the retry replays the first request\&#39;s response instead of re-executing the side effect.", body, response.headers);
        }
        if (isCodeInRange("413", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Payload Too Large — an attachment exceeds 10 MB decoded, or the combined attachments exceed 25 MB decoded. error.code &#x3D; payload_too_large; error.details carries the offending size and the limit.", body, response.headers);
        }
        if (isCodeInRange("422", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Unprocessable — branch on error.code. idempotency_key_reuse: this Idempotency-Key was already used with a DIFFERENT request body (the dedup hash covers the route + the raw body bytes) — do NOT retry as-is; a legitimate retry must resend the byte-identical body, and a genuinely new request needs a fresh key. invalid_request: a semantic validation failure in the request body.", body, response.headers);
        }
        if (isCodeInRange("429", response.httpStatusCode)) {
            const body: RateLimitedEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "RateLimitedEnvelope", ""
            ) as RateLimitedEnvelope;
            throw new ApiException<RateLimitedEnvelope>(response.httpStatusCode, "Too Many Requests — a request-RATE / throughput limit was hit (code rate_limited). This is distinct from a 402 limit_exceeded (a QUOTA cap): a 429 is transient and retry-able — wait error.details.retry_after_seconds (mirrored on the Retry-After header), then the same request succeeds. Branch on the HTTP status: 429 → back off and retry; 402 → surface a quota/upgrade path.", body, response.headers);
        }
        if (isCodeInRange("0", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Error — the standard envelope; branch on error.code.", body, response.headers);
        }

        // Work around for missing responses in specification, e.g. for petstore.yaml
        if (response.httpStatusCode >= 200 && response.httpStatusCode <= 299) {
            const body: SendResultView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "SendResultView", ""
            ) as SendResultView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to restoreMessage
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async restoreMessageWithHttpInfo(response: ResponseContext): Promise<HttpInfo<MessageView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: MessageView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "MessageView", ""
            ) as MessageView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }
        if (isCodeInRange("0", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Error", body, response.headers);
        }

        // Work around for missing responses in specification, e.g. for petstore.yaml
        if (response.httpStatusCode >= 200 && response.httpStatusCode <= 299) {
            const body: MessageView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "MessageView", ""
            ) as MessageView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to sendMessage
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async sendMessageWithHttpInfo(response: ResponseContext): Promise<HttpInfo<SendResultView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: SendResultView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "SendResultView", ""
            ) as SendResultView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }
        if (isCodeInRange("202", response.httpStatusCode)) {
            const body: SendResultView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "SendResultView", ""
            ) as SendResultView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }
        if (isCodeInRange("400", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Bad Request — request-shape/validation failure. error.code includes invalid_request (e.g. more than 10 attachments), too_many_recipients, invalid_recipient, invalid_attachment (undecodable base64).", body, response.headers);
        }
        if (isCodeInRange("402", response.httpStatusCode)) {
            const body: LimitExceededEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "LimitExceededEnvelope", ""
            ) as LimitExceededEnvelope;
            throw new ApiException<LimitExceededEnvelope>(response.httpStatusCode, "Payment required — a per-account resource cap was hit (code limit_exceeded). error.details.resource is the AccountView usage/limits field stem (agents, domains, messages_month, storage_bytes), so the client can key it to usage.&lt;resource&gt; / limits.max_&lt;resource&gt;. This is a QUOTA (stock/flow) cap — distinct from a 429 rate_limited (throughput). A retry alone will not clear it; surface a quota/upgrade path.", body, response.headers);
        }
        if (isCodeInRange("409", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Conflict — code idempotency_in_flight: a request with this Idempotency-Key is still executing. Retry-able: wait for the first request to finish, then retry with the SAME key and byte-identical body — the retry replays the first request\&#39;s response instead of re-executing the side effect.", body, response.headers);
        }
        if (isCodeInRange("413", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Payload Too Large — an attachment exceeds 10 MB decoded, or the combined attachments exceed 25 MB decoded. error.code &#x3D; payload_too_large; error.details carries the offending size and the limit.", body, response.headers);
        }
        if (isCodeInRange("422", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Unprocessable — branch on error.code. idempotency_key_reuse: this Idempotency-Key was already used with a DIFFERENT request body (the dedup hash covers the route + the raw body bytes) — do NOT retry as-is; a legitimate retry must resend the byte-identical body, and a genuinely new request needs a fresh key. invalid_request: a semantic validation failure in the request body.", body, response.headers);
        }
        if (isCodeInRange("429", response.httpStatusCode)) {
            const body: RateLimitedEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "RateLimitedEnvelope", ""
            ) as RateLimitedEnvelope;
            throw new ApiException<RateLimitedEnvelope>(response.httpStatusCode, "Too Many Requests — a request-RATE / throughput limit was hit (code rate_limited). This is distinct from a 402 limit_exceeded (a QUOTA cap): a 429 is transient and retry-able — wait error.details.retry_after_seconds (mirrored on the Retry-After header), then the same request succeeds. Branch on the HTTP status: 429 → back off and retry; 402 → surface a quota/upgrade path.", body, response.headers);
        }
        if (isCodeInRange("0", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Error — the standard envelope; branch on error.code.", body, response.headers);
        }

        // Work around for missing responses in specification, e.g. for petstore.yaml
        if (response.httpStatusCode >= 200 && response.httpStatusCode <= 299) {
            const body: SendResultView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "SendResultView", ""
            ) as SendResultView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to updateMessage
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async updateMessageWithHttpInfo(response: ResponseContext): Promise<HttpInfo<UpdateMessageResultView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: UpdateMessageResultView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "UpdateMessageResultView", ""
            ) as UpdateMessageResultView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }
        if (isCodeInRange("0", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Error", body, response.headers);
        }

        // Work around for missing responses in specification, e.g. for petstore.yaml
        if (response.httpStatusCode >= 200 && response.httpStatusCode <= 299) {
            const body: UpdateMessageResultView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "UpdateMessageResultView", ""
            ) as UpdateMessageResultView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

}
