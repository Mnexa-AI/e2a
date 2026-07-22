import { test } from "node:test";
import assert from "node:assert/strict";
import { isEventsLogDisabled } from "./event-capability.ts";

test("event-log skip requires both 501 and the events_log_disabled error code", () => {
  assert.equal(isEventsLogDisabled(501, { error: { code: "events_log_disabled" } }), true);
  assert.equal(isEventsLogDisabled(501, { error: { code: "not_implemented" } }), false);
  assert.equal(isEventsLogDisabled(501, null), false);
  assert.equal(isEventsLogDisabled(500, { error: { code: "events_log_disabled" } }), false);
});
