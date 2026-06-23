// TODO: better import syntax?
import {BaseAPIRequestFactory, RequiredError, COLLECTION_FORMATS} from './baseapi.js';
import {Configuration} from '../configuration.js';
import {RequestContext, HttpMethod, ResponseContext, HttpFile, HttpInfo} from '../http/http.js';
import {ObjectSerializer} from '../models/ObjectSerializer.js';
import {ApiException} from './exception.js';
import {canConsumeForm, isCodeInRange} from '../util.js';
import {SecurityAuthentication} from '../auth/auth.js';


import { CreateInvitationInputBody } from '../models/CreateInvitationInputBody.js';
import { CreateInvitationResponse } from '../models/CreateInvitationResponse.js';
import { ErrorEnvelope } from '../models/ErrorEnvelope.js';
import { MemberView } from '../models/MemberView.js';
import { PageInvitationView } from '../models/PageInvitationView.js';
import { PageMemberView } from '../models/PageMemberView.js';
import { PageWorkspaceView } from '../models/PageWorkspaceView.js';
import { RenameWorkspaceInputBody } from '../models/RenameWorkspaceInputBody.js';
import { SetMemberRoleInputBody } from '../models/SetMemberRoleInputBody.js';
import { WorkspaceView } from '../models/WorkspaceView.js';

/**
 * no description
 */
export class WorkspacesApiRequestFactory extends BaseAPIRequestFactory {

    /**
     * Accept an invitation. Requires the signed-in user\'s email to match the invited email. Idempotent (a second accept by the already-joined user returns 200). A revoked/expired/torn-down invitation returns 410.
     * Accept an invitation
     * @param token 
     */
    public async acceptInvitation(token: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'token' is not null or undefined
        if (token === null || token === undefined) {
            throw new RequiredError("WorkspacesApi", "acceptInvitation", "token");
        }


        // Path Params
        const localVarPath = '/v1/invitations/{token}/accept'
            .replace('{' + 'token' + '}', encodeURIComponent(String(token)));

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
     * Invite an email to join with a role. Sends an accept link. Inviting an existing member returns 409 already_member (use PATCH …/members to change a role). Rate-limited. Admin only.
     * Invite a member
     * @param id 
     * @param createInvitationInputBody 
     * @param xE2AWorkspace Active workspace id (ws_…). Session-only selector: chooses which of your workspaces this request acts in. Ignored for API-key / OAuth credentials, where the workspace is intrinsic to the credential.
     */
    public async createInvitation(id: string, createInvitationInputBody: CreateInvitationInputBody, xE2AWorkspace?: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("WorkspacesApi", "createInvitation", "id");
        }


        // verify required parameter 'createInvitationInputBody' is not null or undefined
        if (createInvitationInputBody === null || createInvitationInputBody === undefined) {
            throw new RequiredError("WorkspacesApi", "createInvitation", "createInvitationInputBody");
        }



        // Path Params
        const localVarPath = '/v1/workspaces/{id}/invitations'
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.POST);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Header Params
        requestContext.setHeaderParam("X-E2A-Workspace", ObjectSerializer.serialize(xE2AWorkspace, "string", ""));


        // Body Params
        const contentType = ObjectSerializer.getPreferredMediaType([
            "application/json"
        ]);
        requestContext.setHeaderParam("Content-Type", contentType);
        const serializedBody = ObjectSerializer.stringify(
            ObjectSerializer.serialize(createInvitationInputBody, "CreateInvitationInputBody", ""),
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
     * A workspace by id, with your role. Any live member.
     * Get a workspace
     * @param id 
     * @param xE2AWorkspace Active workspace id (ws_…). Session-only selector: chooses which of your workspaces this request acts in. Ignored for API-key / OAuth credentials, where the workspace is intrinsic to the credential.
     */
    public async getWorkspace(id: string, xE2AWorkspace?: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("WorkspacesApi", "getWorkspace", "id");
        }



        // Path Params
        const localVarPath = '/v1/workspaces/{id}'
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.GET);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Header Params
        requestContext.setHeaderParam("X-E2A-Workspace", ObjectSerializer.serialize(xE2AWorkspace, "string", ""));


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
     * Pending invitations for the workspace. Admin only.
     * List pending invitations
     * @param id 
     * @param xE2AWorkspace Active workspace id (ws_…). Session-only selector: chooses which of your workspaces this request acts in. Ignored for API-key / OAuth credentials, where the workspace is intrinsic to the credential.
     */
    public async listInvitations(id: string, xE2AWorkspace?: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("WorkspacesApi", "listInvitations", "id");
        }



