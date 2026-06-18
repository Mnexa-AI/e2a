"use client";

// Inbox (threaded) — primary per-agent screen.
// Two-column grid: thread list (360px) | thread detail (1fr).
// Threads grouped client-side over a 100-row window of mixed inbound +
// outbound messages from `GET /v1/agents/{address}/messages?direction=all`.
// Server-side conversations endpoint is a tracked follow-up; until it
// lands, the window may starve old threads for accounts with >100
// recent messages.
//
// Selection state lives in `window.location.hash` (#conv:X or #orphan:X)
// so deep-links work and the back button moves between threads.

import { Suspense, useMemo, useState, useSyncExternalStore } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import useSWR from "swr";
import { listAgentMessages } from "../../../../components/onboarding/api";
import type { MessageSummary } from "../../../../components/types";
import { ThreadList } from "../../../../components/messages/ThreadList";
import { ThreadDetail } from "../../../../components/messages/ThreadDetail";
import { findThread, groupIntoThreads } from "../../../../components/messages/threading";
import { agentMessagesKey } from "../../../../../lib/swrKeys";

// Sync the URL fragment into React state. useSyncExternalStore is the
// idiomatic way to read browser-owned state without effect ping-pong.
function getHash(): string {
  if (typeof window === "undefined") return "";
  return window.location.hash ? window.location.hash.slice(1) : "";
}
function subscribeHash(onChange: () => void) {
  window.addEventListener("hashchange", onChange);
  return () => window.removeEventListener("hashchange", onChange);
}
function useUrlHash(): string {
  return useSyncExternalStore(subscribeHash, getHash, () => "");
}

// AgentInboxPage wraps the content in <Suspense>. Next.js 16+ requires
// useSearchParams() to live inside a Suspense boundary; otherwise the
// whole route opts into client-only rendering and any future server
// component above this page silently bails the static export.
export default function AgentInboxPage() {
  return (
    <Suspense fallback={null}>
      <AgentInboxContent />
    </Suspense>
  );
}

function AgentInboxContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const email = searchParams.get("email") ?? "";

  // Initial 100-row window. SWR keys by email so navigating between
  // agents fetches independently; mutations on the focus page call
  // `invalidateAgentMessages(email)` to refresh this query.
  const {
    data: initialPage,
    error: fetchError,
  } = useSWR(
    email ? agentMessagesKey(email, "all", "all") : null,
    () => listAgentMessages(email, { direction: "all", status: "all", pageSize: 100 }),
    // `keepPreviousData` is on globally for the smooth-revalidation
    // UX, but for per-agent keys it shows the WRONG agent's data
    // during a ?email=A → ?email=B switch. Disable here so the page
    // flashes a brief loading state instead of mis-attributing
    // messages to the new agent.
    { keepPreviousData: false },
  );

  // "Load older" appends additional pages keyed by the prior page's
  // next_cursor. We keep these in local state because SWR's cache key
  // would need the cursor in it (defeating the dedup) — appended
  // pages are append-only so a separate state ref works fine.
  const [olderPages, setOlderPages] = useState<MessageSummary[][]>([]);
  const [latestCursor, setLatestCursor] = useState<string | null | undefined>(undefined);
  const [loadingMore, setLoadingMore] = useState(false);
  const [loadError, setLoadError] = useState("");

  const initialMessages = initialPage?.items ?? [];
  // Concatenate the initial page with any imperatively-loaded older
  // pages, then de-dupe by `message_id`. The de-dupe matters because
  // SWR can revalidate the initial page mid-session (focus event,
  // explicit invalidation from the focus page's approve flow). New
  // messages arriving at the top push the initial-page boundary
  // down, which can re-include rows that already live in
  // `olderPages`. Without this de-dupe, the same message renders
  // twice in the thread bucket and `msgCount` lies.
  const rows: MessageSummary[] = useMemo(() => {
    const seen = new Set<string>();
    const out: MessageSummary[] = [];
    for (const m of [...initialMessages, ...olderPages.flat()]) {
      if (seen.has(m.message_id)) continue;
      seen.add(m.message_id);
      out.push(m);
    }
    return out;
  }, [initialMessages, olderPages]);
  // The cursor to use for the next "Load older" click is the most
  // recent next_cursor we've seen (either from the initial fetch or
  // the latest appended page).
  const nextCursor: string | null =
    latestCursor !== undefined ? latestCursor : (initialPage?.next_cursor ?? null);

  const threads = useMemo(
    () => (rows.length > 0 ? groupIntoThreads(rows) : []),
    [rows],
  );
  const hash = useUrlHash();
  const selected = findThread(threads, hash);
  const pendingCount = threads.filter((t) => t.state === "pending").length;
  const error = loadError || (fetchError ? fetchError.message || "Failed to load messages" : "");

  const selectThread = (key: string) => {
    if (typeof window !== "undefined") {
      // history.replaceState skips a scroll-to-top + skips a navigation
      // entry. window.location.hash = … would push a new entry, which
      // makes Back move between threads — pleasant for keyboard, but
      // chatty in the browser history. replace keeps Back going to
      // /dashboard.
      window.history.replaceState(null, "", `#${key}`);
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    }
  };

  // The focus page's MessageView detail carries NEITHER `direction` nor
  // `hitl_status` (and blanks `from`/`status` on outbound), so we thread
  // both off the list row (MessageSummaryView has them) into the URL:
  //   &direction=<inbound|outbound>  → picks the detail projection
  //   &pending=1                     → gates approve/reject
  // The focus page defaults to inbound / not-pending when absent.
  const focusUrl = (m: MessageSummary, withHeaders: boolean) => {
    const pending = m.hitl_status === "pending_approval" ? "&pending=1" : "";
    return (
      `/dashboard/agents/messages/view?email=${encodeURIComponent(email)}` +
      `&id=${encodeURIComponent(m.message_id)}` +
      `&direction=${m.direction}${pending}` +
      (withHeaders ? "&headers=1" : "")
    );
  };
  const openMessage = (m: MessageSummary) => {
    router.push(focusUrl(m, false));
  };
  const openMessageWithHeaders = (m: MessageSummary) => {
    router.push(focusUrl(m, true));
  };

  const loadOlder = async () => {
    if (!nextCursor) return;
    // Capture the email at call time so we can detect a navigation
    // (?email=… changed) before the response lands. Without this, a
    // late response would merge into the wrong agent's rows.
    const startEmail = email;
    setLoadingMore(true);
    setLoadError("");
    try {
      const res = await listAgentMessages(startEmail, {
        direction: "all",
        status: "all",
        pageSize: 100,
        cursor: nextCursor,
      });
      if (startEmail !== email) return;
      setOlderPages((prev) => [...prev, res.items]);
      setLatestCursor(res.next_cursor ?? null);
    } catch (err) {
      if (startEmail !== email) return;
      setLoadError(err instanceof Error ? err.message : "Failed to load older messages");
    } finally {
      if (startEmail === email) setLoadingMore(false);
    }
  };

  return (
    <div
      data-testid="agent-inbox"
      className="grid grid-cols-1 md:grid-cols-[360px_minmax(0,1fr)]"
      style={{
        borderTop: "1px solid var(--border)",
        // Fill remaining viewport under (app) chrome + agent header.
        minHeight: "calc(100vh - var(--chrome-h) - 200px)",
      }}
    >
      <ThreadList
        threads={threads}
        selectedKey={selected?.key ?? null}
        onSelect={selectThread}
        total={threads.length}
        pendingCount={pendingCount}
        hasMore={!!nextCursor}
        onLoadMore={loadOlder}
        loadingMore={loadingMore}
      />
      <div className="flex flex-col min-h-0">
        {error && (
          <div
            className="m-6 p-4 text-[13px]"
            style={{
              background: "var(--danger-bg)",
              border: "1px solid var(--danger-bg)",
              color: "var(--danger-strong)",
              borderRadius: "var(--r-md)",
            }}
          >
            {error}
          </div>
        )}
        {!error && !initialPage && (
          <div
            className="px-7 py-8 text-[13px]"
            style={{ color: "var(--fg-muted)" }}
          >
            Loading inbox…
          </div>
        )}
        {!error && initialPage && (
          <ThreadDetail
            thread={selected}
            agentEmail={email}
            onOpenMessage={openMessage}
            onOpenHeaders={openMessageWithHeaders}
          />
        )}
      </div>
    </div>
  );
}
