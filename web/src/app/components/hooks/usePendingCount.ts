"use client";

import { useEffect, useState } from "react";
import { listPendingMessages } from "../onboarding/api";

// usePendingCount returns the number of HITL pending_approval messages
// owned by the current user. Polls every 30s so the sidebar badge stays
// roughly fresh without the user needing to reload. Returns null while
// the first fetch is in flight and on error, so callers can distinguish
// "unknown" from "zero" and decide how to render.
export function usePendingCount(): number | null {
  const [count, setCount] = useState<number | null>(null);

  useEffect(() => {
    let cancelled = false;

    const load = async () => {
      try {
        const msgs = await listPendingMessages();
        if (!cancelled) setCount(msgs.length);
      } catch {
        if (!cancelled) setCount(null);
      }
    };

    load();
    const id = setInterval(load, 30_000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  return count;
}
