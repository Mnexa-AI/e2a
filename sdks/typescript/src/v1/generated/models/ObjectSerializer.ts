export * from '../models/APIKeyExportEntry.js';
export * from '../models/APIKeyView.js';
export * from '../models/AccountUserView.js';
export * from '../models/AccountView.js';
export * from '../models/AgentIdentity.js';
export * from '../models/AgentView.js';
export * from '../models/ApproveRequest.js';
export * from '../models/Attachment.js';
export * from '../models/AttachmentMetaView.js';
export * from '../models/AttachmentView.js';
export * from '../models/AuthVerdict.js';
export * from '../models/CheckResult.js';
export * from '../models/ConversationDetailView.js';
export * from '../models/ConversationSummaryView.js';
export * from '../models/CreateAPIKeyRequest.js';
export * from '../models/CreateAPIKeyResponse.js';
export * from '../models/CreateAgentRequest.js';
export * from '../models/CreateWebhookRequest.js';
export * from '../models/CreateWebhookResponse.js';
export * from '../models/DNSRecordView.js';
export * from '../models/DNSRecordsView.js';
export * from '../models/DeleteUserDataResult.js';
export * from '../models/DeliveryStatusJSON.js';
export * from '../models/DeploymentInfoView.js';
export * from '../models/Domain.js';
export * from '../models/DomainView.js';
export * from '../models/ErrorBody.js';
export * from '../models/ErrorEnvelope.js';
export * from '../models/EventJSON.js';
export * from '../models/ForwardRequest.js';
export * from '../models/LimitsCapsView.js';
export * from '../models/LimitsUsageView.js';
export * from '../models/Message.js';
export * from '../models/MessageBodyView.js';
export * from '../models/MessageParsedView.js';
export * from '../models/MessageSummaryView.js';
export * from '../models/MessageView.js';
export * from '../models/OAuthConnectionEntry.js';
export * from '../models/PageAPIKeyView.js';
export * from '../models/PageAgentView.js';
export * from '../models/PageConversationSummaryView.js';
export * from '../models/PageDomainView.js';
export * from '../models/PageEventJSON.js';
export * from '../models/PageMessageSummaryView.js';
export * from '../models/PageSuppression.js';
export * from '../models/PageWebhookDeliveryView.js';
export * from '../models/PageWebhookView.js';
export * from '../models/ProtectionConfigView.js';
export * from '../models/ProtectionDirectionView.js';
export * from '../models/ProtectionEventExportEntry.js';
export * from '../models/ProtectionGateView.js';
export * from '../models/ProtectionHoldsView.js';
export * from '../models/ProtectionScanView.js';
export * from '../models/RedeliverDelivery.js';
export * from '../models/RedeliverEventRequest.js';
export * from '../models/RedeliverView.js';
export * from '../models/RegisterDomainRequest.js';
export * from '../models/RejectRequest.js';
export * from '../models/RejectResultView.js';
export * from '../models/ReplyRequest.js';
export * from '../models/Result.js';
export * from '../models/RotateSecretResponse.js';
export * from '../models/SendEmailRequest.js';
export * from '../models/SendResultView.js';
export * from '../models/SendingDNSRecordView.js';
export * from '../models/Suppression.js';
export * from '../models/SuppressionExportEntry.js';
export * from '../models/TestWebhookRequest.js';
export * from '../models/TestWebhookResponse.js';
export * from '../models/UpdateAgentRequest.js';
export * from '../models/UpdateDomainRequest.js';
export * from '../models/UpdateMessageRequest.js';
export * from '../models/UpdateMessageResultView.js';
export * from '../models/UpdateWebhookRequest.js';
export * from '../models/UsageEventEntry.js';
export * from '../models/UserExport.js';
export * from '../models/UserExportUser.js';
export * from '../models/VerifyDomainView.js';
export * from '../models/WebhookDeliveryView.js';
export * from '../models/WebhookFiltersView.js';
export * from '../models/WebhookView.js';

