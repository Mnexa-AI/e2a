import { randomBytes } from "node:crypto";

const RUN_ID = randomBytes(3).toString("hex");

export function runId(): string {
  return RUN_ID;
}

export function uniqueSlug(prefix = "e2etest"): string {
  return `${prefix}-${RUN_ID}-${randomBytes(3).toString("hex")}`;
}

export function uniqueSubject(label: string): string {
  return `[e2e-${RUN_ID}] ${label} ${Date.now()}`;
}

export function uniqueIdempotencyKey(): string {
  return `idem-${RUN_ID}-${randomBytes(6).toString("hex")}`;
}

export const SINK_EMAIL = process.env.E2E_SINK_EMAIL ?? "blackhole+e2e@e2a.dev";
