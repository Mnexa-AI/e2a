import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { components } from "@e2a/sdk/v1";
import { createClient } from "../sdk.js";

type Schemas = components["schemas"];
type PendingSummary = Schemas["PendingMessageSummary"];
type PendingDetail = Schemas["PendingMessageDetail"];
type ApproveOverrides = Schemas["ApprovePendingMessageRequest"];

// formatExpiresIn renders a compact "in 2h 15m" / "expired" label so
// `pending list` output is scannable at a terminal width without
// forcing the reviewer to compare timestamps in their head.
function formatExpiresIn(iso?: string): string {
  if (!iso) return "—";
  const diffMs = new Date(iso).getTime() - Date.now();
  if (diffMs <= 0) return "expired";
  const mins = Math.floor(diffMs / 60_000);
  if (mins < 60) return `in ${mins}m`;
  const hours = Math.floor(mins / 60);
  const m = mins % 60;
  if (hours < 24) return m === 0 ? `in ${hours}h` : `in ${hours}h ${m}m`;
  const days = Math.floor(hours / 24);
  const h = hours % 24;
  return h === 0 ? `in ${days}d` : `in ${days}d ${h}h`;
}

export async function pendingList(): Promise<void> {
  const client = createClient();
  const res = await client.listPendingMessages();
  const msgs = (res.messages ?? []) as PendingSummary[];
  if (msgs.length === 0) {
    process.stdout.write("No messages pending approval.\n");
    return;
  }
  for (const m of msgs) {
    const recipients = [...(m.to ?? []), ...(m.cc ?? [])].join(", ") || "(no recipients)";
    const expires = formatExpiresIn(m.approval_expires_at);
    process.stdout.write(
      `${m.id}  ${m.agent_id} → ${recipients}  "${m.subject}"  ${expires}\n`,
    );
  }
}

export async function pendingShow(id: string | undefined): Promise<void> {
  if (!id) {
    process.stderr.write("Usage: e2a pending show <message-id>\n");
    process.exit(1);
  }
  const client = createClient();
  const m = (await client.getPendingMessage(id)) as PendingDetail;
  const lines: string[] = [];
  lines.push(`ID:        ${m.id}`);
  lines.push(`Agent:     ${m.agent_id}`);
  lines.push(`Status:    ${m.status}`);
  if (m.type) lines.push(`Type:      ${m.type}`);
  lines.push(`To:        ${(m.to ?? []).join(", ")}`);
  if (m.cc?.length) lines.push(`Cc:        ${m.cc.join(", ")}`);
  if (m.bcc?.length) lines.push(`Bcc:       ${m.bcc.join(", ")}`);
  lines.push(`Subject:   ${m.subject}`);
  if (m.conversation_id) lines.push(`ConvID:    ${m.conversation_id}`);
  if (m.approval_expires_at) {
    lines.push(`Expires:   ${m.approval_expires_at} (${formatExpiresIn(m.approval_expires_at)})`);
  }
  if (m.edited) lines.push(`Edited:    true`);
  lines.push("");
  lines.push("--- Body ---");
  lines.push(m.body_text ?? "(no plain body — body was scrubbed or HTML-only)");
  if (m.body_html) {
    lines.push("");
    lines.push("--- HTML body ---");
    lines.push(m.body_html);
  }
  if (m.attachments?.length) {
    lines.push("");
    lines.push("--- Attachments ---");
    for (const a of m.attachments) {
      lines.push(`  ${a.filename}  (${a.content_type})`);
    }
  }
  process.stdout.write(lines.join("\n") + "\n");
}

// editableDocFromMessage renders the minimal set of fields a reviewer
// can edit into a YAML-ish plain-text doc. We avoid a full YAML parser —
// each field is a single line prefixed with a known label, and the body
// lives below a fenced separator. Keeps the UX readable and the parser
// trivial.
function editableDocFromMessage(m: PendingDetail): string {
  return [
    `# Edit fields below and save to approve with changes.`,
    `# Lines starting with '#' are ignored. Do not rename the field labels.`,
    ``,
    `subject: ${m.subject ?? ""}`,
    `to: ${(m.to ?? []).join(", ")}`,
    `cc: ${(m.cc ?? []).join(", ")}`,
    `bcc: ${(m.bcc ?? []).join(", ")}`,
    ``,
    `--- body ---`,
    m.body_text ?? "",
  ].join("\n");
}

function parseEditableDoc(doc: string): {
  subject?: string;
  to?: string[];
  cc?: string[];
  bcc?: string[];
  body?: string;
} {
  const lines = doc.split("\n");
  const out: {
    subject?: string;
    to?: string[];
    cc?: string[];
    bcc?: string[];
    body?: string;
  } = {};
  let i = 0;
  for (; i < lines.length; i++) {
    const line = lines[i];
    if (line.trim() === "--- body ---") {
      i++;
      break;
    }
    if (line.startsWith("#") || line.trim() === "") continue;
    const colonAt = line.indexOf(":");
    if (colonAt < 0) continue;
    const key = line.slice(0, colonAt).trim().toLowerCase();
    const val = line.slice(colonAt + 1).trim();
    switch (key) {
      case "subject":
        out.subject = val;
        break;
      case "to":
      case "cc":
      case "bcc":
        (out as Record<string, string[]>)[key] = val
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean);
        break;
    }
  }
  if (i < lines.length) {
    out.body = lines.slice(i).join("\n").replace(/\n+$/, "");
  }
  return out;
}