import { APIKeyExportEntry } from '../models/APIKeyExportEntry.js';
import { APIKeyView       , APIKeyViewScopeEnum   } from '../models/APIKeyView.js';
import { AccountUserView } from '../models/AccountUserView.js';
import { AccountView   , AccountViewScopeEnum      } from '../models/AccountView.js';
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
import { CreateAPIKeyRequest   , CreateAPIKeyRequestScopeEnum   } from '../models/CreateAPIKeyRequest.js';
import { CreateAPIKeyResponse        , CreateAPIKeyResponseScopeEnum   } from '../models/CreateAPIKeyResponse.js';
import { CreateAgentRequest } from '../models/CreateAgentRequest.js';
import { CreateWebhookRequest , CreateWebhookRequestEventsEnum     } from '../models/CreateWebhookRequest.js';
import { CreateWebhookResponse    , CreateWebhookResponseEventsEnum        } from '../models/CreateWebhookResponse.js';
import { DNSRecordView } from '../models/DNSRecordView.js';
import { DNSRecordsView } from '../models/DNSRecordsView.js';
import { DeleteUserDataResult } from '../models/DeleteUserDataResult.js';
import { DeliveryStatusJSON } from '../models/DeliveryStatusJSON.js';
import { DeploymentInfoView } from '../models/DeploymentInfoView.js';
import { Domain } from '../models/Domain.js';
import { DomainView         , DomainViewSendingStatusEnum      } from '../models/DomainView.js';
import { ErrorBody } from '../models/ErrorBody.js';
import { ErrorEnvelope } from '../models/ErrorEnvelope.js';
import { EventJSON        , EventJSONStatusEnum  , EventJSONTypeEnum   } from '../models/EventJSON.js';
import { ForwardRequest } from '../models/ForwardRequest.js';
import { LimitsCapsView } from '../models/LimitsCapsView.js';
import { LimitsUsageView } from '../models/LimitsUsageView.js';
import { Message } from '../models/Message.js';
import { MessageBodyView } from '../models/MessageBodyView.js';
import { MessageParsedView } from '../models/MessageParsedView.js';
import { MessageSummaryView     , MessageSummaryViewDeliveryStatusEnum  , MessageSummaryViewDirectionEnum          , MessageSummaryViewReviewStatusEnum  , MessageSummaryViewSentAsEnum        } from '../models/MessageSummaryView.js';
import { MessageView        , MessageViewDeliveryStatusEnum  , MessageViewDirectionEnum            , MessageViewReviewStatusEnum  , MessageViewSentAsEnum        } from '../models/MessageView.js';
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
import { ProtectionGateView, ProtectionGateViewActionEnum   , ProtectionGateViewPolicyEnum   } from '../models/ProtectionGateView.js';
import { ProtectionHoldsView, ProtectionHoldsViewOnExpiryEnum    } from '../models/ProtectionHoldsView.js';
import { ProtectionScanView, ProtectionScanViewSensitivityEnum   } from '../models/ProtectionScanView.js';
import { RedeliverDelivery  , RedeliverDeliveryStatusEnum    } from '../models/RedeliverDelivery.js';
import { RedeliverEventRequest } from '../models/RedeliverEventRequest.js';
import { RedeliverView   , RedeliverViewStatusEnum    } from '../models/RedeliverView.js';
import { RegisterDomainRequest } from '../models/RegisterDomainRequest.js';
import { RejectRequest } from '../models/RejectRequest.js';
import { RejectResultView } from '../models/RejectResultView.js';
import { ReplyRequest } from '../models/ReplyRequest.js';
import { Result } from '../models/Result.js';
import { RotateSecretResponse } from '../models/RotateSecretResponse.js';
import { SendEmailRequest } from '../models/SendEmailRequest.js';
import { SendResultView   , SendResultViewMethodEnum   , SendResultViewSentAsEnum  , SendResultViewStatusEnum   } from '../models/SendResultView.js';
import { SendingDNSRecordView } from '../models/SendingDNSRecordView.js';
import { Suppression } from '../models/Suppression.js';
import { SuppressionExportEntry } from '../models/SuppressionExportEntry.js';
import { TestWebhookRequest , TestWebhookRequestEventEnum   } from '../models/TestWebhookRequest.js';
import { TestWebhookResponse } from '../models/TestWebhookResponse.js';
import { UpdateAgentRequest } from '../models/UpdateAgentRequest.js';
import { UpdateDomainRequest } from '../models/UpdateDomainRequest.js';
import { UpdateMessageRequest } from '../models/UpdateMessageRequest.js';
import { UpdateMessageResultView } from '../models/UpdateMessageResultView.js';
import { UpdateWebhookRequest  , UpdateWebhookRequestEventsEnum     } from '../models/UpdateWebhookRequest.js';
import { UsageEventEntry } from '../models/UsageEventEntry.js';
import { UserExport } from '../models/UserExport.js';
import { UserExportUser } from '../models/UserExportUser.js';
import { VerifyDomainView } from '../models/VerifyDomainView.js';
import { WebhookDeliveryView  , WebhookDeliveryViewEventTypeEnum       , WebhookDeliveryViewStatusEnum   } from '../models/WebhookDeliveryView.js';
import { WebhookFiltersView } from '../models/WebhookFiltersView.js';
import { WebhookView    , WebhookViewEventsEnum       } from '../models/WebhookView.js';

