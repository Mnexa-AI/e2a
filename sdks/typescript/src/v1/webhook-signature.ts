// Webhook signature verification for the top-level webhooks resource.
//
// e2a signs each subscriber delivery with HMAC-SHA256 keyed by the
// webhook's signing_secret. The signature is sent on the X-E2A-Signature
// header in Stripe-style format:
//
//   X-E2A-Signature: t=<unix>,v1=<hex>[,v1=<hex>]
//
// During the 24h rotation grace window each delivery carries two v1=
// pairs (one per active secret). Receivers should accept the request if
// ANY of the v1= signatures matches. The timestamp guards against replay
// — reject anything older than the configured tolerance (default 5 min).
//
// The signed payload is `${t}.${rawBody}`. Pass the raw request body
// bytes — JSON.stringify-ing the parsed body will not match because of
// whitespace and key-order differences.

import { createHmac, timingSafeEqual } from "crypto";

export interface VerifySignatureOptions {
  /** Raw HTTP request body bytes. */
  rawBody: string | Buffer;
  /** Value of the X-E2A-Signature header. */
  header: string;
  /** Webhook signing secret (whsec_...). */
  secret: string;
  /** Tolerance in seconds; defaults to 300. */
  toleranceSeconds?: number;
  /** Test-only clock override; defaults to Date.now(). */
  now?: () => number;
}

/**
 * Verify an X-E2A-Signature header. Returns true on success, false on
 * any failure (bad format, missing pair, signature mismatch, replay).
 * Throws nothing — designed for use directly in an HTTP handler's
 * branch decision.
 */
export function verifyWebhookSignature(opts: VerifySignatureOptions): boolean {
  const tolerance = opts.toleranceSeconds ?? 300;
  const nowMs = opts.now ? opts.now() : Date.now();

  // Parse t=... and one or more v1=... pairs.
  let t = "";
  const v1s: string[] = [];
  for (const part of opts.header.split(",")) {
    const trimmed = part.trim();
    if (trimmed.startsWith("t=")) {
      t = trimmed.slice(2);
    } else if (trimmed.startsWith("v1=")) {
      v1s.push(trimmed.slice(3));
    }
  }
  if (!t || v1s.length === 0) return false;

  const ts = Number(t);
  if (!Number.isFinite(ts)) return false;
  const ageSeconds = Math.abs(nowMs / 1000 - ts);
  if (ageSeconds > tolerance) return false;

  const body = typeof opts.rawBody === "string" ? opts.rawBody : opts.rawBody.toString("utf8");
  const expectedHex = createHmac("sha256", opts.secret)
    .update(`${t}.${body}`)
    .digest("hex");
  const expectedBytes = Buffer.from(expectedHex, "hex");

  for (const candidate of v1s) {
    if (candidate.length !== expectedHex.length) continue;
    const candidateBytes = Buffer.from(candidate, "hex");
    if (candidateBytes.length !== expectedBytes.length) continue;
    if (timingSafeEqual(candidateBytes, expectedBytes)) return true;
  }
  return false;
}
