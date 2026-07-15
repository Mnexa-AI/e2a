import type { McpClient } from "../src/client.js";

type ListMessagesParams = Parameters<McpClient["listMessages"]>[0];

const senderFilter: ListMessagesParams = { from_: "alice@example.com" };
void senderFilter;

// The MCP tool and its wrapper follow the SDK's reserved-word-safe spelling.
// @ts-expect-error `from` is the REST wire name, not the MCP input name.
const removedSenderFilter: ListMessagesParams = { from: "alice@example.com" };
void removedSenderFilter;
