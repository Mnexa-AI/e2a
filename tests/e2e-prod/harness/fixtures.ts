import { randomBytes } from "node:crypto";
import type { ApiClient, RawResponse } from "./client.ts";

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

// holdAllOutbound replaces the retired `hitl_enabled` flag. It sets an
// outbound review gate with policy=allowlist + action=review and an empty
// allowlist, so every recipient is unknown and every send is held for
// review (status=pending_review). The /protection sub-resource is a full
// replace (PUT), so we send the complete inbound/outbound/holds shape.
export function holdAllOutbound<T = unknown>(
  client: ApiClient,
  email: string,
): Promise<RawResponse<T>> {
  return client.put<T>(`/v1/agents/${encodeURIComponent(email)}/protection`, {
    body: {
      inbound: { gate: {}, scan: {} },
      outbound: { gate: { policy: "allowlist", action: "review", allowlist: [] }, scan: {} },
      holds: {},
    },
  });
}