        // Path Params
        const localVarPath = '/v1/workspaces/{id}/invitations'
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.GET);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Header Params
        requestContext.setHeaderParam("X-E2A-Workspace", ObjectSerializer.serialize(xE2AWorkspace, "string", ""));


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
     * Members and their roles. Any live member.
     * List workspace members
     * @param id 
     * @param xE2AWorkspace Active workspace id (ws_…). Session-only selector: chooses which of your workspaces this request acts in. Ignored for API-key / OAuth credentials, where the workspace is intrinsic to the credential.
     */
    public async listMembers(id: string, xE2AWorkspace?: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("WorkspacesApi", "listMembers", "id");
        }



        // Path Params
        const localVarPath = '/v1/workspaces/{id}/members'
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.GET);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Header Params
        requestContext.setHeaderParam("X-E2A-Workspace", ObjectSerializer.serialize(xE2AWorkspace, "string", ""));


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
     * Every workspace you are a live member of, each annotated with your role. Your personal (default) workspace sorts first.
     * List my workspaces
     */
    public async listWorkspaces(_options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // Path Params
        const localVarPath = '/v1/workspaces';

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
     * Remove a member, or leave the workspace by targeting yourself. Cannot remove the last admin. Admin (or self for a leave).
     * Remove a member (or leave)
     * @param id 
     * @param userId 
     * @param xE2AWorkspace Active workspace id (ws_…). Session-only selector: chooses which of your workspaces this request acts in. Ignored for API-key / OAuth credentials, where the workspace is intrinsic to the credential.
     */
    public async removeMember(id: string, userId: string, xE2AWorkspace?: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("WorkspacesApi", "removeMember", "id");
        }


        // verify required parameter 'userId' is not null or undefined
        if (userId === null || userId === undefined) {
            throw new RequiredError("WorkspacesApi", "removeMember", "userId");
        }



        // Path Params
        const localVarPath = '/v1/workspaces/{id}/members/{user_id}'
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)))
            .replace('{' + 'user_id' + '}', encodeURIComponent(String(userId)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.DELETE);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Header Params
        requestContext.setHeaderParam("X-E2A-Workspace", ObjectSerializer.serialize(xE2AWorkspace, "string", ""));


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
     * Change a workspace\'s display name (e.g. \"Josh\'s Workspace\" → \"Acme\"). Admin only; reachable only through a human session.
     * Rename a workspace
     * @param id 
     * @param renameWorkspaceInputBody 
     * @param xE2AWorkspace Active workspace id (ws_…). Session-only selector: chooses which of your workspaces this request acts in. Ignored for API-key / OAuth credentials, where the workspace is intrinsic to the credential.
     */
    public async renameWorkspace(id: string, renameWorkspaceInputBody: RenameWorkspaceInputBody, xE2AWorkspace?: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("WorkspacesApi", "renameWorkspace", "id");
        }


        // verify required parameter 'renameWorkspaceInputBody' is not null or undefined
        if (renameWorkspaceInputBody === null || renameWorkspaceInputBody === undefined) {
            throw new RequiredError("WorkspacesApi", "renameWorkspace", "renameWorkspaceInputBody");
        }



        // Path Params
        const localVarPath = '/v1/workspaces/{id}'
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.PATCH);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Header Params
        requestContext.setHeaderParam("X-E2A-Workspace", ObjectSerializer.serialize(xE2AWorkspace, "string", ""));


        // Body Params
        const contentType = ObjectSerializer.getPreferredMediaType([
            "application/json"
        ]);
        requestContext.setHeaderParam("Content-Type", contentType);
        const serializedBody = ObjectSerializer.stringify(
            ObjectSerializer.serialize(renameWorkspaceInputBody, "RenameWorkspaceInputBody", ""),
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
     * Revoke a pending invitation; its accept link stops working. Admin only.
     * Revoke a pending invitation
     * @param id 
     * @param invitationId 
     * @param xE2AWorkspace Active workspace id (ws_…). Session-only selector: chooses which of your workspaces this request acts in. Ignored for API-key / OAuth credentials, where the workspace is intrinsic to the credential.
     */
    public async revokeInvitation(id: string, invitationId: string, xE2AWorkspace?: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("WorkspacesApi", "revokeInvitation", "id");
        }


        // verify required parameter 'invitationId' is not null or undefined
        if (invitationId === null || invitationId === undefined) {
            throw new RequiredError("WorkspacesApi", "revokeInvitation", "invitationId");
        }



        // Path Params
        const localVarPath = '/v1/workspaces/{id}/invitations/{invitation_id}'
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)))
            .replace('{' + 'invitation_id' + '}', encodeURIComponent(String(invitationId)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.DELETE);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Header Params
        requestContext.setHeaderParam("X-E2A-Workspace", ObjectSerializer.serialize(xE2AWorkspace, "string", ""));


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
     * Promote to admin or demote to member. Promotion is the transfer-admin mechanism (admins are peers). Cannot demote the last admin. Admin only.
     * Set a member\'s role
     * @param id 
     * @param userId 
     * @param setMemberRoleInputBody 
     * @param xE2AWorkspace Active workspace id (ws_…). Session-only selector: chooses which of your workspaces this request acts in. Ignored for API-key / OAuth credentials, where the workspace is intrinsic to the credential.
     */
    public async setMemberRole(id: string, userId: string, setMemberRoleInputBody: SetMemberRoleInputBody, xE2AWorkspace?: string, _options?: Configuration): Promise<RequestContext> {
        let _config = _options || this.configuration;

        // verify required parameter 'id' is not null or undefined
        if (id === null || id === undefined) {
            throw new RequiredError("WorkspacesApi", "setMemberRole", "id");
        }


        // verify required parameter 'userId' is not null or undefined
        if (userId === null || userId === undefined) {
            throw new RequiredError("WorkspacesApi", "setMemberRole", "userId");
        }


        // verify required parameter 'setMemberRoleInputBody' is not null or undefined
        if (setMemberRoleInputBody === null || setMemberRoleInputBody === undefined) {
            throw new RequiredError("WorkspacesApi", "setMemberRole", "setMemberRoleInputBody");
        }



        // Path Params
        const localVarPath = '/v1/workspaces/{id}/members/{user_id}'
            .replace('{' + 'id' + '}', encodeURIComponent(String(id)))
            .replace('{' + 'user_id' + '}', encodeURIComponent(String(userId)));

        // Make Request Context
        const requestContext = _config.baseServer.makeRequestContext(localVarPath, HttpMethod.PATCH);
        requestContext.setHeaderParam("Accept", "application/json, */*;q=0.8")

        // Header Params
        requestContext.setHeaderParam("X-E2A-Workspace", ObjectSerializer.serialize(xE2AWorkspace, "string", ""));


        // Body Params
        const contentType = ObjectSerializer.getPreferredMediaType([
            "application/json"
        ]);
        requestContext.setHeaderParam("Content-Type", contentType);
        const serializedBody = ObjectSerializer.stringify(
            ObjectSerializer.serialize(setMemberRoleInputBody, "SetMemberRoleInputBody", ""),
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

export class WorkspacesApiResponseProcessor {

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to acceptInvitation
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async acceptInvitationWithHttpInfo(response: ResponseContext): Promise<HttpInfo<WorkspaceView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: WorkspaceView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "WorkspaceView", ""
            ) as WorkspaceView;
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
            const body: WorkspaceView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "WorkspaceView", ""
            ) as WorkspaceView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to createInvitation
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async createInvitationWithHttpInfo(response: ResponseContext): Promise<HttpInfo<CreateInvitationResponse >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("201", response.httpStatusCode)) {
            const body: CreateInvitationResponse = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "CreateInvitationResponse", ""
            ) as CreateInvitationResponse;
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
            const body: CreateInvitationResponse = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "CreateInvitationResponse", ""
            ) as CreateInvitationResponse;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to getWorkspace
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async getWorkspaceWithHttpInfo(response: ResponseContext): Promise<HttpInfo<WorkspaceView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: WorkspaceView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "WorkspaceView", ""
            ) as WorkspaceView;
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
            const body: WorkspaceView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "WorkspaceView", ""
            ) as WorkspaceView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to listInvitations
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async listInvitationsWithHttpInfo(response: ResponseContext): Promise<HttpInfo<PageInvitationView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: PageInvitationView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "PageInvitationView", ""
            ) as PageInvitationView;
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
            const body: PageInvitationView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "PageInvitationView", ""
            ) as PageInvitationView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to listMembers
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async listMembersWithHttpInfo(response: ResponseContext): Promise<HttpInfo<PageMemberView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: PageMemberView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "PageMemberView", ""
            ) as PageMemberView;
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
            const body: PageMemberView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "PageMemberView", ""
            ) as PageMemberView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to listWorkspaces
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async listWorkspacesWithHttpInfo(response: ResponseContext): Promise<HttpInfo<PageWorkspaceView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: PageWorkspaceView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "PageWorkspaceView", ""
            ) as PageWorkspaceView;
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
            const body: PageWorkspaceView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "PageWorkspaceView", ""
            ) as PageWorkspaceView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to removeMember
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async removeMemberWithHttpInfo(response: ResponseContext): Promise<HttpInfo<void >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("204", response.httpStatusCode)) {
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, undefined);
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
            const body: void = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "void", ""
            ) as void;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to renameWorkspace
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async renameWorkspaceWithHttpInfo(response: ResponseContext): Promise<HttpInfo<WorkspaceView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: WorkspaceView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "WorkspaceView", ""
            ) as WorkspaceView;
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
            const body: WorkspaceView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "WorkspaceView", ""
            ) as WorkspaceView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to revokeInvitation
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async revokeInvitationWithHttpInfo(response: ResponseContext): Promise<HttpInfo<void >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("204", response.httpStatusCode)) {
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, undefined);
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
            const body: void = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "void", ""
            ) as void;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

    /**
     * Unwraps the actual response sent by the server from the response context and deserializes the response content
     * to the expected objects
     *
     * @params response Response returned by the server for a request to setMemberRole
     * @throws ApiException if the response code was not in [200, 299]
     */
     public async setMemberRoleWithHttpInfo(response: ResponseContext): Promise<HttpInfo<MemberView >> {
        const contentType = ObjectSerializer.normalizeMediaType(response.headers["content-type"]);
        if (isCodeInRange("200", response.httpStatusCode)) {
            const body: MemberView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "MemberView", ""
            ) as MemberView;
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
            const body: MemberView = ObjectSerializer.deserialize(
                ObjectSerializer.parse(await response.body.text(), contentType),
                "MemberView", ""
            ) as MemberView;
            return new HttpInfo(response.httpStatusCode, response.headers, response.body, body);
        }

        throw new ApiException<string | Blob | undefined>(response.httpStatusCode, "Unknown API Status Code!", await response.getBodyAsAny(), response.headers);
    }

}
