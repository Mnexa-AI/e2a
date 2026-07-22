import assert from "node:assert/strict";
import test from "node:test";

import { assertAdditiveToolCatalog } from "./check-mcp-tool-compat.mjs";

test("rejects a tool removed from the base catalog", () => {
  assert.throws(
    () => assertAdditiveToolCatalog(["get_message", "send_message"], ["send_message"]),
    /removed MCP tool names: get_message/,
  );
});

test("allows tools to be added without removing the base catalog", () => {
  assert.doesNotThrow(() =>
    assertAdditiveToolCatalog(
      ["get_message", "send_message"],
      ["get_message", "list_messages", "send_message"],
    ),
  );
});

test("rejects malformed catalogs before comparing them", () => {
  assert.throws(
    () => assertAdditiveToolCatalog(["send_message", "send_message"], ["send_message"]),
    /base MCP tool catalog must be sorted and unique/,
  );
  assert.throws(
    () => assertAdditiveToolCatalog(["send_message"], ["send_message", 42]),
    /revision MCP tool catalog must contain only strings/,
  );
});
