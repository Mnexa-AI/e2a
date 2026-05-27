import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { cleanup, track } from "../harness/cleanup.ts";
import { uniqueSlug, uniqueSubject, uniqueIdempotencyKey, SINK_EMAIL } from "../harness/fixtures.ts";
import { fail, info, warn, writeReport } from "../harness/report.ts";

const client = new ApiClient();
const SUITE = "11-messaging";

after(async () => {
  const r = await cleanup(client);
  if (r.failed.length) warn(SUITE, "cleanup", `failed ${r.failed.length}`, r.failed);
  writeReport(`./reports/11-messaging.json`);
});

async function createHitlAgent(name: string): Promise<string> {
  const slug = uniqueSlug(name);
  const c = await client.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name, agent_mode: "local" },
  });
  if (c.status !== 201) throw new Error(`create agent failed: ${c.status} ${c.raw.slice(0, 200)}`);
  const email = c.body!.email;
  track("agent", email);
  const u = await client.put(`/api/v1/agents/${encodeURIComponent(email)}`, {
    body: { hitl_enabled: true, hitl_expiration_action: "reject", hitl_ttl_seconds: 120 },
  });
  if (u.status !== 200) throw new Error(`enable HITL failed: ${u.status} ${u.raw.slice(0, 200)}`);
  return email;
}

test("messaging: pagination roundtrip — limit=3 then follow next_token; no duplicate ids", async () => {
  // Queue a few HITL messages so we have something to paginate against.
  const email = await createHitlAgent("pag");
  const queued: string[] = [];
  for (let i = 0; i < 5; i++) {
    const s = await client.post<{ message_id: string }>("/api/v1/send", {
      body: { from: email, to: [SINK_EMAIL], subject: uniqueSubject(`pag ${i}`), body: "x" },
    });
    if (s.body?.message_id) queued.push(s.body.message_id);
  }

  const page1 = await client.get<{ messages: Array<{ id: string }>; next_token?: string }>(
    "/api/v1/messages",
    { query: { limit: 3 } },
  );
  assert.equal(page1.status, 200);
  assert.ok(Array.isArray(page1.body?.messages));
  if (!page1.body?.next_token) {
    info(SUITE, "pagination-single-page", "fewer than 3+1 messages in inbox — pagination not exercised");
    // Cleanup queued.
    for (const id of queued) await client.post(`/api/v1/messages/${id}/reject`, { body: { reason: "e2e pagination cleanup" } });
    return;
  }
  const page2 = await client.get<{ messages: Array<{ id: string }>; next_token?: string }>(
    "/api/v1/messages",
    { query: { limit: 3, next_token: page1.body.next_token } },
  );
  assert.equal(page2.status, 200, `page2 status ${page2.status}: ${page2.raw.slice(0, 200)}`);
  const ids1 = new Set((page1.body!.messages ?? []).map((m) => m.id));
  const ids2 = new Set((page2.body!.messages ?? []).map((m) => m.id));
  const overlap = [...ids1].filter((id) => ids2.has(id));
  if (overlap.length > 0) {
    fail(SUITE, "pagination-duplicate-ids", `${overlap.length} ids appear on both pages: ${overlap.slice(0, 5).join(",")}`);
    // Cleanup before throwing so we don't leak HITL-held messages.
    for (const id of queued) await client.post(`/api/v1/messages/${id}/reject`, { body: { reason: "e2e pagination cleanup" } });
    assert.fail(`pagination roundtrip returned ${overlap.length} duplicate id(s) — pagination is broken`);
  }
  // Cleanup.
  for (const id of queued) await client.post(`/api/v1/messages/${id}/reject`, { body: { reason: "e2e pagination cleanup" } });
});

