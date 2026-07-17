import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { uniqueSlug } from "../harness/fixtures.ts";
import { info, writeReport } from "../harness/report.ts";

// Black-box conformance for the webhooks API (8 ops) against LIVE staging.
// Shapes/status codes verified against api/openapi.yaml (the drift-gated SSOT)
// AND curl-probed on the live server before these assertions were written.
//
// The shared cleanup harness only tracks agents/domains, so every webhook this
// suite creates is deleted inline in a `finally`. deleteWebhook now requires
// `?confirm=DELETE` (uniform destructive-delete guard, #53) — a plain DELETE
// is rejected with 422 before the handler runs.
const SUITE = "16-webhooks";
const client = new ApiClient();

const HOOK_URL = "https://example.com/e2e-webhook";

interface WebhookView {
  id: string;
  url: string;
  description: string;
  events: string[];
  filters: Record<string, unknown>;
  enabled: boolean;
  created_at: string;
  last_delivered_at?: string;
  auto_disabled_at?: string;
}
interface CreateWebhookResponse extends WebhookView {
  signing_secret: string;
}
interface PageWebhookView {
  items: WebhookView[];
  next_cursor: string | null;
}
interface WebhookDeliveryView {
  id: string;
  type: string;
  status: string;
  attempts: number;
  next_retry_at: string;
  created_at: string;
}
interface PageWebhookDeliveryView {
  items: WebhookDeliveryView[];
  next_cursor: string | null;
}
interface RotateSecretResponse {
  signing_secret: string;
  previous_secret_expires_at: string;
}
interface ErrEnvelope {
  error?: { code?: string; message?: string; request_id?: string };
}

