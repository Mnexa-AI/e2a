import type { ApiClient } from "./client.ts";

type Kind = "agent" | "domain" | "signing_secret";

interface Tracked {
  kind: Kind;
  id: string;
}

const tracked: Tracked[] = [];

export function track(kind: Kind, id: string): void {
  tracked.push({ kind, id });
}

export function untrack(kind: Kind, id: string): void {
  const i = tracked.findIndex((t) => t.kind === kind && t.id === id);
  if (i >= 0) tracked.splice(i, 1);
}

export async function cleanup(client: ApiClient, opts: { force?: boolean } = {}): Promise<{
  attempted: number;
  succeeded: number;
  failed: Array<{ kind: Kind; id: string; reason: string }>;
}> {
  const failed: Array<{ kind: Kind; id: string; reason: string }> = [];
  let succeeded = 0;
  for (const t of [...tracked].reverse()) {
    try {
      const path = pathFor(t);
      const res = await client.delete(path);
      if (res.status === 204 || res.status === 200 || res.status === 404 || res.status === 403) {
        // 403 == "not owned"/already deleted under anti-enumeration semantics
        succeeded++;
        untrack(t.kind, t.id);
      } else {
        failed.push({ ...t, reason: `HTTP ${res.status}: ${res.raw.slice(0, 200)}` });
      }
    } catch (e) {
      failed.push({ ...t, reason: (e as Error).message });
    }
  }
  return { attempted: tracked.length + succeeded, succeeded, failed };
}

function pathFor(t: Tracked): string {
  switch (t.kind) {
    case "agent":
      return `/api/v1/agents/${encodeURIComponent(t.id)}`;
    case "domain":
      return `/api/v1/domains/${encodeURIComponent(t.id)}`;
    case "signing_secret":
      return `/api/v1/users/me/signing-secrets/${encodeURIComponent(t.id)}`;
  }
}

export function getTracked(): readonly Tracked[] {
  return tracked;
}
