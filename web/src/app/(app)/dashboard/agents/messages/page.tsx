"use client";

// Inbox (threaded) — primary per-agent screen.
// Two-column grid: thread list (360px) | thread detail (1fr).
// Threads grouped client-side over a 100-row window of mixed inbound +
// outbound messages from `GET /api/v1/agents/{email}/messages?direction=all`.
// Server-side conversations endpoint is a tracked follow-up; until it
// lands, the window may starve old threads for accounts with >100
// recent messages.
//
// Selection state lives in `window.location.hash` (#conv_X or #msg:X)
// so deep-links work and the back button moves between threads.

import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { listAgentMessages } from "../../../../components/onboarding/api";
import type { MessageSummary } from "../../../../components/types";
import { ThreadList } from "../../../../components/messages/ThreadList";
import { ThreadDetail } from "../../../../components/messages/ThreadDetail";
import { findThread, groupIntoThreads } from "../../../../components/messages/threading";

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

export default function AgentInboxPage() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const email = searchParams.get("email") ?? "";

  const [rows, setRows] = useState<MessageSummary[] | null>(null);
  const [error, setError] = useState("");
  const [nextToken, setNextToken] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);

  useEffect(() => {
    if (!email) return;
    let cancelled = false;
    listAgentMessages(email, { direction: "all", status: "all", pageSize: 100 })
      .then((res) => {
        if (cancelled) return;
        setRows(res.messages);
        setNextToken(res.next_token ?? null);
      })
      .catch((err) => {
        if (cancelled) return;
        setError(
          err instanceof Error ? err.message : "Failed to load messages",
        );
      });
    return () => {
      cancelled = true;
    };
  }, [email]);

  const threads = useMemo(
    () => (rows ? groupIntoThreads(rows) : []),
    [rows],
  );
  const hash = useUrlHash();
  const selected = findThread(threads, hash);
  const pendingCount = threads.filter((t) => t.state === "pending").length;

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

  const openMessage = (id: string) => {
    router.push(
      `/dashboard/agents/messages/view?email=${encodeURIComponent(email)}&id=${encodeURIComponent(id)}`,
    );
  };
  const openMessageWithHeaders = (id: string) => {
    router.push(
      `/dashboard/agents/messages/view?email=${encodeURIComponent(email)}&id=${encodeURIComponent(id)}&headers=1`,
    );
  };

  const loadOlder = async () => {
    if (!nextToken) return;
    setLoadingMore(true);
    try {
      const res = await listAgentMessages(email, {
        direction: "all",
        status: "all",
        pageSize: 100,
        token: nextToken,
      });
      setRows((prev) => (prev ? [...prev, ...res.messages] : res.messages));
      setNextToken(res.next_token ?? null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load older messages");
    } finally {
      setLoadingMore(false);
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
        hasMore={!!nextToken}
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
        {!error && rows === null && (
          <div
            className="px-7 py-8 text-[13px]"
            style={{ color: "var(--fg-muted)" }}
          >
            Loading inbox…
          </div>
        )}
        {!error && rows !== null && (
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