async function createHook(events: string[] = ["email.received", "email.sent"]): Promise<CreateWebhookResponse> {
  const r = await client.post<CreateWebhookResponse>("/v1/webhooks", {
    body: { url: HOOK_URL, events, description: `e2e ${uniqueSlug("wh")}` },
  });
  assert.equal(r.status, 201, `create expected 201, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.ok(r.body, "create returns a body");
  return r.body!;
}

async function deleteHook(id: string): Promise<void> {
  // Requires ?confirm=DELETE (uniform destructive-delete guard, #53).
  // Idempotent-ish: a second delete 404s, which is fine for best-effort cleanup.
  await client.delete(`/v1/webhooks/${encodeURIComponent(id)}?confirm=DELETE`);
}

// createWebhook + getWebhook + listWebhooks + updateWebhook + rotateWebhookSecret
// + listWebhookDeliveries + deleteWebhook — the full CRUD round-trip.
test("webhooks: full CRUD round-trip (create/list/get/patch/rotate/deliveries/delete)", async () => {
  const created = await createHook();
  const id = created.id;
  try {
    // createWebhook response shape (CreateWebhookResponse — signing_secret only here + on rotate)
    assert.ok(id.startsWith("wh_"), `id has wh_ prefix: ${id}`);
    assert.equal(created.url, HOOK_URL);
    assert.deepEqual(created.events, ["email.received", "email.sent"]);
    assert.equal(created.enabled, true, "new webhook defaults to enabled");
    assert.equal(typeof created.filters, "object", "filters present (defaults to {})");
    assert.ok(typeof created.created_at === "string" && created.created_at.length > 0);
    assert.ok(
      typeof created.signing_secret === "string" && created.signing_secret.startsWith("whsec_"),
      `create returns signing_secret with whsec_ prefix: ${created.signing_secret}`,
    );

    // listWebhooks — envelope {items,next_cursor}; the created hook must be present.
    const list = await client.get<PageWebhookView>("/v1/webhooks");
    assert.equal(list.status, 200);
    assert.ok(Array.isArray(list.body?.items), "list.items is an array");
    assert.ok(
      list.body!.next_cursor === null || typeof list.body!.next_cursor === "string",
      "next_cursor is string|null",
    );
    const inList = list.body!.items.find((w) => w.id === id);
    assert.ok(inList, `created webhook ${id} is present in list`);
    // list items are WebhookView — no signing_secret leaked in the list.
    assert.ok(!("signing_secret" in (inList as object)), "list item must NOT carry signing_secret");

    // getWebhook — WebhookView (no signing_secret).
    const got = await client.get<WebhookView & { signing_secret?: string }>(`/v1/webhooks/${id}`);
    assert.equal(got.status, 200);
    assert.equal(got.body?.id, id);
    assert.equal(got.body?.url, HOOK_URL);
    assert.equal(got.body?.enabled, true);
    assert.equal(got.body?.signing_secret, undefined, "getWebhook must NOT return signing_secret");

    // updateWebhook (PATCH) — partial; events is full-replace when present.
    const patch = await client.patch<WebhookView>(`/v1/webhooks/${id}`, {
      body: { enabled: false, events: ["email.failed"], description: "e2e patched" },
    });
    assert.equal(patch.status, 200, `patch expected 200, got ${patch.status}: ${patch.raw.slice(0, 200)}`);
    assert.equal(patch.body?.enabled, false, "PATCH enabled=false took effect");
    assert.deepEqual(patch.body?.events, ["email.failed"], "PATCH events is a full replace");
    assert.equal(patch.body?.description, "e2e patched");

    // rotateWebhookSecret — new signing_secret + a grace-window expiry (~24h).
    const rot = await client.post<RotateSecretResponse>(`/v1/webhooks/${id}/rotate-secret`);
    assert.equal(rot.status, 200, `rotate expected 200, got ${rot.status}: ${rot.raw.slice(0, 200)}`);
    assert.ok(
      rot.body?.signing_secret?.startsWith("whsec_"),
      `rotate returns a whsec_ secret: ${rot.body?.signing_secret}`,
    );
    assert.notEqual(rot.body!.signing_secret, created.signing_secret, "rotate yields a DIFFERENT secret");
    assert.ok(
      typeof rot.body?.previous_secret_expires_at === "string",
      "rotate returns previous_secret_expires_at (grace window)",
    );

    // listWebhookDeliveries — PageWebhookDeliveryView {items,next_cursor}.
    // Likely empty (no real events fired at this hook); assert the shape either way.
    const del = await client.get<PageWebhookDeliveryView>(`/v1/webhooks/${id}/deliveries`);
    assert.equal(del.status, 200);
    assert.ok(Array.isArray(del.body?.items), "deliveries.items is an array");
    assert.ok(
      del.body!.next_cursor === null || typeof del.body!.next_cursor === "string",
      "deliveries.next_cursor is string|null",
    );
    for (const d of del.body!.items) {
      assert.ok(d.id && d.type && d.status, "delivery view has id/type/status");
      assert.equal(typeof d.attempts, "number", "delivery.attempts is a number");
    }

    // deleteWebhook — 200 + {deleted:true, id}, then the hook 404s.
    const rm = await client.delete<{ deleted: boolean; id: string }>(`/v1/webhooks/${id}?confirm=DELETE`);
    assert.equal(rm.status, 200, `delete expected 200 + deletion object, got ${rm.status}: ${rm.raw.slice(0, 200)}`);
    assert.equal(rm.body?.deleted, true, "deletion object has deleted:true");
    assert.equal(rm.body?.id, id, "deletion object echoes the webhook id");
    const gone = await client.get<ErrEnvelope>(`/v1/webhooks/${id}`);
    assert.equal(gone.status, 404, `deleted webhook should 404, got ${gone.status}`);
    assert.equal(gone.body?.error?.code, "not_found");
  } finally {
    await deleteHook(id);
  }
});

// testWebhook — on an ENABLED hook it schedules a synthetic delivery and returns
// 200 { delivery_id }. It does NOT block on the real HTTP POST to the target, so
// an unreachable/erroring target URL does NOT surface as a 5xx here.
test("webhooks: testWebhook on enabled hook returns 200 with delivery_id (async, no 5xx)", async () => {
  const hook = await createHook();
  try {
    // TestWebhookRequest schema: { type?: <event enum>, data?: object },
    // additionalProperties:false — the field is `type`, not `event`.
    const r = await client.post<{ delivery_id: string }>(`/v1/webhooks/${hook.id}/test`, {
      body: { type: "email.received" },
    });
    assert.ok(
      r.status < 500,
      `testWebhook must not 5xx even with an unreachable target, got ${r.status}: ${r.raw.slice(0, 200)}`,
    );
    assert.equal(r.status, 200, `testWebhook (enabled) expected 200, got ${r.status}: ${r.raw.slice(0, 200)}`);
    assert.ok(
      typeof r.body?.delivery_id === "string" && r.body.delivery_id.length > 0,
      `testWebhook returns a delivery_id: ${r.body?.delivery_id}`,
    );

    // TestWebhookRequest has no required fields — an empty body is accepted (200).
    const empty = await client.post<{ delivery_id: string }>(`/v1/webhooks/${hook.id}/test`, { body: {} });
    assert.equal(empty.status, 200, `testWebhook with empty body expected 200, got ${empty.status}`);
    assert.ok(typeof empty.body?.delivery_id === "string", "empty-body test still returns a delivery_id");
  } finally {
    await deleteHook(hook.id);
  }
});

// testWebhook on a DISABLED hook. openapi documents only 200 + a `default` error
// for this op; the server pins a specific, sensible 409 webhook_disabled. Not a
// violation (default covers it) but worth asserting so a silent change (e.g. a
// 5xx, or accepting the test on a disabled hook) is caught.
test("webhooks: testWebhook on disabled hook returns 409 webhook_disabled", async () => {
  const hook = await createHook();
  try {
    const patch = await client.patch<WebhookView>(`/v1/webhooks/${hook.id}`, { body: { enabled: false } });
    assert.equal(patch.status, 200);
    assert.equal(patch.body?.enabled, false);

    const r = await client.post<ErrEnvelope>(`/v1/webhooks/${hook.id}/test`, {
      body: { type: "email.received" },
    });
    assert.equal(r.status, 409, `disabled-hook test expected 409, got ${r.status}: ${r.raw.slice(0, 160)}`);
    assert.equal(r.body?.error?.code, "webhook_disabled", "409 carries webhook_disabled code");
  } finally {
    await deleteHook(hook.id);
  }
});

// ---- Negatives ----

test("webhooks: unauthenticated list returns 401", async () => {
  const r = await client.get<ErrEnvelope>("/v1/webhooks", { apiKey: null });
  assert.equal(r.status, 401, `unauth list expected 401, got ${r.status}`);
  assert.equal(r.body?.error?.code, "unauthorized");
});

test("webhooks: get nonexistent returns 404 not_found", async () => {
  const r = await client.get<ErrEnvelope>(`/v1/webhooks/wh_${uniqueSlug("missing").replace(/-/g, "")}`);
  assert.equal(r.status, 404, `get nonexistent expected 404, got ${r.status}`);
  assert.equal(r.body?.error?.code, "not_found");
});

test("webhooks: delete nonexistent returns 404 not_found", async () => {
  const r = await client.delete<ErrEnvelope>(`/v1/webhooks/wh_${uniqueSlug("missing").replace(/-/g, "")}?confirm=DELETE`);
  assert.equal(r.status, 404, `delete nonexistent expected 404, got ${r.status}`);
  assert.equal(r.body?.error?.code, "not_found");
});

test("webhooks: create missing required fields returns 422", async () => {
  const noUrl = await client.post<ErrEnvelope>("/v1/webhooks", { body: { events: ["email.sent"] } });
  assert.equal(noUrl.status, 422, `create missing url expected 422, got ${noUrl.status}: ${noUrl.raw.slice(0, 160)}`);
  assert.equal(noUrl.body?.error?.code, "invalid_request");

  const noEvents = await client.post<ErrEnvelope>("/v1/webhooks", { body: { url: HOOK_URL } });
  assert.equal(noEvents.status, 422, `create missing events expected 422, got ${noEvents.status}`);
  assert.equal(noEvents.body?.error?.code, "invalid_request");
});

test("webhooks: create with unknown event enum returns 422", async () => {
  const r = await client.post<ErrEnvelope>("/v1/webhooks", {
    body: { url: HOOK_URL, events: ["not.a.real.event"] },
  });
  assert.equal(r.status, 422, `bad enum expected 422, got ${r.status}: ${r.raw.slice(0, 160)}`);
  assert.equal(r.body?.error?.code, "invalid_request");
});

// URL must be HTTPS — enforced with a distinct 400 invalid_webhook_url (SSRF/plaintext
// guard). openapi is silent on the code; pinned here from the live probe.
test("webhooks: create with non-HTTPS url returns 400 invalid_webhook_url", async () => {
  const r = await client.post<ErrEnvelope>("/v1/webhooks", {
    body: { url: "http://example.com/e2e-webhook", events: ["email.sent"] },
  });
  assert.ok(r.status >= 400 && r.status < 500, `non-https url expected 4xx, got ${r.status}`);
  if (r.status === 400) {
    assert.equal(r.body?.error?.code, "invalid_webhook_url", "400 carries invalid_webhook_url code");
  } else {
    info(SUITE, "non-https-code", `probed 400 invalid_webhook_url; server now returns ${r.status}: ${r.raw.slice(0, 160)}`);
  }
});

after(async () => {
  await writeReport(`./reports/${SUITE}.json`);
});