/* tslint:disable:no-unused-variable */
let primitives = [
                    "string",
                    "boolean",
                    "double",
                    "integer",
                    "long",
                    "float",
                    "number",
                    "any"
                 ];

let enumsMap: Set<string> = new Set<string>([
    "APIKeyViewScopeEnum",
    "AccountViewScopeEnum",
    "CreateAPIKeyRequestScopeEnum",
    "CreateAPIKeyResponseScopeEnum",
    "CreateWebhookRequestEventsEnum",
    "CreateWebhookResponseEventsEnum",
    "DomainViewSendingStatusEnum",
    "EventJSONStatusEnum",
    "EventJSONTypeEnum",
    "MessageSummaryViewDeliveryStatusEnum",
    "MessageSummaryViewDirectionEnum",
    "MessageSummaryViewReviewStatusEnum",
    "MessageSummaryViewSentAsEnum",
    "MessageViewDeliveryStatusEnum",
    "MessageViewDirectionEnum",
    "MessageViewReviewStatusEnum",
    "MessageViewSentAsEnum",
    "ProtectionGateViewActionEnum",
    "ProtectionGateViewPolicyEnum",
    "ProtectionHoldsViewOnExpiryEnum",
    "ProtectionScanViewSensitivityEnum",
    "RedeliverDeliveryStatusEnum",
    "RedeliverViewStatusEnum",
    "SendResultViewMethodEnum",
    "SendResultViewSentAsEnum",
    "SendResultViewStatusEnum",
    "TestWebhookRequestEventEnum",
    "UpdateWebhookRequestEventsEnum",
    "WebhookDeliveryViewEventTypeEnum",
    "WebhookDeliveryViewStatusEnum",
    "WebhookViewEventsEnum",
]);

