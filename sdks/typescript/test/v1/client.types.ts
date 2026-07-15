import type { FieldError } from "../../src/v1/generated/models/FieldError.js";
import type { ListMessagesParams } from "../../src/v1/index.js";

const senderFilter: ListMessagesParams = { from_: "alice@example.com" };
void senderFilter;

// The pre-GA breaking rename intentionally removes the old public spelling.
// @ts-expect-error `from` is the wire name; SDK callers use `from_`.
const removedSenderFilter: ListMessagesParams = { from: "alice@example.com" };
void removedSenderFilter;

const validationField: FieldError = { location: "", message: "invalid request" };
void validationField;

// @ts-expect-error location is required by the GA validation contract.
const missingValidationLocation: FieldError = { message: "invalid request" };
void missingValidationLocation;
