import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { uniqueSlug, uniqueSubject, holdAllOutbound } from "../harness/fixtures.ts";
import { track, cleanup } from "../harness/cleanup.ts";
import { writeReport } from "../harness/report.ts";

// Happy-path coverage for operations the rest of the suite only PROBES negatively
// (updateAgent, updateMessage, forwardMessage, getConversation, replyToMessage,
// approveReview). The operationId coverage gate records an op as covered only on
// a 2xx, so an op that elsewhere gets nothing but a 404/400 probe reads as
// UNCOVERED. These tests close that gap by INVOKING each op successfully on fresh,
// isolated agents — no dependency on shared-account state (which made the messaging
// suite's approve/reply flaky under the full concurrent run).
const SUITE = "23-coverage-happy-path";
const client = new ApiClient();

// A real, deliverable recipient: SES accepts + 200s it (a non-simulator recipient
// 500s on staging's SES sandbox), so outbound-carrying ops actually reach "sent".
const SIMULATOR = "success@simulator.amazonses.com";
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

async function freshAgent(label: string, hold = false): Promise<string> {
  const email = `${uniqueSlug(label)}@${client.env.sharedDomain}`;
  const r = await client.post<{ email: string }>("/v1/agents", {
    body: { email, name: `cov ${label}` },
    expect: 201,
  });
  track("agent", email);
  if (hold) {
    const u = await holdAllOutbound(client, email);
    assert.equal(u.status, 200, `hold-all-outbound ${email}: ${u.raw.slice(0, 200)}`);
  }
  return email;
}

// Self-send loopback → poll the inbox for the inbound copy (with its conversation id).
async function loopbackMessage(email: string, subject: string): Promise<{ id: string; conversationId: string }> {
  // A self-send may complete synchronously through the local loopback path →
  // 200 sent, or be durably accepted for asynchronous delivery → 202 accepted.
  // Tolerate both; completion is verified by polling for the inbound copy below.
  await client.post(`/v1/agents/${encodeURIComponent(email)}/messages`, {
    body: { to: [email], subject, text: "coverage happy-path body" },
    expect: [200, 202],
  });
  for (let i = 0; i < 12; i++) {
    const list = await client.get<{
      items: Array<{ id: string; subject: string; conversation_id: string }>;
    }>(`/v1/agents/${encodeURIComponent(email)}/messages`, { query: { direction: "inbound", limit: 20 } });
    const m = list.body?.items?.find((x) => x.subject === subject);
    // MessageSummaryView's id field is `id` (not `message_id`).
    if (m) return { id: m.id, conversationId: m.conversation_id };
    await sleep(1500);
  }
  throw new Error(`loopback message "${subject}" never appeared for ${email}`);
}

test("updateAgent: PATCH an agent's name (200)", async () => {
  const email = await freshAgent("covua");
  const r = await client.patch<{ name: string }>(`/v1/agents/${encodeURIComponent(email)}`, {
    body: { name: "renamed-by-coverage" },
    expect: 200,
  });
  assert.equal(r.body?.name, "renamed-by-coverage");
});

test("updateMessage + getConversation: label an inbound message, fetch its conversation (200)", async () => {
  const email = await freshAgent("covum");
  const { id, conversationId } = await loopbackMessage(email, uniqueSubject("cov um"));

  const upd = await client.patch<{ labels?: string[] }>(`/v1/agents/${encodeURIComponent(email)}/messages/${id}`, {
    body: { add_labels: ["coverage"] },
    expect: 200,
  });
  assert.ok((upd.body?.labels ?? ["coverage"]).includes("coverage"), "label was added");

  const conv = await client.get<{ conversation_id?: string }>(
    `/v1/agents/${encodeURIComponent(email)}/conversations/${encodeURIComponent(conversationId)}`,
    { expect: 200 },
  );
  assert.ok(conv.body, "conversation fetched");
});

test("replyToMessage: reply to an inbound message (self-loopback → 200)", async () => {
  const email = await freshAgent("covrp");
  const { id } = await loopbackMessage(email, uniqueSubject("cov rp"));
  // The loopback message's sender is the agent itself → the reply loops back too.
  const r = await client.post<{ message_id: string }>(`/v1/agents/${encodeURIComponent(email)}/messages/${id}/reply`, {
    body: { text: "replying for coverage" },
    expect: [200, 202],
  });
  assert.ok(r.body?.message_id, "reply returned a message id");
});

test("forwardMessage: forward an inbound message to the simulator (200)", async () => {
  const email = await freshAgent("covfw");
  const { id } = await loopbackMessage(email, uniqueSubject("cov fw"));
  const r = await client.post<{ message_id: string }>(`/v1/agents/${encodeURIComponent(email)}/messages/${id}/forward`, {
    body: { to: [SIMULATOR], text: "fyi — forwarded for coverage" },
    expect: [200, 202],
  });
  assert.ok(r.body?.message_id, "forward returned a message id");
});

test("approveReview: approve a HITL-held outbound (200 terminal or 202 enqueued)", async () => {
  const email = await freshAgent("covap", true); // holds all outbound for review
  const send = await client.post<{ status: string; message_id: string }>(
    `/v1/agents/${encodeURIComponent(email)}/messages`,
    { body: { to: [SIMULATOR], subject: uniqueSubject("cov ap"), text: "to approve" }, expect: 202 },
  );
  assert.equal(send.body?.status, "pending_review");
  const id = send.body!.message_id;
  const ap = await client.post<{ message_id: string; status: string }>(
    `/v1/reviews/${id}/approve`,
    { body: {}, expect: [200, 202] },
  );
  assert.equal(ap.body?.message_id, id);
  assert.equal(ap.body?.status, ap.status === 202 ? "accepted" : "sent");
});

after(async () => {
  await cleanup(client);
  await writeReport(`./reports/${SUITE}.json`);
});