let typeMap: {[index: string]: any} = {
    "APIKeyExportEntry": APIKeyExportEntry,
    "APIKeyView": APIKeyView,
    "AccountUserView": AccountUserView,
    "AccountView": AccountView,
    "AgentIdentity": AgentIdentity,
    "AgentView": AgentView,
    "ApproveRequest": ApproveRequest,
    "Attachment": Attachment,
    "AttachmentMetaView": AttachmentMetaView,
    "AttachmentView": AttachmentView,
    "AuthVerdict": AuthVerdict,
    "CheckResult": CheckResult,
    "ConversationDetailView": ConversationDetailView,
    "ConversationSummaryView": ConversationSummaryView,
    "CreateAPIKeyRequest": CreateAPIKeyRequest,
    "CreateAPIKeyResponse": CreateAPIKeyResponse,
    "CreateAgentRequest": CreateAgentRequest,
    "CreateWebhookRequest": CreateWebhookRequest,
    "CreateWebhookResponse": CreateWebhookResponse,
    "DNSRecordView": DNSRecordView,
    "DNSRecordsView": DNSRecordsView,
    "DeleteUserDataResult": DeleteUserDataResult,
    "DeliveryStatusJSON": DeliveryStatusJSON,
    "DeploymentInfoView": DeploymentInfoView,
    "Domain": Domain,
    "DomainView": DomainView,
    "ErrorBody": ErrorBody,
    "ErrorEnvelope": ErrorEnvelope,
    "EventJSON": EventJSON,
    "ForwardRequest": ForwardRequest,
    "LimitsCapsView": LimitsCapsView,
    "LimitsUsageView": LimitsUsageView,
    "Message": Message,
    "MessageBodyView": MessageBodyView,
    "MessageParsedView": MessageParsedView,
    "MessageSummaryView": MessageSummaryView,
    "MessageView": MessageView,
    "OAuthConnectionEntry": OAuthConnectionEntry,
    "PageAPIKeyView": PageAPIKeyView,
    "PageAgentView": PageAgentView,
    "PageConversationSummaryView": PageConversationSummaryView,
    "PageDomainView": PageDomainView,
    "PageEventJSON": PageEventJSON,
    "PageMessageSummaryView": PageMessageSummaryView,
    "PageSuppression": PageSuppression,
    "PageWebhookDeliveryView": PageWebhookDeliveryView,
    "PageWebhookView": PageWebhookView,
    "ProtectionConfigView": ProtectionConfigView,
    "ProtectionDirectionView": ProtectionDirectionView,
    "ProtectionEventExportEntry": ProtectionEventExportEntry,
    "ProtectionGateView": ProtectionGateView,
    "ProtectionHoldsView": ProtectionHoldsView,
    "ProtectionScanView": ProtectionScanView,
    "RedeliverDelivery": RedeliverDelivery,
    "RedeliverEventRequest": RedeliverEventRequest,
    "RedeliverView": RedeliverView,
    "RegisterDomainRequest": RegisterDomainRequest,
    "RejectRequest": RejectRequest,
    "RejectResultView": RejectResultView,
    "ReplyRequest": ReplyRequest,
    "Result": Result,
    "RotateSecretResponse": RotateSecretResponse,
    "SendEmailRequest": SendEmailRequest,
    "SendResultView": SendResultView,
    "SendingDNSRecordView": SendingDNSRecordView,
    "Suppression": Suppression,
    "SuppressionExportEntry": SuppressionExportEntry,
    "TestWebhookRequest": TestWebhookRequest,
    "TestWebhookResponse": TestWebhookResponse,
    "UpdateAgentRequest": UpdateAgentRequest,
    "UpdateDomainRequest": UpdateDomainRequest,
    "UpdateMessageRequest": UpdateMessageRequest,
    "UpdateMessageResultView": UpdateMessageResultView,
    "UpdateWebhookRequest": UpdateWebhookRequest,
    "UsageEventEntry": UsageEventEntry,
    "UserExport": UserExport,
    "UserExportUser": UserExportUser,
    "VerifyDomainView": VerifyDomainView,
    "WebhookDeliveryView": WebhookDeliveryView,
    "WebhookFiltersView": WebhookFiltersView,
    "WebhookView": WebhookView,
}

type MimeTypeDescriptor = {
    type: string;
    subtype: string;
    subtypeTokens: string[];
};

/**
 * Every mime-type consists of a type, subtype, and optional parameters.
 * The subtype can be composite, including information about the content format.
 * For example: `application/json-patch+json`, `application/merge-patch+json`.
 *
 * This helper transforms a string mime-type into an internal representation.
 * This simplifies the implementation of predicates that in turn define common rules for parsing or stringifying
 * the payload.
 */
const parseMimeType = (mimeType: string): MimeTypeDescriptor => {
    const [type = '', subtype = ''] = mimeType.split('/');
    return {
        type,
        subtype,
        subtypeTokens: subtype.split('+'),
    };
};

type MimeTypePredicate = (mimeType: string) => boolean;

// This factory creates a predicate function that checks a string mime-type against defined rules.
const mimeTypePredicateFactory = (predicate: (descriptor: MimeTypeDescriptor) => boolean): MimeTypePredicate => (mimeType) => predicate(parseMimeType(mimeType));

// Use this factory when you need to define a simple predicate based only on type and, if applicable, subtype.
const mimeTypeSimplePredicateFactory = (type: string, subtype?: string): MimeTypePredicate => mimeTypePredicateFactory((descriptor) => {
    if (descriptor.type !== type) return false;
    if (subtype != null && descriptor.subtype !== subtype) return false;
    return true;
});

