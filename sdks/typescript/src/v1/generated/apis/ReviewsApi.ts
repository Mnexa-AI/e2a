// TODO: better import syntax?
import {BaseAPIRequestFactory, RequiredError, COLLECTION_FORMATS} from './baseapi.js';
import {Configuration} from '../configuration.js';
import {RequestContext, HttpMethod, ResponseContext, HttpFile, HttpInfo} from '../http/http.js';
import {ObjectSerializer} from '../models/ObjectSerializer.js';
import {ApiException} from './exception.js';
import {canConsumeForm, isCodeInRange} from '../util.js';
import {SecurityAuthentication} from '../auth/auth.js';


import { ApproveRequest } from '../models/ApproveRequest.js';
import { ErrorEnvelope } from '../models/ErrorEnvelope.js';
import { MessageView } from '../models/MessageView.js';
import { PageReviewView } from '../models/PageReviewView.js';
import { RateLimitedEnvelope } from '../models/RateLimitedEnvelope.js';
import { RejectRequest } from '../models/RejectRequest.js';
import { RejectResultView } from '../models/RejectResultView.js';
import { SendResultView } from '../models/SendResultView.js';

/**
 * no description
 */
export class ReviewsApiRequestFactory extends BaseAPIRequestFactory {

    /**
     * Approve a hold. Branches on direction: an outbound draft is sent via SES (honoring Idempotency-Key + optional reviewer overrides); an inbound hold is released to the inbox. Returns 202 with status=accepted when outbound delivery is durably queued for async submission, and 200 for a synchronous terminal sent result or an inbound release. Account-scoped only — an agent cannot approve its own hold. Approving an outbound draft applies the same per-agent send-rate limit as a direct send: 429 rate_limited when the agent is over its throughput limit (back off Retry-After seconds and retry). The merged outbound draft after applying reviewer overrides is subject to the same composed-message ceiling: 10 MiB (10485760 bytes), measured as subject + text + html + decoded attachment bytes; exceeding it returns 413 payload_too_large.
     * Approve a held message
     * @param id 
     * @param approveRequest 
     * @param idempotencyKey Optional idempotency key for safe retries (unique per logical request). A retry with the same key and byte-identical body replays the first request\&#39;s response instead of re-executing it. Completed keys are remembered for at least 24 hours (the published minimum dedup window). Within the window: same key + different body → 422 idempotency_key_reuse (do not retry as-is); same key while the first request is still executing → 409 idempotency_in_flight (wait, then retry unchanged). Dedup is best-effort: under idempotency-store degradation or a mid-request crash the guarantee degrades to at-least-once — a keyed retry may re-execute rather than replay.
     */
    public async approveReview(id: string, approveRequest: ApproveRequest, idempotencyKey?: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("ReviewsApi", "approveReview", "id");
        }


        // verify required parameter 'approveRequest' is not null or undefined
        if (approveRequest === null || approveRequest === undefined) {
            throw new RequiredError("ReviewsApi", "approveReview", "approveRequest");
        }