// diffOverrides compares the parsed editor output against the original
// message and returns only the fields the reviewer actually changed.
// Keeps the server-side `edited` flag truthful.
function diffOverrides(
  parsed: ReturnType<typeof parseEditableDoc>,
  original: PendingDetail,
): ApproveOverrides {
  const out: ApproveOverrides = {};
  if (parsed.subject !== undefined && parsed.subject !== (original.subject ?? "")) {
    out.subject = parsed.subject;
  }
  if (parsed.body !== undefined && parsed.body !== (original.body_text ?? "")) {
    out.body_text = parsed.body;
  }
  const arrEq = (a: string[] | undefined, b: string[] | undefined) =>
    JSON.stringify(a ?? []) === JSON.stringify(b ?? []);
  if (parsed.to && !arrEq(parsed.to, original.to)) out.to = parsed.to;
  if (parsed.cc && !arrEq(parsed.cc, original.cc)) out.cc = parsed.cc;
  if (parsed.bcc && !arrEq(parsed.bcc, original.bcc)) out.bcc = parsed.bcc;
  return out;
}

// openEditor spawns $EDITOR (or $VISUAL, or vi fallback) on a temp file
// and returns the edited text. Blocks until the editor exits.
function openEditor(initial: string): string {
  const editor = process.env.VISUAL || process.env.EDITOR || "vi";
  const dir = mkdtempSync(join(tmpdir(), "e2a-approve-"));
  const path = join(dir, "approve.txt");
  writeFileSync(path, initial);
  const res = spawnSync(editor, [path], { stdio: "inherit" });
  if (res.status !== 0) {
    throw new Error(`editor exited with status ${res.status}`);
  }
  return readFileSync(path, "utf8");
}

export async function pendingApprove(
  id: string | undefined,
  opts: { edit?: boolean; idempotencyKey?: string },
): Promise<void> {
  if (!id) {
    process.stderr.write("Usage: e2a pending approve <message-id> [--edit] [--idempotency-key <key>]\n");
    process.exit(1);
  }

  const client = createClient();

  // We need the message's owning agent email to call the agent-scoped
  // approve endpoint. Fetch the detail unconditionally — even on the
  // non-edit path — because the alternative (asking the user to pass
  // --agent) is brittle and easy to typo. The lookup is a single GET
  // and gates a side-effectful POST, so the extra round trip pays for
  // itself in fewer 404s.
  const original = (await client.getPendingMessage(id)) as PendingDetail;
  if (original.status !== "pending_approval") {
    process.stderr.write(
      `Cannot ${opts.edit ? "edit" : "approve"}: message is ${original.status}, not pending.\n`,
    );
    process.exit(1);
  }
  // PendingMessageDetail.agent_id is `string | undefined` in the
  // generated OpenAPI types — the field is non-optional on the wire
  // but swag emits it as optional. The status guard above already
  // proves the row exists, so a missing agent_id here is a server
  // bug; bail loudly rather than crashing in the URL builder.
  if (!original.agent_id) {
    process.stderr.write(`Server returned a pending message without an agent_id (id=${id}).\n`);
    process.exit(1);
  }
  const agentEmail = original.agent_id;

  let overrides: ApproveOverrides = {};
  if (opts.edit) {
    const edited = openEditor(editableDocFromMessage(original));
    overrides = diffOverrides(parseEditableDoc(edited), original);
  }

  const res = opts.idempotencyKey !== undefined
    ? await client.approveMessage(agentEmail, id, overrides, { idempotencyKey: opts.idempotencyKey })
    : await client.approveMessage(agentEmail, id, overrides);
  const editedNote = res.edited ? " (with edits)" : "";
  process.stdout.write(
    `Approved: ${res.message_id} → ${res.provider_message_id ?? ""}${editedNote}\n`,
  );
}

export async function pendingReject(
  id: string | undefined,
  reason: string | undefined,
): Promise<void> {
  if (!id) {
    process.stderr.write('Usage: e2a pending reject <message-id> [--reason "..."]\n');
    process.exit(1);
  }
  const client = createClient();
  // Same agent-email-discovery pattern as approve.
  const original = (await client.getPendingMessage(id)) as PendingDetail;
  if (!original.agent_id) {
    process.stderr.write(`Server returned a pending message without an agent_id (id=${id}).\n`);
    process.exit(1);
  }
  await client.rejectMessage(original.agent_id, id, reason ?? "");
  process.stdout.write(`Rejected: ${id}\n`);
}
