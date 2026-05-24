"use client";

import { useCallback, useEffect, useState } from "react";
import { usePathname } from "next/navigation";
import { listPendingMessages } from "../onboarding/api";

// usePendingCount returns the number of HITL pending_approval messages
// owned by the current user. The Sidebar lives in the persistent (app)
// layout, so without refresh triggers its badge would only update on
// the 30s poll — leaving stale counts after the user approves a draft
// and navigates away. We compensate with three refetch triggers:
//
//   1. Pathname change — covers the common flow (approve on focus
//      page → router.push back to inbox = pathname change = refetch).
//      Catches the symptom the user actually feels.
//   2. Document visibility change — catches "user did the action in
//      another tab or via the CLI, then comes back to this tab".
//   3. Background poll — fallback for in-page mutations that don't
//      trigger navigation (e.g., the inbox PendingCallout's Review
//      flow if it ever lands in-place). 30s is generous; can be
//      tightened if HITL volume grows.
//
// Returns null while the first fetch is in flight and on error, so
// callers can distinguish "unknown" from "zero" and decide how to
// render.
export function usePendingCount(): number | null {
  const [count, setCount] = useState<number | null>(null);
  const pathname = usePathname();

  // Stable load closure — useEffect re-runs depend only on the
  // trigger (pathname). Stuffing it into useCallback also lets the
  // visibilitychange listener share the same fetch path.
  const load = useCallback(async () => {
    try {
      const msgs = await listPendingMessages();
      setCount(msgs.length);
    } catch {
      setCount(null);
    }
  }, []);

  // Refetch on mount + pathname change.
  useEffect(() => {
    let cancelled = false;
    const run = async () => {
      try {
        const msgs = await listPendingMessages();
        if (!cancelled) setCount(msgs.length);
      } catch {
        if (!cancelled) setCount(null);
      }
    };
    run();
    return () => {
      cancelled = true;
    };
  }, [pathname]);

  // 30s background poll — independent of pathname changes so the
  // count stays warm even on long-lived dashboard sessions.
  useEffect(() => {
    const id = setInterval(load, 30_000);
    return () => clearInterval(id);
  }, [load]);

  // Refetch when the tab regains visibility. Catches the "approved
  // via CLI / magic link in another tab" case.
  useEffect(() => {
    if (typeof document === "undefined") return;
    const onVis = () => {
      if (document.visibilityState === "visible") load();
    };
    document.addEventListener("visibilitychange", onVis);
    return () => document.removeEventListener("visibilitychange", onVis);
  }, [load]);

  return count;
}
