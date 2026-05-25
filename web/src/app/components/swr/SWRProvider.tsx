"use client";

// SWRConfig wrapper for the (app) authenticated surface. Centralizes
// the freshness policy so individual `useSWR` call sites don't each
// have to opt in:
//
//   - revalidateOnFocus      — refetch when the browser tab regains
//                               focus. Covers "user did the action in
//                               another tab / CLI and came back".
//   - revalidateOnReconnect  — refetch after a network drop recovers.
//   - dedupingInterval       — coalesce identical concurrent requests
//                               for 2s so a render burst doesn't fan
//                               out to 5 network calls.
//   - keepPreviousData       — show the previous fetch while
//                               revalidating in the background so
//                               navigation between agents (or any
//                               dependent key change) doesn't flash a
//                               loading state.
//
// Mutations are not auto-invalidated by SWR — that's a deliberate
// design choice (SWR doesn't know which queries to invalidate when an
// arbitrary POST happens). Each mutation site calls `mutate(key)` (or
// `mutate(keyMatcher)`) explicitly. See lib/swrKeys.ts for the
// canonical key strings and `mutateAgents()` / `mutatePending()`
// helpers that wrap the common invalidations.

import { SWRConfig } from "swr";
import type { ReactNode } from "react";

export function SWRProvider({ children }: { children: ReactNode }) {
  return (
    <SWRConfig
      value={{
        revalidateOnFocus: true,
        revalidateOnReconnect: true,
        dedupingInterval: 2000,
        keepPreviousData: true,
        // SWR's default error retry is 5 attempts; that's heavy for
        // background refresh. Cap at 2 — auth/404 errors don't
        // benefit from retry, and the dashboard error states are
        // already legible.
        errorRetryCount: 2,
        errorRetryInterval: 5000,
        // Don't retry on 4xx — the request<T> helper throws ApiError
        // with status; respect 401/403/404 immediately.
        onErrorRetry: (error, _key, _config, revalidate, { retryCount }) => {
          const status = (error as { status?: number })?.status;
          if (status && status >= 400 && status < 500) return;
          if (retryCount >= 2) return;
          setTimeout(() => revalidate({ retryCount }), 5000);
        },
      }}
    >
      {children}
    </SWRConfig>
  );
}