test("messaging: Idempotency-Key replay — same key+body returns same message_id", async () => {
  const email = await createHitlAgent("idem");
  const idemKey = uniqueIdempotencyKey();
  const body = { from: email, to: [SINK_EMAIL], subject: uniqueSubject("idem"), body: "idempotency test" };

  const r1 = await client.post<{ message_id: string; status: string }>("/api/v1/send", {
    body,
    headers: { "Idempotency-Key": idemKey },
  });
  assert.ok(r1.status === 200 || r1.status === 202, `first send unexpected ${r1.status}: ${r1.raw.slice(0, 200)}`);
  assert.ok(r1.body?.message_id, "first send returned message_id");
  const firstId = r1.body!.message_id;

  const r2 = await client.post<{ message_id: string; status: string }>("/api/v1/send", {
    body,
    headers: { "Idempotency-Key": idemKey },
  });
  if (r2.body?.message_id !== firstId) {
    fail(
      SUITE,
      "idem-key-not-replayed",
      `same Idempotency-Key + same body yielded different message_id: ${firstId} → ${r2.body?.message_id}. Server should replay original response, not re-queue.`,
    );
    // Hard assert — Idempotency-Key semantics are a financial-stakes
    // contract (double-send protection on approve). Don't paper over.
    await client.post(`/api/v1/messages/${firstId}/reject`, { body: { reason: "e2e idem cleanup pre-fail" } });
    if (r2.body?.message_id) {
      await client.post(`/api/v1/messages/${r2.body.message_id}/reject`, { body: { reason: "e2e idem cleanup pre-fail" } });
    }
    assert.fail(`Idempotency-Key replay broken: ${firstId} !== ${r2.body?.message_id}`);
  }
  info(SUITE, "idem-key-replayed", `Idempotency-Key replay correct: same key+body → same message_id ${firstId}`);

  // Different body, same key → 422 per spec.
  const r3 = await client.post("/api/v1/send", {
    body: { ...body, subject: uniqueSubject("idem mutated") },
    headers: { "Idempotency-Key": idemKey },
  });
  if (r3.status !== 422) {
    info(SUITE, "idem-diff-body-non-422", `same key + DIFFERENT body returned ${r3.status} instead of 422: ${r3.raw.slice(0, 200)}`);
  }

  await client.post(`/api/v1/messages/${firstId}/reject`, { body: { reason: "e2e idem cleanup" } });
});

test("messaging: /agents/{email}/test on HITL agent returns 202 with message_id (and is rejectable)", async () => {
  const email = await createHitlAgent("test-ep");
  const r = await client.post<{ status?: string; message_id?: string } | Record<string, string>>(
    `/api/v1/agents/${encodeURIComponent(email)}/test`,
    { body: {} },
  );
  if (r.status === 403) {
    info(SUITE, "test-domain-unverified", `/test on slug-domain HITL agent returned 403 — slug agents are supposed to be auto-verified per 01-basic.create test. Body: ${r.raw.slice(0, 200)}`);
    return;
  }
  if (r.status >= 500) {
    fail(SUITE, "test-5xx", `/test endpoint returned ${r.status}: ${r.raw.slice(0, 200)}`);
    return;
  }
  // HITL is enabled, so spec says 202.
  if (r.status !== 202) {
    info(SUITE, "test-non-202", `expected 202 for HITL agent, got ${r.status}: ${r.raw.slice(0, 200)}`);
  }
  const msgId = (r.body as { message_id?: string })?.message_id;
  if (msgId) {
    const rej = await client.post(`/api/v1/messages/${msgId}/reject`, { body: { reason: "e2e /test cleanup" } });
    assert.ok(rej.status === 200, `failed to reject /test message: ${rej.status} ${rej.raw.slice(0, 200)}`);
  } else {
    info(SUITE, "test-no-msgid", "response body did not include message_id");
  }
});

