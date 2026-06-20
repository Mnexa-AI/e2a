import { z } from "zod";

/**
 * Shared attachment schema for outbound mail tools (send_message,
 * reply_to_message, approve_message).
 *
 * Wire shape matches what every other layer (HTTP API, TS + Python
 * SDKs, CLI) already speaks: { filename, content_type, data:base64 }.
 * Defined once here so all three tools validate identically — a
 * future extension (e.g. an Anthropic Files API `file_id` variant)
 * needs to update only this file.
 *
 * Validation philosophy: catch the most common LLM-produced
 * malformations with specific, actionable error messages so the
 * model can retry sanely. The SDK and backend each re-validate as
 * a defense-in-depth layer; we don't try to be the only line.
 */

// MIME type regex per RFC 6838 §4.2 — type/subtype with the allowed
// punctuation. Catches obviously-broken values like "" or "pdf" before
// they reach the SDK. Doesn't enforce IANA-registered types — clients
// legitimately invent custom subtypes ("application/x-foo+json"); the
// shape check is what we care about.
const MIME_TYPE_RE = /^[a-zA-Z][a-zA-Z0-9.+-]*\/[a-zA-Z0-9.+-]+$/;

// RFC 4648 §4 standard alphabet (NOT §5 url-safe). Padding mandatory.
// Catches: url-safe variants (-_ not in set), whitespace insertions,
// truncation that breaks 4-byte alignment.
const BASE64_STANDARD_RE = /^[A-Za-z0-9+/]+={0,2}$/;

// Per-attachment decoded-size cap. Beyond this the LLM has almost
// certainly hit context-window pressure and the base64 is truncated;
// the backend's request cap would reject these anyway. 5 MB matches
// what mature email gateways accept inline before bouncing to a
// shared-storage upload pattern.
const MAX_ATTACHMENT_BYTES = 5 * 1024 * 1024;

export const attachmentInputSchema = z
  .object({
    filename: z
      .string()
      .min(1)
      .max(255)
      .describe(
        "Display filename including extension (e.g. 'report.pdf'). Recipients see this as the suggested save name.",
      ),
    content_type: z
      .string()
      .regex(MIME_TYPE_RE, {
        message:
          "content_type must be a valid MIME type like 'application/pdf' or 'image/png'",
      })
      .describe(
        "MIME type. Required by the backend's MIME composer; an empty or malformed value would produce a broken message.",
      ),
    data: z
      .string()
      .min(1)
      .regex(BASE64_STANDARD_RE, {
        message:
          "data must be standard RFC 4648 §4 base64 with no whitespace; pass the value returned by another tool verbatim and do not encode raw text yourself",
      })
      .refine((s) => s.length % 4 === 0, {
        message:
          "base64 length not divisible by 4 — likely truncated. If you forwarded this from another tool's output, the original string was cut by context-window pressure; try a smaller attachment.",
      })
      .refine(
        (s) => {
          try {
            const decoded = Buffer.from(s, "base64");
            // Round-trip: re-encode and compare. Catches subtle corruption
            // (one char swapped) that passes the regex but produces a
            // different byte stream than the LLM intended.
            if (decoded.toString("base64") !== s) return false;
            return decoded.length > 0 && decoded.length <= MAX_ATTACHMENT_BYTES;
          } catch {
            return false;
          }
        },
        {
          message: `base64 failed decode/round-trip check, was empty, or decoded >${MAX_ATTACHMENT_BYTES} bytes (max ${MAX_ATTACHMENT_BYTES / (1024 * 1024)} MB per attachment)`,
        },
      )
      .describe(
        "Base64-encoded file content (RFC 4648 §4, standard alphabet, padded). Pass values returned verbatim by other tools (file readers, doc generators, get_attachment); do not attempt to encode raw text yourself. Max 5 MB per attachment after decoding.",
      ),
  })
  .describe(
    "One file attachment. Provide filename + MIME content_type + base64-encoded data.",
  );

// Attachments-array schema — reused by send_message, reply_to_message,
// approve_message. Optional in all three: the absence of any
// attachments means a plain message (current behavior preserved).
export const attachmentsArraySchema = z
  .array(attachmentInputSchema)
  .max(20, "too many attachments (max 20 per message)")
  .optional()
  .describe(
    "Optional list of file attachments. Each entry is base64-encoded; the MCP server forwards them verbatim to the e2a backend, which handles MIME composition. To forward an attachment received on an inbound message, fetch its base64 via `get_attachment` and pass the same shape here.",
  );

export type AttachmentInput = z.infer<typeof attachmentInputSchema>;