// Creating a set of named predicates that will help us determine how to handle different mime-types
const isTextLikeMimeType = mimeTypeSimplePredicateFactory('text');
const isJsonMimeType = mimeTypeSimplePredicateFactory('application', 'json');
const isJsonLikeMimeType = mimeTypePredicateFactory((descriptor) => descriptor.type === 'application' && descriptor.subtypeTokens.some((item) => item === 'json'));
const isOctetStreamMimeType = mimeTypeSimplePredicateFactory('application', 'octet-stream');
const isFormUrlencodedMimeType = mimeTypeSimplePredicateFactory('application', 'x-www-form-urlencoded');

// Defining a list of mime-types in the order of prioritization for handling.
const supportedMimeTypePredicatesWithPriority: MimeTypePredicate[] = [
    isJsonMimeType,
    isJsonLikeMimeType,
    isTextLikeMimeType,
    isOctetStreamMimeType,
    isFormUrlencodedMimeType,
];

const nullableSuffix = " | null";
const optionalSuffix = " | undefined";
const arrayPrefix = "Array<";
const arraySuffix = ">";
const mapPrefix = "{ [key: string]: ";
const mapSuffix = "; }";

export class ObjectSerializer {
    public static findCorrectType(data: any, expectedType: string) {
        if (data == undefined) {
            return expectedType;
        } else if (primitives.indexOf(expectedType.toLowerCase()) !== -1) {
            return expectedType;
        } else if (expectedType === "Date") {
            return expectedType;
        } else {
            if (enumsMap.has(expectedType)) {
                return expectedType;
            }

            if (!typeMap[expectedType]) {
                return expectedType; // w/e we don't know the type
            }

            // Check the discriminator
            let discriminatorProperty = typeMap[expectedType].discriminator;
            if (discriminatorProperty == null) {
                return expectedType; // the type does not have a discriminator. use it.
            } else {
                if (data[discriminatorProperty]) {
                    var discriminatorType = data[discriminatorProperty];
                    let mapping = typeMap[expectedType].mapping;
                    if (mapping != undefined && mapping[discriminatorType]) {
                        return mapping[discriminatorType]; // use the type given in the discriminator
                    } else if(typeMap[discriminatorType]) {
                        return discriminatorType;
                    } else {
                        return expectedType; // discriminator did not map to a type
                    }
                } else {
                    return expectedType; // discriminator was not present (or an empty string)
                }
            }
        }
    }

    public static serialize(data: any, type: string, format: string): any {
        if (data == undefined) {
            return data;
        } else if (primitives.indexOf(type.toLowerCase()) !== -1) {
            return data;
        } else if (type.endsWith(nullableSuffix)) {
            let subType: string = type.slice(0, -nullableSuffix.length); // Type | null => Type
            return ObjectSerializer.serialize(data, subType, format);
        } else if (type.endsWith(optionalSuffix)) {
            let subType: string = type.slice(0, -optionalSuffix.length); // Type | undefined => Type
            return ObjectSerializer.serialize(data, subType, format);
        } else if (type.startsWith(arrayPrefix)) {
            let subType: string = type.slice(arrayPrefix.length, -arraySuffix.length); // Array<Type> => Type
            let transformedData: any[] = [];
            for (let date of data) {
                transformedData.push(ObjectSerializer.serialize(date, subType, format));
            }
            return transformedData;
        } else if (type.startsWith(mapPrefix)) {
            let subType: string = type.slice(mapPrefix.length, -mapSuffix.length); // { [key: string]: Type; } => Type
            let transformedData: { [key: string]: any } = {};
            for (let key in data) {
                transformedData[key] = ObjectSerializer.serialize(
                    data[key],
                    subType,
                    format,
                );
            }
            return transformedData;
        } else if (type === "Date") {
            if (!(data instanceof Date)) {
                return data;
            }
            if (format == "date") {
                let month = data.getMonth()+1
                let monthStr = month < 10 ? "0" + month.toString() : month.toString()
                let day = data.getDate();
                let dayStr = day < 10 ? "0" + day.toString() : day.toString();

                return data.getFullYear() + "-" + monthStr + "-" + dayStr;
            } else {
                return data.toISOString();
            }
        } else {
            if (enumsMap.has(type)) {
                return data;
            }
            if (!typeMap[type]) { // in case we dont know the type
                return data;
            }

            // Get the actual type of this object
            type = this.findCorrectType(data, type);

            // get the map for the correct type.
            let attributeTypes = typeMap[type].getAttributeTypeMap();
            let instance: {[index: string]: any} = {};
            for (let attributeType of attributeTypes) {
                instance[attributeType.baseName] = ObjectSerializer.serialize(data[attributeType.name], attributeType.type, attributeType.format);
            }
            return instance;
        }
    }