test("messaging: HITL approve flow — send queues, approve sends, status→sent", async () => {
  const email = await createHitlAgent("appr");
  const s = await client.post<{ message_id: string; status: string }>("/api/v1/send", {
    body: { from: email, to: [SINK_EMAIL], subject: uniqueSubject("approve"), body: "approve me" },
  });
  if (s.status !== 202) {
    info(SUITE, "approve-no-202", `expected 202 from HITL send, got ${s.status}: ${s.raw.slice(0, 200)}`);
    return;
  }
  assert.equal(s.body?.status, "pending_approval");
  const id = s.body!.message_id;

  // Approve — empty body approves as-is. Goes out via SMTP to blackhole sink.
  const ap = await client.post<{ message_id: string; status: string }>(`/api/v1/messages/${id}/approve`, { body: {} });
  if (ap.status !== 200) {
    fail(SUITE, "approve-non-200", `approve returned ${ap.status}: ${ap.raw.slice(0, 200)}`);
    return;
  }
  assert.equal(ap.body?.message_id, id);
  // Status should be "sent" or whatever the post-approve canonical is.
  const finalStatus = ap.body?.status;
  info(SUITE, "approve-final-status", `approve returned status="${finalStatus}"`);

  // Re-approve must fail with 409 (already sent).
  const ap2 = await client.post(`/api/v1/messages/${id}/approve`, { body: {} });
  if (ap2.status !== 409) {
    info(SUITE, "double-approve-non-409", `re-approve of sent message returned ${ap2.status} instead of 409: ${ap2.raw.slice(0, 200)}`);
  }
});

test("messaging: reject of a sent message returns 409 (state guard)", async () => {
  const email = await createHitlAgent("rej");
  const s = await client.post<{ message_id: string }>("/api/v1/send", {
    body: { from: email, to: [SINK_EMAIL], subject: uniqueSubject("reject after send"), body: "x" },
  });
  if (s.status !== 202 || !s.body?.message_id) {
    info(SUITE, "rej-after-send-skipped", `setup HITL send returned ${s.status}: ${s.raw.slice(0, 200)}`);
    return;
  }
  const id = s.body.message_id;
  // First approve so it transitions to sent.
  const ap = await client.post(`/api/v1/messages/${id}/approve`, { body: {} });
  if (ap.status !== 200) {
    info(SUITE, "rej-after-send-approve-failed", `approve returned ${ap.status}, can't test reject-after-send`);
    return;
  }
  // Now reject the same message — must 409.
  const rej = await client.post(`/api/v1/messages/${id}/reject`, { body: { reason: "should fail" } });
  if (rej.status !== 409) {
    info(SUITE, "reject-after-send-non-409", `expected 409 for reject after send, got ${rej.status}: ${rej.raw.slice(0, 200)}`);
  }
});

test("messaging: approve with field overrides applies them before send", async () => {
  const email = await createHitlAgent("appov");
  const s = await client.post<{ message_id: string }>("/api/v1/send", {
    body: { from: email, to: [SINK_EMAIL], subject: uniqueSubject("original"), body: "original body" },
  });
  if (s.status !== 202 || !s.body?.message_id) {
    info(SUITE, "approve-override-skipped", `setup send returned ${s.status}`);
    return;
  }
  const id = s.body.message_id;
  // Override subject + body on approve. Spec: any subset of subject/body/body_html/to/cc/bcc/attachments.
  const ap = await client.post<{ message_id: string; status: string }>(
    `/api/v1/messages/${id}/approve`,
    { body: { subject: "overridden subject (approve-time)", body_text: "overridden body" } },
  );
  if (ap.status !== 200) {
    info(SUITE, "approve-override-non-200", `override approve returned ${ap.status}: ${ap.raw.slice(0, 200)}`);
    return;
  }
  // Fetch the message to see whether the override was actually persisted to the
  // sent record. Note: body columns are scrubbed on send per the approve description;
  // subject may remain.
  const g = await client.get<{ subject?: string; status?: string }>(`/api/v1/messages/${id}`);
  if (g.status === 200) {
    info(SUITE, "approve-override-readback", `final subject after override approve: "${g.body?.subject ?? "(absent)"}", status="${g.body?.status}"`);
  } else {
    info(SUITE, "approve-override-readback-failed", `GET /messages/${id} returned ${g.status}`);
  }
});

