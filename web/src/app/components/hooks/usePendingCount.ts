"use client";

// Sidebar's pending-count badge. Thin wrapper around SWR's
// `usePendingMessages` query — kept as a hook for two reasons:
//
//   1. Callers only need the count, not the list, so we project once
//      here instead of at every call site.
//   2. SWR's `data === undefined` (first fetch in flight or hard
//      error) maps to `null` for back-compat with the old hook's
//      contract — callers can distinguish "unknown" from "zero".
//
// Refresh wiring (focus / reconnect / dedup) lives in SWRProvider's
// config, not here. Mutation sites that drop the count (approve,
// reject) call `invalidatePendingList()` from lib/swrKeys.ts.

import useSWR from "swr";
import { listPendingMessages } from "../onboarding/api";
import { pendingMessagesKey } from "../../../lib/swrKeys";

export function usePendingCount(): number | null {
  const { data, error } = useSWR(pendingMessagesKey, () => listPendingMessages());
  if (error) return null;
  return data ? data.length : null;
}
