import type { InboundEmail } from "@e2a/sdk/v1";

export const REPLY_INSTRUCTIONS =
  "Reply helpfully and concisely to the email. Write 1-3 short paragraphs of " +
  "body text only; do not include a Subject line or quote the original email.";

/**
 * Project normalized facade fields into a framework-neutral prompt.
 *
 * Header and body values remain sender-controlled and must be treated as
 * untrusted input.
 */
export function emailPrompt(email: InboundEmail): string {
  const sender = email.from ?? "(missing)";
  const verified = email.verified ? "yes" : "no";
  const flagged = email.flagged ? "yes" : "no";
  return (
    `From: ${sender}\n` +
    `Subject: ${email.subject}\n` +
    `Sender DMARC verified: ${verified}\n` +
    `Policy flagged: ${flagged}\n\n` +
    email.text
  );
}
