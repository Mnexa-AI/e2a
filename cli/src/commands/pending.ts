import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { ApproveRequest, MessageView, RequestOptions } from "@e2a/sdk/v1";
import { createClient, requireAgentEmail } from "../sdk.js";

// The status value the server uses for outbound drafts held for human
// approval. Held drafts surface as outbound messages carrying this status.
const PENDING_STATUS = "pending_approval";

export async function pendingList(from?: string): Promise<void> {
  const client = createClient({ from });
  const address = requireAgentEmail(from);
  // The v1 surface has no cross-agent pending endpoint. Held outbound
  // drafts appear in the per-agent message list with
  // status="pending_approval"; list the agent's outbound messages and
  // filter to the held ones.
  const outbound = await client.messages
    .list(address, { direction: "outbound" })
    .toArray({ limit: 1000 });
  const msgs = outbound.filter((m) => m.status === PENDING_STATUS);
  if (msgs.length === 0) {
    process.stdout.write("No messages pending approval.\n");
    return;
  }
  for (const m of msgs) {
    const recipients = [...(m.to ?? []), ...(m.cc ?? [])].join(", ") || "(no recipients)";
    process.stdout.write(
      `${m.messageId}  ${m._from ?? address} → ${recipients}  "${m.subject}"\n`,
    );
  }
}

export async function pendingShow(id: string | undefined, from?: string): Promise<void> {
  if (!id) {
    process.stderr.write("Usage: e2a pending show <message-id>\n");
    process.exit(1);
  }
  const client = createClient({ from });
  const address = requireAgentEmail(from);
  const m = await client.messages.get(address, id);
  const lines: string[] = [];
  lines.push(`ID:        ${m.messageId}`);
  lines.push(`Agent:     ${address}`);
  lines.push(`Status:    ${m.status}`);
  lines.push(`To:        ${(m.to ?? []).join(", ")}`);
  if (m.cc?.length) lines.push(`Cc:        ${m.cc.join(", ")}`);
  lines.push(`Subject:   ${m.subject}`);
  if (m.conversationId) lines.push(`ConvID:    ${m.conversationId}`);
  lines.push("");
  lines.push("--- Body ---");
  const bodyText = m.parsed?.text || m.body?.text;
  lines.push(bodyText ?? "(no plain body — body was scrubbed or HTML-only)");
  if (m.body?.html) {
    lines.push("");
    lines.push("--- HTML body ---");
    lines.push(m.body.html);
  }
  process.stdout.write(lines.join("\n") + "\n");
}

// editableDocFromMessage renders the minimal set of fields a reviewer
// can edit into a YAML-ish plain-text doc. We avoid a full YAML parser —
// each field is a single line prefixed with a known label, and the body
// lives below a fenced separator. Keeps the UX readable and the parser
// trivial.
function editableDocFromMessage(m: MessageView): string {
  return [
    `# Edit fields below and save to approve with changes.`,
    `# Lines starting with '#' are ignored. Do not rename the field labels.`,
    ``,
    `subject: ${m.subject ?? ""}`,
    `to: ${(m.to ?? []).join(", ")}`,
    `cc: ${(m.cc ?? []).join(", ")}`,
    ``,
    `--- body ---`,
    (m.parsed?.text || m.body?.text) ?? "",
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
  original: MessageView,
): ApproveRequest {
  const out: ApproveRequest = {};
  const originalBody = (original.parsed?.text || original.body?.text) ?? "";
  if (parsed.subject !== undefined && parsed.subject !== (original.subject ?? "")) {
    out.subject = parsed.subject;
  }
  if (parsed.body !== undefined && parsed.body !== originalBody) {
    out.bodyText = parsed.body;
  }
  const arrEq = (a: string[] | undefined, b: string[] | undefined) =>
    JSON.stringify(a ?? []) === JSON.stringify(b ?? []);
  if (parsed.to && !arrEq(parsed.to, original.to ?? undefined)) out.to = parsed.to;
  if (parsed.cc && !arrEq(parsed.cc, original.cc ?? undefined)) out.cc = parsed.cc;
  if (parsed.bcc && parsed.bcc.length) out.bcc = parsed.bcc;
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
  opts: { edit?: boolean; idempotencyKey?: string; from?: string },
): Promise<void> {
  if (!id) {
    process.stderr.write("Usage: e2a pending approve <message-id> [--edit] [--idempotency-key <key>]\n");
    process.exit(1);
  }

  const client = createClient({ from: opts.from });
  const address = requireAgentEmail(opts.from);

  // Fetch the message first to confirm it is still held. The lookup is a
  // single GET and gates a side-effectful POST, so the extra round trip
  // pays for itself in fewer surprises.
  const original = await client.messages.get(address, id);
  if (original.status !== PENDING_STATUS) {
    process.stderr.write(
      `Cannot ${opts.edit ? "edit" : "approve"}: message is ${original.status}, not pending.\n`,
    );
    process.exit(1);
  }

  let overrides: ApproveRequest = {};
  if (opts.edit) {
    const edited = openEditor(editableDocFromMessage(original));
    overrides = diffOverrides(parseEditableDoc(edited), original);
  }

  const reqOpts: RequestOptions | undefined = opts.idempotencyKey !== undefined
    ? { idempotencyKey: opts.idempotencyKey }
    : undefined;
  const res = await client.messages.approve(address, id, overrides, reqOpts);
  const editedNote = res.edited ? " (with edits)" : "";
  process.stdout.write(
    `Approved: ${res.messageId} → ${res.providerMessageId ?? ""}${editedNote}\n`,
  );
}

export async function pendingReject(
  id: string | undefined,
  reason: string | undefined,
  from?: string,
): Promise<void> {
  if (!id) {
    process.stderr.write('Usage: e2a pending reject <message-id> [--reason "..."]\n');
    process.exit(1);
  }
  const client = createClient({ from });
  const address = requireAgentEmail(from);
  await client.messages.reject(address, id, { reason: reason ?? "" });
  process.stdout.write(`Rejected: ${id}\n`);
}
