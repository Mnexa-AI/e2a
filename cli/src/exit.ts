// Exit codes are the CLI's scripting contract. Harness scripts (tether, hooks,
// CI lanes) branch on these instead of parsing output, so the values are
// frozen once published — add new codes, never renumber.
//
// HELD exists because a held send is an HTTP 200: the API accepted the message
// but parked it in the review queue (`status: "pending_review"`), so the
// recipient got nothing. Scripts that treat "exit 0" as "delivered" would
// silently report into a queue nobody reads — the exact failure mode that bit
// the tether harness twice.
export const EXIT = {
  OK: 0,
  /** Network, server, or unexpected error. */
  ERROR: 1,
  /** Bad flags or arguments. */
  USAGE: 2,
  /** Send accepted but held for review (pending_review) — NOT delivered. */
  HELD: 3,
  /** Bad credentials or wrong key scope for the operation. */
  AUTH: 4,
  /**
   * Permanent request error (404 / 409 / 422 …) — retrying the identical
   * invocation cannot succeed. Distinguished from ERROR so retry-on-1
   * wrappers don't hammer a typo'd message id or an unverified domain.
   */
  REQUEST: 5,
  /** A deadline-bounded wait (`listen --once --until`) expired unmatched. */
  TIMEOUT: 6,
} as const;

/** Write a message to stderr and exit with the given code. */
export function fail(code: number, message: string): never {
  process.stderr.write(message.endsWith("\n") ? message : message + "\n");
  process.exit(code);
}