        // Path Params
        const localVarPath = '/v1/reviews/{id}/approve'
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.POST);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Header Params
        requestContext.setHeaderParam("Idempotency-Key", ObjectSerializer.serialize(idempotencyKey, "string", ""));


        // Body Params
        const contentType = ObjectSerializer.getPreferredMediaType([
            "application/json"
        ]);
        requestContext.setHeaderParam("Content-Type", contentType);
        const serializedBody = ObjectSerializer.stringify(
            ObjectSerializer.serialize(approveRequest, "ApproveRequest", ""),
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
     * Full detail of one held message — body + recipients (and, for inbound, the screening/auth context) — for a reviewer to make a decision. Account-scoped only.
     * Get a held message (full detail)
     * @param id 
     */
    public async getReview(id: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("ReviewsApi", "getReview", "id");
        }


        // Path Params
        const localVarPath = '/v1/reviews/{id}'
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
     * The review queue: every message held in pending_review across the account\'s inboxes — outbound drafts awaiting send approval AND inbound messages held by a screening gate. Account-scoped credentials only; agents cannot see (or resolve) holds.
     * List messages awaiting review
     * @param cursor Opaque pagination cursor from a previous response\&#39;s next_cursor. Continuation requests must not change the other filters.
     * @param limit Maximum number of items to return (1-100).
     */
    public async listReviews(cursor?: string, limit?: number, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;



        // Path Params
        const localVarPath = '/v1/reviews';

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.GET);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Query Params
        if (cursor !== undefined) {
            requestContext.setQueryParam("cursor", ObjectSerializer.serialize(cursor, "string", ""));
        }

        // Query Params
        if (limit !== undefined) {
            requestContext.setQueryParam("limit", ObjectSerializer.serialize(limit, "number", "int64"));
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
     * Reject a hold. An outbound draft is discarded (never sent); an inbound hold is dropped (never reaches the agent; payload retained hidden for forensics). Account-scoped only.
     * Reject a held message
     * @param id 
     * @param rejectRequest 
     */
    public async rejectReview(id: string, rejectRequest: RejectRequest, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("ReviewsApi", "rejectReview", "id");
        }


        // verify required parameter 'rejectRequest' is not null or undefined
        if (rejectRequest === null || rejectRequest === undefined) {
            throw new RequiredError("ReviewsApi", "rejectReview", "rejectRequest");
        }


        // Path Params
        const localVarPath = '/v1/reviews/{id}/reject'
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.POST);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")


        // Body Params
        const contentType = ObjectSerializer.getPreferredMediaType([
            "application/json"
        ]);
        requestContext.setHeaderParam("Content-Type", contentType);
        const serializedBody = ObjectSerializer.stringify(
            ObjectSerializer.serialize(rejectRequest, "RejectRequest", ""),
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

export class ReviewsApiResponseProcessor {

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to approveReview
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async approveReviewWithHttpInfo(response: ResponseContext): Promise<HttpInfo<SendResultView >> {
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
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Bad Request — invalid reviewer overrides. error.code includes too_many_recipients when to, cc, and bcc contain more than 50 recipients combined; error.details reports max_recipients and provided.", body, response.headers);
        }
        if (isCodeInRange("409", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Conflict — branch on error.code. message_not_pending: the hold was already resolved (approved/rejected/expired) and cannot be re-approved. idempotency_in_flight: a request with this Idempotency-Key is still executing — wait for it to finish, then retry with the SAME key and byte-identical body to replay its response.", body, response.headers);
        }
        if (isCodeInRange("413", response.httpStatusCode)) {
            const body: ErrorEnvelope = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "ErrorEnvelope", ""
            ) as ErrorEnvelope;
            throw new ApiException<ErrorEnvelope>(response.httpStatusCode, "Payload Too Large — error.code &#x3D; payload_too_large: the merged outbound draft exceeds the 10 MiB (10485760 byte) composed-message ceiling after applying reviewer overrides. The measured total is subject + text + html + decoded attachment bytes.", body, response.headers);
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
     * @params response Response returned by the server for a request to getReview
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async getReviewWithHttpInfo(response: ResponseContext): Promise<HttpInfo<MessageView >> {
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
     * @params response Response returned by the server for a request to listReviews
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async listReviewsWithHttpInfo(response: ResponseContext): Promise<HttpInfo<PageReviewView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: PageReviewView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "PageReviewView", ""
            ) as PageReviewView;
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
            const body: PageReviewView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "PageReviewView", ""
            ) as PageReviewView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to rejectReview
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async rejectReviewWithHttpInfo(response: ResponseContext): Promise<HttpInfo<RejectResultView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: RejectResultView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "RejectResultView", ""
            ) as RejectResultView;
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
            const body: RejectResultView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "RejectResultView", ""
            ) as RejectResultView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

}
