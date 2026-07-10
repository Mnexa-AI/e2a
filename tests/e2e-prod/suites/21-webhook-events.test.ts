import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { uniqueSlug, uniqueSubject, holdAllOutbound } from "../harness/fixtures.ts";
import { writeReport, info } from "../harness/report.ts";

// Black-box conformance for REAL webhook-event EMISSION against LIVE staging.
// This is the P2 gap the prod e2e suite structurally cannot cover: staging runs
// with the events log ON, prod runs it OFF (list_events → events_log_disabled on
// prod). So this suite ONLY makes sense against staging.
//
// Emission is proved two independent ways for every event type:
//   1. listEvents  (GET /v1/events, filtered type + agent_id + since) — the event
//      row landed in the outbox/log.
//   2. listWebhookDeliveries (GET /v1/webhooks/{id}/deliveries) — the event was
//      fanned out to a subscriber and an HTTP delivery was ATTEMPTED for that
//      event_type. We assert attempts>=1, NOT delivery success: staging has no
//      real webhook sink, so the dummy target (example.com) 405s the POST. A 405
//      (or any last_status_code) still proves the delivery leg ran — which is the
//      emission signal. Requiring a 2xx would test the sink, not e2a.
//
// Shapes/status verified against api/openapi.yaml (the drift-gated SSOT) AND
// curl-probed on live staging before these assertions were written (2026-07-10):
//   EventJSON     required {id,type,schema_version,created_at,status,data};
//                 optional agent_id, conversation_id, message_id, delivery_status.
//   PageEventJSON {items, next_cursor:string|null}.
//   RedeliverView required {event_id,status}; single-webhook replay also carries
//                 top-level delivery_id + webhook_id (status "pending"); bulk
//                 fan-out carries deliveries[] (status "scheduled").
//   WebhookDeliveryView required {id,event_type,status,attempts,next_retry_at,created_at}.
//
// Event types covered (HTTP-triggerable, per internal/webhookpub/event.go):
//   email.sent            — real send (no hold) to the SES simulator (SES 200).
//   email.pending_review  — hold-all-outbound BEFORE send → 202 pending_review.
//   email.review_rejected — reject a held message (clean; no send).
//   email.review_approved — approve a held message addressed to the simulator
//                           (approve→send succeeds; a non-simulator recipient
//                           500s on staging's SES sandbox).
//
// Event types SKIPPED with reasons (not HTTP-triggerable on staging here):
//   email.received  — needs a real inbound SMTP delivery; that is the prober's
//                     dedicated round-trip, not an API-driven trigger.
//   email.blocked   — needs a screening `block` gate/scan config to refuse a
//                     message; out of scope for the emission battery.
//   email.delivered/bounced/complained — async SES delivery-feedback, arrives via
//                     SNS on an unbounded timeline (and the simulator's feedback
//                     is not deterministic within a test window).
//   email.failed/deferred — terminal/transient async-send outcomes; only the
//                     primary signal under E2A_OUTBOUND_MODE=async.
//   domain.sending_verified/failed, domain.suppression_added — need real
//                     sending-identity provisioning against a custom domain.
//
// Ops exercised: listEvents (envelope + filters), getEvent (+404), redeliverEvent
// (re-queues a delivery; a new attempt appears). Every agent + webhook created is
// deleted inline in a finally (agent delete cascades to held messages; we also
// resolve holds explicitly). The shared cleanup harness is not used (it only
// tracks agents), and per the task the harness/ is never edited.
const SUITE = "21-webhook-events";
const client = new ApiClient();

// Real, deliverable recipient: SES accepts + 200s it and drops it (no real
// mailbox), so email.sent / review_approved actually reach the "sent" state.
const SIMULATOR = "success@simulator.amazonses.com";

