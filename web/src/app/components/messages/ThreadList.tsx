"use client";

// Left column of the dashboard inbox — scrollable list of threads with
// a sticky header. The list rows are deliberately compact (5 lines max)
// so the inbox can show ~12 threads in a 920px artboard.

import type { Thread } from "./threading";
import { ThreadRow } from "./ThreadRow";

export type ThreadListProps = {
  threads: Thread[];
  selectedKey: string | null;
  onSelect: (key: string) => void;
  /** Total in the active window — drives the header subtitle. */
  total: number;
  pendingCount: number;
  hasMore?: boolean;
  onLoadMore?: () => void;
  loadingMore?: boolean;
};

export function ThreadList({
  threads,
  selectedKey,
  onSelect,
  total,
  pendingCount,
  hasMore,
  onLoadMore,
  loadingMore,
}: ThreadListProps) {
  return (
    <div
      data-testid="thread-list"
      className="flex flex-col min-h-0 overflow-hidden"
      style={{
        background: "var(--bg-panel)",
        borderRight: "1px solid var(--border)",
      }}
    >
      {/* Sticky list header */}
      <div
        className="flex items-center justify-between"
        style={{
          padding: "11px 16px",
          borderBottom: "1px solid var(--border)",
          background: "var(--bg-elev)",
        }}
      >
        <span style={{ fontSize: 12, fontWeight: 600, color: "var(--fg)" }}>
          Inbox
        </span>
        <span
          style={{
            fontFamily: "var(--f-mono)",
            fontSize: 11,
            color: "var(--fg-subtle)",
          }}
        >
          {total} {total === 1 ? "thread" : "threads"}
          {pendingCount > 0 && ` · ${pendingCount} pending`}
        </span>
      </div>

      {/* Rows */}
      <div className="overflow-y-auto flex-1">
        {threads.length === 0 && (
          <div
            data-testid="thread-list-empty"
            className="px-4 py-8 text-center"
            style={{ color: "var(--fg-muted)", fontSize: 13 }}
          >
            No conversations yet.
          </div>
        )}
        {threads.map((t) => (
          <ThreadRow
            key={t.key}
            thread={t}
            active={t.key === selectedKey}
            onSelect={onSelect}
          />
        ))}
        {hasMore && (
          <button
            type="button"
            onClick={onLoadMore}
            disabled={loadingMore}
            className="w-full text-center"
            style={{
              padding: "14px 16px 20px",
              fontFamily: "var(--f-mono)",
              fontSize: 11,
              color: loadingMore
                ? "var(--fg-subtle)"
                : "var(--accent-strong)",
              background: "transparent",
              border: "none",
              cursor: loadingMore ? "default" : "pointer",
            }}
          >
            {loadingMore ? "loading older…" : "load older →"}
          </button>
        )}
      </div>
    </div>
  );
}
