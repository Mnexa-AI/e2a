import { createHash } from "node:crypto";

/** Result of trying to claim one at-least-once webhook delivery. */
export type ClaimResult = "new" | "processing" | "processed";

const MAX_CONVERSATION_ID_LENGTH = 200;
const SAFE_CONVERSATION_SUFFIX = /^[A-Za-z0-9_-]+$/u;

/** Return the upstream thread id or a retry-stable first-contact anchor. */
export function conversationIdFor(eventId: string, existing: string | null | undefined): string {
  if (existing !== null && existing !== undefined && existing.trim().length > 0) return existing;
  const suffix = eventId.startsWith("evt_") ? eventId.slice(4) : eventId;
  const candidate = `conv_${suffix}`;
  if (suffix.length > 0 && candidate.length <= MAX_CONVERSATION_ID_LENGTH && SAFE_CONVERSATION_SUFFIX.test(suffix)) {
    return candidate;
  }
  const digest = createHash("sha256").update(eventId, "utf8").digest("hex");
  return `conv_${digest}`;
}

/** Bounded in-process state for side-effect-safe duplicate deliveries. */
export class EventDeduper {
  private readonly maxProcessed: number;
  private readonly processing = new Set<string>();
  private readonly processed = new Set<string>();
  private readonly processedOrder: string[] = [];

  constructor(options: { maxProcessed?: number } = {}) {
    this.maxProcessed = options.maxProcessed ?? 10_000;
    if (!Number.isInteger(this.maxProcessed) || this.maxProcessed <= 0) {
      throw new RangeError("maxProcessed must be a positive integer");
    }
  }

  async claim(eventId: string): Promise<ClaimResult> {
    if (this.processed.has(eventId)) return "processed";
    if (this.processing.has(eventId)) return "processing";
    this.processing.add(eventId);
    return "new";
  }

  async complete(eventId: string): Promise<void> {
    this.processing.delete(eventId);
    if (!this.processed.has(eventId)) {
      this.processed.add(eventId);
      this.processedOrder.push(eventId);
    }
    while (this.processedOrder.length > this.maxProcessed) {
      const expired = this.processedOrder.shift();
      if (expired !== undefined) this.processed.delete(expired);
    }
  }

  async release(eventId: string): Promise<void> {
    this.processing.delete(eventId);
  }
}