interface EventJSON {
  id: string;
  type: string;
  schema_version: number;
  created_at: string;
  status: string;
  data: Record<string, unknown>;
  agent_id?: string;
  conversation_id?: string;
  message_id?: string;
  delivery_status?: { matched_webhooks?: number; delivered?: number; pending?: number; failed?: number };
}
interface PageEventJSON {
  items: EventJSON[];
  next_cursor: string | null;
}
interface WebhookDeliveryView {
  id: string;
  event_type: string;
  status: string;
  attempts: number;
  next_retry_at: string;
  created_at: string;
  last_status_code?: number;
  last_error?: string;
  last_attempt_at?: string;
}
interface PageWebhookDeliveryView {
  items: WebhookDeliveryView[];
  next_cursor: string | null;
}
interface CreateWebhookResponse {
  id: string;
  url: string;
  events: string[];
  enabled: boolean;
  signing_secret: string;
}
interface RedeliverView {
  event_id: string;
  status: string;
  delivery_id?: string;
  webhook_id?: string;
  deliveries?: Array<{ webhook_id?: string; delivery_id?: string; status?: string }>;
}
interface SendResult {
  status?: string;
  message_id?: string;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

async function createAgent(label: string, hold = false): Promise<string> {
  const slug = uniqueSlug(label);
  const c = await client.post<{ email: string }>("/v1/agents", {
    body: { email: `${slug}@${client.env.sharedDomain}`, name: `events ${label}` },
  });
  if (c.status !== 201 || !c.body?.email) {
    throw new Error(`create agent failed: ${c.status} ${c.raw.slice(0, 200)}`);
  }
  const email = c.body.email;
  if (hold) {
    const u = await holdAllOutbound(client, email);
    if (u.status !== 200) {
      await delAgent(email);
      throw new Error(`hold-all-outbound failed: ${u.status} ${u.raw.slice(0, 200)}`);
    }
  }
  return email;
}

async function createHook(events: string[]): Promise<CreateWebhookResponse> {
  const r = await client.post<CreateWebhookResponse>("/v1/webhooks", {
    // Dummy HTTPS target: passes the create-time HTTPS/SSRF guard, then 405s the
    // POST at delivery time — proving the delivery ATTEMPT without a real sink.
    body: { url: "https://example.com/e2e-webhook-events", events, description: `e2e ${uniqueSlug("whev")}` },
  });
  assert.equal(r.status, 201, `create webhook expected 201, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.ok(r.body?.id?.startsWith("wh_"), `webhook id has wh_ prefix: ${r.body?.id}`);
  return r.body!;
}

async function delAgent(email: string): Promise<void> {
  await client.delete(`/v1/agents/${encodeURIComponent(email)}?confirm=DELETE`);
}
async function delHook(id: string): Promise<void> {
  await client.delete(`/v1/webhooks/${encodeURIComponent(id)}`);
}

// pollEvent: poll listEvents (filtered type+agent_id+since) until an event
// matching `match` appears, or the bounded window elapses. Backoff 500ms→3s.
async function pollEvent(
  params: { type: string; agentId: string; since: string },
  match: (e: EventJSON) => boolean,
  timeoutMs = 15000,
): Promise<EventJSON | null> {
  const deadline = Date.now() + timeoutMs;
  let delay = 500;
  while (Date.now() < deadline) {
    const r = await client.get<PageEventJSON>("/v1/events", {
      query: { type: params.type, agent_id: params.agentId, since: params.since, limit: 50 },
    });
    if (r.status === 200 && r.body?.items) {
      const found = r.body.items.find(match);
      if (found) return found;
    }
    await sleep(delay);
    delay = Math.min(Math.floor(delay * 1.5), 3000);
  }
  return null;
}

// pollDelivery: poll a webhook's deliveries until one for `eventType` with
// attempts>=1 appears (proving a delivery leg ran for that event). Optionally
// require a specific delivery id (used by the redeliver test).
async function pollDelivery(
  webhookId: string,
  eventType: string,
  opts: { deliveryId?: string } = {},
  timeoutMs = 15000,
): Promise<WebhookDeliveryView | null> {
  const deadline = Date.now() + timeoutMs;
  let delay = 500;
  while (Date.now() < deadline) {
    const r = await client.get<PageWebhookDeliveryView>(`/v1/webhooks/${webhookId}/deliveries`);
    if (r.status === 200 && r.body?.items) {
      const found = r.body.items.find(
        (d) => d.event_type === eventType && d.attempts >= 1 && (!opts.deliveryId || d.id === opts.deliveryId),
      );
      if (found) return found;
    }
    await sleep(delay);
    delay = Math.min(Math.floor(delay * 1.5), 3000);
  }
  return null;
}

function assertEventShape(e: EventJSON, expect: { type: string; agentId: string; messageId?: string }): void {
  // EventJSON required fields (openapi): id,type,schema_version,created_at,status,data.
  assert.ok(typeof e.id === "string" && e.id.startsWith("evt_"), `event id has evt_ prefix: ${e.id}`);
  assert.equal(e.type, expect.type, "event.type matches the triggered type");
  assert.ok(typeof e.schema_version === "number" && e.schema_version >= 1, "schema_version is a positive integer");
  assert.ok(typeof e.created_at === "string" && e.created_at.length > 0, "created_at present");
  assert.ok(typeof e.status === "string" && e.status.length > 0, "status present");
  assert.ok(e.data && typeof e.data === "object", "data object present");
  // agent_id is optional in the schema but populated for these agent-scoped events.
  assert.equal(e.agent_id, expect.agentId, "event.agent_id is the triggering inbox");
  if (expect.messageId) {
    // Correlate to the triggering message: top-level message_id (populated on
    // staging) OR data.message_id (always present in the payload).
    const dataMsg = e.data.message_id;
    assert.ok(
      e.message_id === expect.messageId || dataMsg === expect.messageId,
      `event correlates to message ${expect.messageId} (top-level=${e.message_id} data=${String(dataMsg)})`,
    );
  }
}

// ---- email.sent: a REAL send (no hold) to the SES simulator ----
test("emit: email.sent — real send emits the event and attempts a delivery", async () => {
  const email = await createAgent("sent");
  const hook = await createHook(["email.sent"]);
  const since = new Date().toISOString();
  try {
    const send = await client.post<SendResult>(`/v1/agents/${encodeURIComponent(email)}/messages`, {
      body: { to: [SIMULATOR], subject: uniqueSubject("emit sent"), body: "real send to SES simulator" },
    });
    assert.equal(send.status, 200, `real send expected 200 sent, got ${send.status}: ${send.raw.slice(0, 200)}`);
    assert.equal(send.body?.status, "sent", "no-hold agent sends immediately");
    const messageId = send.body!.message_id!;
    assert.ok(messageId?.startsWith("msg_"), "send returns a msg_ id");

    const ev = await pollEvent({ type: "email.sent", agentId: email, since }, (e) =>
      e.message_id === messageId || e.data.message_id === messageId,
    );
    assert.ok(ev, `email.sent event for ${messageId} must appear in listEvents within 15s`);
    assertEventShape(ev!, { type: "email.sent", agentId: email, messageId });

    const del = await pollDelivery(hook.id, "email.sent");
    assert.ok(del, `a delivery ATTEMPT (attempts>=1) for email.sent must appear on webhook ${hook.id}`);
    assert.ok(del!.attempts >= 1, `delivery attempted (attempts=${del!.attempts})`);
    info(SUITE, "email.sent", `emitted evt=${ev!.id}, delivery whd=${del!.id} attempts=${del!.attempts} last_status=${del!.last_status_code} (405 = no real sink, delivery leg ran)`);
  } finally {
    await delHook(hook.id);
    await delAgent(email);
  }
});

// ---- email.pending_review: hold-all-outbound BEFORE send → 202 ----
test("emit: email.pending_review — held send emits the event and attempts a delivery", async () => {
  const email = await createAgent("pending", true);
  const hook = await createHook(["email.pending_review"]);
  const since = new Date().toISOString();
  let heldId: string | null = null;
  try {
    const send = await client.post<SendResult>(`/v1/agents/${encodeURIComponent(email)}/messages`, {
      body: { to: [SIMULATOR], subject: uniqueSubject("emit pending"), body: "held for review" },
    });
    assert.equal(send.status, 202, `held send expected 202 pending_review, got ${send.status}: ${send.raw.slice(0, 200)}`);
    assert.equal(send.body?.status, "pending_review", "gated send is held");
    heldId = send.body!.message_id!;
    assert.ok(heldId?.startsWith("msg_"), "held send returns a msg_ id");

    const ev = await pollEvent({ type: "email.pending_review", agentId: email, since }, (e) =>
      e.message_id === heldId || e.data.message_id === heldId,
    );
    assert.ok(ev, `email.pending_review event for ${heldId} must appear within 15s`);
    assertEventShape(ev!, { type: "email.pending_review", agentId: email, messageId: heldId! });
    // Payload is direction-aware (outbound HITL hold).
    assert.equal(ev!.data.direction, "outbound", "pending_review payload carries direction=outbound");

    const del = await pollDelivery(hook.id, "email.pending_review");
    assert.ok(del, `a delivery ATTEMPT for email.pending_review must appear on webhook ${hook.id}`);
    assert.ok(del!.attempts >= 1, `delivery attempted (attempts=${del!.attempts})`);
    info(SUITE, "email.pending_review", `emitted evt=${ev!.id}, delivery whd=${del!.id} attempts=${del!.attempts}`);
  } finally {
    // Resolve the hold explicitly (reject), then delete (delete cascades anyway).
    if (heldId) await client.post(`/v1/reviews/${heldId}/reject`, { body: { reason: "e2e pending-emit cleanup" } });
    await delHook(hook.id);
    await delAgent(email);
  }
});

// ---- email.review_rejected: reject a held message (no send) ----
test("emit: email.review_rejected — rejecting a hold emits the event and attempts a delivery", async () => {
  const email = await createAgent("reject", true);
  const hook = await createHook(["email.review_rejected"]);
  const since = new Date().toISOString();
  try {
    const send = await client.post<SendResult>(`/v1/agents/${encodeURIComponent(email)}/messages`, {
      body: { to: [SIMULATOR], subject: uniqueSubject("emit reject"), body: "will be rejected" },
    });
    assert.equal(send.status, 202, `held send expected 202, got ${send.status}: ${send.raw.slice(0, 200)}`);
    const heldId = send.body!.message_id!;

    const reason = "e2e review_rejected emission";
    const rej = await client.post<{ status?: string; message_id?: string }>(`/v1/reviews/${heldId}/reject`, {
      body: { reason },
    });
    assert.equal(rej.status, 200, `reject expected 200, got ${rej.status}: ${rej.raw.slice(0, 200)}`);
    assert.equal(rej.body?.status, "review_rejected", "reject transitions to review_rejected");

    const ev = await pollEvent({ type: "email.review_rejected", agentId: email, since }, (e) =>
      e.message_id === heldId || e.data.message_id === heldId,
    );
    assert.ok(ev, `email.review_rejected event for ${heldId} must appear within 15s`);
    assertEventShape(ev!, { type: "email.review_rejected", agentId: email, messageId: heldId });
    assert.equal(ev!.data.rejection_reason, reason, "payload echoes the rejection reason");

    const del = await pollDelivery(hook.id, "email.review_rejected");
    assert.ok(del, `a delivery ATTEMPT for email.review_rejected must appear on webhook ${hook.id}`);
    assert.ok(del!.attempts >= 1, `delivery attempted (attempts=${del!.attempts})`);
    info(SUITE, "email.review_rejected", `emitted evt=${ev!.id}, delivery whd=${del!.id} attempts=${del!.attempts}`);
  } finally {
    await delHook(hook.id);
    await delAgent(email);
  }
});

// ---- email.review_approved: approve a held message addressed to the simulator ----
test("emit: email.review_approved — approving a hold (to the simulator) emits the event and attempts a delivery", async () => {
  const email = await createAgent("approve", true);
  const hook = await createHook(["email.review_approved"]);
  const since = new Date().toISOString();
  let heldId: string | null = null;
  let resolved = false;
  try {
    const send = await client.post<SendResult>(`/v1/agents/${encodeURIComponent(email)}/messages`, {
      // Addressed to the simulator so approve→send actually succeeds on staging's
      // SES sandbox (a non-simulator/blackhole recipient 500s the send leg).
      body: { to: [SIMULATOR], subject: uniqueSubject("emit approve"), body: "will be approved + sent" },
    });
    assert.equal(send.status, 202, `held send expected 202, got ${send.status}: ${send.raw.slice(0, 200)}`);
    heldId = send.body!.message_id!;

    const ap = await client.post<SendResult>(`/v1/reviews/${heldId}/approve`, { body: {} });
    assert.equal(ap.status, 200, `approve→send (to simulator) expected 200, got ${ap.status}: ${ap.raw.slice(0, 200)}`);
    assert.equal(ap.body?.status, "sent", "approved hold to the simulator sends successfully");
    resolved = true;

    const ev = await pollEvent({ type: "email.review_approved", agentId: email, since }, (e) =>
      e.message_id === heldId || e.data.message_id === heldId,
    );
    assert.ok(ev, `email.review_approved event for ${heldId} must appear within 15s`);
    assertEventShape(ev!, { type: "email.review_approved", agentId: email, messageId: heldId! });
    assert.equal(ev!.data.direction, "outbound", "review_approved payload carries direction=outbound");

    const del = await pollDelivery(hook.id, "email.review_approved");
    assert.ok(del, `a delivery ATTEMPT for email.review_approved must appear on webhook ${hook.id}`);
    assert.ok(del!.attempts >= 1, `delivery attempted (attempts=${del!.attempts})`);
    info(SUITE, "email.review_approved", `emitted evt=${ev!.id}, delivery whd=${del!.id} attempts=${del!.attempts}`);
  } finally {
    // If approve didn't resolve the hold, reject it so nothing lingers.
    if (heldId && !resolved) {
      await client.post(`/v1/reviews/${heldId}/reject`, { body: { reason: "e2e approve-emit cleanup" } });
    }
    await delHook(hook.id);
    await delAgent(email);
  }
});

// ---- Events read API: listEvents envelope + filters ----
test("events: listEvents returns PageEventJSON envelope and honors type/agent_id/since/limit filters", async () => {
  const email = await createAgent("list");
  const hook = await createHook(["email.sent"]);
  const since = new Date().toISOString();
  try {
    const send = await client.post<SendResult>(`/v1/agents/${encodeURIComponent(email)}/messages`, {
      body: { to: [SIMULATOR], subject: uniqueSubject("emit list"), body: "for listEvents" },
    });
    assert.equal(send.status, 200, `real send expected 200, got ${send.status}: ${send.raw.slice(0, 200)}`);
    const messageId = send.body!.message_id!;
    // Ensure the event exists before asserting on the filtered list.
    const seed = await pollEvent({ type: "email.sent", agentId: email, since }, (e) =>
      e.message_id === messageId || e.data.message_id === messageId,
    );
    assert.ok(seed, "seed email.sent event present");

    // Full envelope shape (PageEventJSON: items + next_cursor:string|null, both required).
    const page = await client.get<PageEventJSON>("/v1/events", { query: { limit: 5 } });
    assert.equal(page.status, 200, `listEvents expected 200, got ${page.status}`);
    assert.ok(Array.isArray(page.body?.items), "items is an array");
    assert.ok(
      page.body!.next_cursor === null || typeof page.body!.next_cursor === "string",
      `next_cursor must be present as string|null, got ${JSON.stringify(page.body!.next_cursor)}`,
    );
    assert.ok(page.body!.items.length <= 5, "limit=5 clamps the page size");

    // type filter: every returned item is the requested type.
    const typed = await client.get<PageEventJSON>("/v1/events", {
      query: { type: "email.sent", agent_id: email, since },
    });
    assert.equal(typed.status, 200);
    assert.ok(typed.body!.items.length >= 1, "type+agent_id+since filter returns the seeded event");
    for (const e of typed.body!.items) {
      assert.equal(e.type, "email.sent", "type filter is honored");
      assert.equal(e.agent_id, email, "agent_id filter is honored");
    }

    // agent_id filter isolation: a bogus agent_id returns an empty page (not an error).
    const other = await client.get<PageEventJSON>("/v1/events", {
      query: { agent_id: `nonexistent-${Date.now()}@${client.env.sharedDomain}`, since },
    });
    assert.equal(other.status, 200, "agent_id filter with no matches returns 200");
    assert.equal(other.body!.items.length, 0, "unknown agent_id yields an empty page");

    // since filter: a future timestamp excludes everything.
    const future = new Date(Date.now() + 3_600_000).toISOString();
    const none = await client.get<PageEventJSON>("/v1/events", { query: { agent_id: email, since: future } });
    assert.equal(none.status, 200);
    assert.equal(none.body!.items.length, 0, "since=future excludes all events");
  } finally {
    await delHook(hook.id);
    await delAgent(email);
  }
});

// ---- Events read API: getEvent (+ 404) ----
test("events: getEvent returns the EventJSON by evt_ id; nonexistent → 404", async () => {
  const email = await createAgent("get");
  const hook = await createHook(["email.sent"]);
  const since = new Date().toISOString();
  try {
    const send = await client.post<SendResult>(`/v1/agents/${encodeURIComponent(email)}/messages`, {
      body: { to: [SIMULATOR], subject: uniqueSubject("emit get"), body: "for getEvent" },
    });
    assert.equal(send.status, 200);
    const messageId = send.body!.message_id!;
    const ev = await pollEvent({ type: "email.sent", agentId: email, since }, (e) =>
      e.message_id === messageId || e.data.message_id === messageId,
    );
    assert.ok(ev, "seeded email.sent event present");

    const got = await client.get<EventJSON>(`/v1/events/${ev!.id}`);
    assert.equal(got.status, 200, `getEvent expected 200, got ${got.status}: ${got.raw.slice(0, 200)}`);
    assert.equal(got.body?.id, ev!.id, "getEvent echoes the requested id");
    assertEventShape(got.body!, { type: "email.sent", agentId: email, messageId });

    const miss = await client.get(`/v1/events/evt_nonexistent_${Date.now()}`);
    assert.equal(miss.status, 404, `getEvent nonexistent expected 404, got ${miss.status}: ${miss.raw.slice(0, 200)}`);
  } finally {
    await delHook(hook.id);
    await delAgent(email);
  }
});

// ---- Events read API: redeliverEvent (re-queues a delivery) ----
test("events: redeliverEvent re-queues a delivery for the event; a new attempt appears", async () => {
  const email = await createAgent("redeliver");
  const hook = await createHook(["email.sent"]);
  const since = new Date().toISOString();
  try {
    const send = await client.post<SendResult>(`/v1/agents/${encodeURIComponent(email)}/messages`, {
      body: { to: [SIMULATOR], subject: uniqueSubject("emit redeliver"), body: "for redeliver" },
    });
    assert.equal(send.status, 200);
    const messageId = send.body!.message_id!;
    const ev = await pollEvent({ type: "email.sent", agentId: email, since }, (e) =>
      e.message_id === messageId || e.data.message_id === messageId,
    );
    assert.ok(ev, "seeded email.sent event present");
    // Wait for the original delivery so we can prove redeliver ADDS one.
    const first = await pollDelivery(hook.id, "email.sent");
    assert.ok(first, "original email.sent delivery attempt present before redeliver");

    // Single-webhook replay: RedeliverView carries event_id + status + top-level
    // delivery_id + webhook_id (probed status "pending"). requestBody is required.
    const rd = await client.post<RedeliverView>(`/v1/events/${ev!.id}/redeliver`, { body: { webhook_id: hook.id } });
    assert.equal(rd.status, 200, `redeliver expected 200, got ${rd.status}: ${rd.raw.slice(0, 200)}`);
    assert.equal(rd.body?.event_id, ev!.id, "RedeliverView echoes the event id");
    assert.ok(typeof rd.body?.status === "string" && rd.body.status.length > 0, "RedeliverView has a status");
    // Collect the new delivery id(s) from either the single or bulk shape.
    const newIds = [
      rd.body?.delivery_id,
      ...(rd.body?.deliveries ?? []).map((d) => d.delivery_id),
    ].filter((x): x is string => typeof x === "string" && x.length > 0);
    assert.ok(newIds.length >= 1, `redeliver returns at least one new delivery id: ${JSON.stringify(rd.body)}`);
    assert.ok(newIds.includes(first!.id) === false, "redeliver id is distinct from the original delivery");

    // The re-queued delivery must surface in the webhook's deliveries.
    const requeued = await pollDelivery(hook.id, "email.sent", { deliveryId: newIds[0] });
    assert.ok(requeued, `re-queued delivery ${newIds[0]} must appear on webhook ${hook.id}`);
    assert.ok(requeued!.attempts >= 1, `re-queued delivery attempted (attempts=${requeued!.attempts})`);
    info(SUITE, "redeliverEvent", `event=${ev!.id} original whd=${first!.id} → redelivered whd=${newIds[0]} status=${rd.body?.status}`);

    // Redeliver of a nonexistent event → 404.
    const miss = await client.post(`/v1/events/evt_nonexistent_${Date.now()}/redeliver`, { body: {} });
    assert.equal(miss.status, 404, `redeliver nonexistent expected 404, got ${miss.status}: ${miss.raw.slice(0, 200)}`);
  } finally {
    await delHook(hook.id);
    await delAgent(email);
  }
});

// ---- Negatives ----
test("events: unauthenticated listEvents / getEvent → 401", async () => {
  const list = await client.get("/v1/events", { apiKey: null });
  assert.equal(list.status, 401, `unauth listEvents expected 401, got ${list.status}`);
  const get = await client.get(`/v1/events/evt_whatever`, { apiKey: null });
  assert.equal(get.status, 401, `unauth getEvent expected 401, got ${get.status}`);
});

// ---- Documented skips for events that can't be HTTP-triggered on staging here ----
test("emit: email.received — needs real inbound SMTP (prober's round-trip)", { skip: "email.received requires a real inbound SMTP delivery; that is the prober's dedicated job, not an API-driven trigger from this suite" }, () => {});
test("emit: email.blocked — needs a screening block config", { skip: "email.blocked requires a screening gate/scan `block` action to refuse a message; out of scope for the HTTP emission battery" }, () => {});
test("emit: email.delivered/bounced/complained — async SES delivery feedback", { skip: "delivery-feedback events arrive async via SES→SNS on an unbounded timeline and are not deterministic within a test window" }, () => {});
test("emit: domain.sending_verified/failed, domain.suppression_added — need sending-identity provisioning", { skip: "domain.* events require real SES sending-identity provisioning against a custom domain, not available to a throwaway shared-domain agent" }, () => {});

after(async () => {
  await writeReport(`./reports/${SUITE}.json`);
});