    public static deserialize(data: any, type: string, format: string): any {
        // polymorphism may change the actual type.
        type = ObjectSerializer.findCorrectType(data, type);
        if (data == undefined) {
            return data;
        } else if (primitives.indexOf(type.toLowerCase()) !== -1) {
            return data;
        } else if (type.endsWith(nullableSuffix)) {
            let subType: string = type.slice(0, -nullableSuffix.length); // Type | null => Type
            return ObjectSerializer.deserialize(data, subType, format);
        } else if (type.endsWith(optionalSuffix)) {
            let subType: string = type.slice(0, -optionalSuffix.length); // Type | undefined => Type
            return ObjectSerializer.deserialize(data, subType, format);
        } else if (type.startsWith(arrayPrefix)) {
            let subType: string = type.slice(arrayPrefix.length, -arraySuffix.length); // Array<Type> => Type
            let transformedData: any[] = [];
            for (let date of data) {
                transformedData.push(ObjectSerializer.deserialize(date, subType, format));
            }
            return transformedData;
        } else if (type.startsWith(mapPrefix)) {
            let subType: string = type.slice(mapPrefix.length, -mapSuffix.length); // { [key: string]: Type; } => Type
            let transformedData: { [key: string]: any } = {};
            for (let key in data) {
                transformedData[key] = ObjectSerializer.deserialize(
                    data[key],
                    subType,
                    format,
                );
            }
            return transformedData;
        } else if (type === "Date") {
            return new Date(data);
        } else {
            if (enumsMap.has(type)) {// is Enum
                return data;
            }

            if (!typeMap[type]) { // dont know the type
                return data;
            }
            let instance = new typeMap[type]();
            let attributeTypes = typeMap[type].getAttributeTypeMap();
            for (let attributeType of attributeTypes) {
                let value = ObjectSerializer.deserialize(data[attributeType.baseName], attributeType.type, attributeType.format);
                if (value !== undefined) {
                    instance[attributeType.name] = value;
                }
            }
            return instance;
        }
    }


    /**
     * Normalize media type
     *
     * We currently do not handle any media types attributes, i.e. anything
     * after a semicolon. All content is assumed to be UTF-8 compatible.
     */
    public static normalizeMediaType(mediaType: string | undefined): string | undefined {
        if (mediaType === undefined) {
            return undefined;
        }
        return (mediaType.split(";")[0] ?? '').trim().toLowerCase();
    }

    /**
     * From a list of possible media types, choose the one we can handle best.
     *
     * The order of the given media types does not have any impact on the choice
     * made.
     */
    public static getPreferredMediaType(mediaTypes: Array<string>): string {
        /** According to OAS 3 we should default to json */
        if (mediaTypes.length === 0) {
            return "application/json";
        }

        const normalMediaTypes = mediaTypes.map(ObjectSerializer.normalizeMediaType);

        for (const predicate of supportedMimeTypePredicatesWithPriority) {
            for (const mediaType of normalMediaTypes) {
                if (mediaType != null && predicate(mediaType)) {
                    return mediaType;
                }
            }
        }

        throw new Error("None of the given media types are supported: " + mediaTypes.join(", "));
    }

    /**
     * Convert data to a string according the given media type
     */
    public static stringify(data: any, mediaType: string): string {
        if (isTextLikeMimeType(mediaType)) {
            return String(data);
        }

        if (isJsonLikeMimeType(mediaType)) {
            return JSON.stringify(data);
        }

        throw new Error("The mediaType " + mediaType + " is not supported by ObjectSerializer.stringify.");
    }

    /**
     * Parse data from a string according to the given media type
     */
    public static parse(rawData: string, mediaType: string | undefined) {
        if (mediaType === undefined) {
            throw new Error("Cannot parse content. No Content-Type defined.");
        }

        if (isTextLikeMimeType(mediaType)) {
            return rawData;
        }

        if (isJsonLikeMimeType(mediaType)) {
            return JSON.parse(rawData);
        }

        throw new Error("The mediaType " + mediaType + " is not supported by ObjectSerializer.parse.");
    }
}