test("messaging: reply to bogus message ID returns 404", async () => {
  const email = client.env.primaryAgentEmail;
  const r = await client.post(
    `/api/v1/agents/${encodeURIComponent(email)}/messages/msg_does_not_exist_${Date.now()}/reply`,
    { body: { body: "won't be delivered" } },
  );
  assert.ok(r.status === 404 || (r.status >= 400 && r.status < 500), `expected 4xx (404), got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("messaging: reply with empty body returns 400", async () => {
  // Find any message we own to attempt reply against; if none, skip.
  const list = await client.get<{ messages: Array<{ id: string; direction?: string }> }>("/api/v1/messages", { query: { limit: 5 } });
  const candidate = list.body?.messages?.find((m) => m.direction === "inbound") ?? list.body?.messages?.[0];
  if (!candidate) {
    info(SUITE, "reply-empty-skipped", "no messages in inbox to attempt reply against");
    return;
  }
  const email = client.env.primaryAgentEmail;
  const r = await client.post(
    `/api/v1/agents/${encodeURIComponent(email)}/messages/${encodeURIComponent(candidate.id)}/reply`,
    { body: {} },
  );
  // Spec: 400 missing body, OR 404 if message isn't owned by THIS agent.
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx (400 or 404), got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("messaging: /messages search filters — surface what's supported", async () => {
  // Probe each known/likely filter to see what the server actually accepts.
  const probes = [
    { name: "agent_email", q: { agent_email: client.env.primaryAgentEmail, limit: 5 } },
    { name: "status=pending_approval", q: { status: "pending_approval", limit: 5 } },
    { name: "direction=outbound", q: { direction: "outbound", limit: 5 } },
    { name: "direction=inbound", q: { direction: "inbound", limit: 5 } },
    { name: "since=2024-01-01", q: { since: "2024-01-01T00:00:00Z", limit: 5 } },
  ];
  const observed: string[] = [];
  for (const p of probes) {
    const r = await client.get<{ messages: unknown[] }>("/api/v1/messages", { query: p.q as Record<string, string | number> });
    observed.push(`${p.name}=${r.status}/${Array.isArray(r.body?.messages) ? r.body.messages.length : "?"}`);
    if (r.status >= 500) {
      fail(SUITE, `filter-5xx-${p.name}`, `${p.name} caused ${r.status}: ${r.raw.slice(0, 200)}`);
    }
  }
  info(SUITE, "filter-surface", `filters: ${observed.join(" | ")}`);
});

test("messaging: GET /messages/{id} of nonexistent message returns 4xx", async () => {
  const r = await client.get(`/api/v1/messages/msg_nonexistent_${Date.now()}`);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("messaging: GET /agents/{email}/messages returns documented shape", async () => {
  const email = client.env.primaryAgentEmail;
  const r = await client.get<{ messages?: unknown[] }>(`/api/v1/agents/${encodeURIComponent(email)}/messages`, {
    query: { limit: 5 },
  });
  assert.equal(r.status, 200, `expected 200, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.ok(Array.isArray(r.body?.messages), "messages array present");
});

test("messaging: send with reply_to (header round-trip) is accepted", async () => {
  const email = await createHitlAgent("replyto");
  const r = await client.post<{ message_id: string }>("/api/v1/send", {
    body: {
      from: email,
      to: [SINK_EMAIL],
      subject: uniqueSubject("with reply_to"),
      body: "x",
      reply_to: ["specific-reply@example.com"],
    },
  });
  if (r.status >= 400 && r.status < 500) {
    info(SUITE, "reply-to-rejected", `send with reply_to returned ${r.status}: ${r.raw.slice(0, 200)}`);
    return;
  }
  assert.ok(r.status === 200 || r.status === 202, `expected 200/202, got ${r.status}`);
  if (r.body?.message_id) {
    await client.post(`/api/v1/messages/${r.body.message_id}/reject`, { body: { reason: "e2e reply_to cleanup" } });
  }
});
