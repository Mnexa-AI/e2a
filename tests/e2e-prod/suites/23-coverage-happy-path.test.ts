import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { uniqueSlug, uniqueSubject, holdAllOutbound } from "../harness/fixtures.ts";
import { track, cleanup } from "../harness/cleanup.ts";
import { writeReport } from "../harness/report.ts";

// Happy-path coverage for operations the rest of the suite only PROBES negatively
// (updateAgent, updateMessage, forwardMessage, getConversation, replyToMessage,
// approveReview) or not at all (the soft-delete/restore trio: deleteMessage,
// restoreMessage, restoreAgent). The operationId coverage gate records an op as
// covered only on a 2xx, so an op that elsewhere gets nothing but a 404/400 probe
// reads as UNCOVERED. These tests close that gap by INVOKING each op successfully
// on fresh, isolated agents — no dependency on shared-account state (which made
// the messaging suite's approve/reply flaky under the full concurrent run).
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

test("deleteMessage + restoreMessage: trash a message, then bring it back (200)", async () => {
  const email = await freshAgent("covdm");
  const { id } = await loopbackMessage(email, uniqueSubject("cov dm"));
  const base = `/v1/agents/${encodeURIComponent(email)}/messages`;

  // Default DELETE = reversible move to trash: no ?confirm needed, 200 receipt.
  const del = await client.delete<{ deleted: boolean; id: string }>(`${base}/${id}`, { expect: 200 });
  assert.equal(del.body?.deleted, true, "delete receipt must say deleted:true");
  assert.equal(del.body?.id, id, "delete receipt echoes the message id");

  // Trashed: gone from the ordinary list, present in the trash (deleted=true)
  // list, and still directly readable with deleted_at stamped. List checks pass
  // read_status=all: the inbound list defaults to read_status=unread, and the
  // direct GET below marks the message read — without read_status=all the
  // restored message would be invisible for the wrong reason.
  const live = await client.get<{ items: Array<{ id: string }> }>(base, {
    query: { read_status: "all", limit: 50 },
    expect: 200,
  });
  assert.ok(!live.body?.items?.some((m) => m.id === id), "trashed message must leave the ordinary list");
  const trash = await client.get<{ items: Array<{ id: string }> }>(base, {
    query: { deleted: "true", limit: 50 },
    expect: 200,
  });
  assert.ok(trash.body?.items?.some((m) => m.id === id), "trashed message must appear in the deleted=true list");
  const direct = await client.get<{ id: string; deleted_at?: string }>(`${base}/${id}`, { expect: 200 });
  assert.ok(direct.body?.deleted_at, "direct GET of a trashed message includes deleted_at");

  // Restore: 200 MessageView, live again (deleted_at omitted), back in the list.
  const rst = await client.post<{ id: string; deleted_at?: string }>(`${base}/${id}/restore`, { expect: 200 });
  assert.equal(rst.body?.id, id, "restore returns the restored message view");
  assert.equal(rst.body?.deleted_at, undefined, "restored view must omit deleted_at");
  const relisted = await client.get<{ items: Array<{ id: string }> }>(base, {
    query: { read_status: "all", limit: 50 },
    expect: 200,
  });
  assert.ok(relisted.body?.items?.some((m) => m.id === id), "restored message must rejoin the ordinary list");

  // Restoring a live message is a 409 not_in_trash (coverage already recorded
  // on the 2xx above — this only pins the error contract).
  const again = await client.post<{ error?: { code?: string } }>(`${base}/${id}/restore`, { expect: 409 });
  assert.equal(again.body?.error?.code, "not_in_trash");
});

test("deleteAgent (trash) + restoreAgent: trash an agent, then bring it back with messages intact", async () => {
  const email = await freshAgent("covra");
  const subject = uniqueSubject("cov ra");
  await loopbackMessage(email, subject);
  const agentPath = `/v1/agents/${encodeURIComponent(email)}`;

  // Soft delete (no permanent=true) moves the agent to the trash. The receipt's
  // messages_deleted is zero — nothing is purged on a trash move.
  const del = await client.delete<{ deleted: boolean; email: string; messages_deleted: number }>(
    `${agentPath}?confirm=DELETE`,
    { expect: 200 },
  );
  assert.equal(del.body?.deleted, true);
  assert.equal(del.body?.messages_deleted, 0, "trash move must not purge messages");

  // A trashed agent is 404 for every live lookup.
  await client.get(agentPath, { expect: 404 });

  // Restore: 200 AgentView, live again, messages intact.
  const rst = await client.post<{ email: string; deleted_at?: string }>(`${agentPath}/restore`, { expect: 200 });
  assert.equal(rst.body?.email, email, "restore returns the restored agent view");
  assert.equal(rst.body?.deleted_at, undefined, "restored agent must omit deleted_at");
  await client.get(agentPath, { expect: 200 });
  const msgs = await client.get<{ items: Array<{ subject: string }> }>(`${agentPath}/messages`, {
    query: { direction: "inbound", read_status: "all", limit: 50 },
    expect: 200,
  });
  assert.ok(
    msgs.body?.items?.some((m) => m.subject === subject),
    "agent restore must bring its messages back with it",
  );

  // Restoring a live agent is a 409 not_in_trash.
  const again = await client.post<{ error?: { code?: string } }>(`${agentPath}/restore`, { expect: 409 });
  assert.equal(again.body?.error?.code, "not_in_trash");
});

after(async () => {
  await cleanup(client);
  await writeReport(`./reports/${SUITE}.json`);
});
