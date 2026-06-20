// Webhook signature verification for the /v1 webhooks resource (Slice 8b).
//
// e2a signs each subscriber delivery with HMAC-SHA256 keyed by the webhook's
// per-webhook signing_secret (whsec_...), Stripe-style:
//
//   X-E2A-Signature: t=<unix>,v1=<hex>[,v1=<hex>]
//
// During the 24h rotation grace each delivery carries two v1= pairs (one per
// active secret); accept if ANY matches. The timestamp guards replay — reject
// anything older than the tolerance (default 5 min). The signed payload is
// `${t}.${rawBody}` — pass the RAW request body bytes; re-stringifying the
// parsed JSON will not match (whitespace/key-order differ).

import { createHmac, timingSafeEqual } from "crypto";
import { E2AWebhookSignatureError } from "./errors.js";

export interface VerifySignatureOptions {
  /** Raw HTTP request body bytes. */
  rawBody: string | Buffer;
  /** Value of the X-E2A-Signature header. */
  header: string;
  /** Webhook signing secret (whsec_...). Pass an array to verify one handler
   *  against several endpoints' secrets. */
  secret: string | string[];
  /** Tolerance in seconds; defaults to 300. */
  toleranceSeconds?: number;
  /** Test-only clock override; defaults to Date.now(). */
  now?: () => number;
}

/**
 * Verify an X-E2A-Signature header. Returns true on success, false on any
 * failure (bad format, missing pair, signature mismatch, replay). Never throws.
 */
export function verifyWebhookSignature(opts: VerifySignatureOptions): boolean {
  // Guard a missing/non-string header (e.g. req.headers[...] is undefined) so a
  // missing X-E2A-Signature is a clean `false`, never a raw TypeError (WH-SIG-1).
  if (!opts.header || typeof opts.header !== "string") return false;
  const tolerance = opts.toleranceSeconds ?? 300;
  const nowMs = opts.now ? opts.now() : Date.now();

  let t = "";
  const v1s: string[] = [];
  for (const part of opts.header.split(",")) {
    const trimmed = part.trim();
    if (trimmed.startsWith("t=")) t = trimmed.slice(2);
    else if (trimmed.startsWith("v1=")) v1s.push(trimmed.slice(3));
  }
  if (!t || v1s.length === 0) return false;

  const ts = Number(t);
  if (!Number.isFinite(ts)) return false;
  if (Math.abs(nowMs / 1000 - ts) > tolerance) return false;

  const body = typeof opts.rawBody === "string" ? opts.rawBody : opts.rawBody.toString("utf8");
  const secrets = Array.isArray(opts.secret) ? opts.secret : [opts.secret];

  for (const secret of secrets) {
    const expectedHex = createHmac("sha256", secret).update(`${t}.${body}`).digest("hex");
    const expectedBytes = Buffer.from(expectedHex, "hex");
    for (const candidate of v1s) {
      if (candidate.length !== expectedHex.length) continue;
      const candidateBytes = Buffer.from(candidate, "hex");
      if (candidateBytes.length !== expectedBytes.length) continue;
      if (timingSafeEqual(candidateBytes, expectedBytes)) return true;
    }
  }
  return false;
}

/** A verified webhook event. The per-event `data` shape is typed once the
 *  server emits per-type payload schemas (a tracked follow-up); until then it
 *  is `unknown` and callers narrow on `type`. */
export interface WebhookEvent {
  id?: string;
  type: string;
  created_at?: string;
  data: unknown;
  [k: string]: unknown;
}

export interface ConstructEventOptions {
  toleranceSeconds?: number;
  now?: () => number;
}

function sigError(code: string, message: string): E2AWebhookSignatureError {
  return new E2AWebhookSignatureError({ code, message, status: 0, retryable: false });
}

/**
 * Verify a delivery and parse it into a typed {@link WebhookEvent} in one call
 * (Stripe's `constructEvent` shape). Throws {@link E2AWebhookSignatureError} on
 * a bad signature, a replay outside tolerance, or an unparseable body. This is
 * the recommended path — no separate verify step needed. Call it from your
 * webhook handler with the RAW request body.
 */
export function constructEvent(
  rawBody: string | Buffer,
  header: string,
  secret: string | string[],
  opts: ConstructEventOptions = {},
): WebhookEvent {
  if (!verifyWebhookSignature({ rawBody, header, secret, toleranceSeconds: opts.toleranceSeconds, now: opts.now })) {
    throw sigError("webhook_signature_invalid", "webhook signature verification failed");
  }
  const text = typeof rawBody === "string" ? rawBody : rawBody.toString("utf8");
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    throw sigError("webhook_body_invalid", "webhook body is not valid JSON");
  }
  if (!parsed || typeof parsed !== "object" || typeof (parsed as { type?: unknown }).type !== "string") {
    throw sigError("webhook_body_invalid", "webhook event is missing a string `type`");
  }
  return parsed as WebhookEvent;
}
