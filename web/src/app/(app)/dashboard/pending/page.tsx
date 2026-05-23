"use client";

import { Suspense, useCallback, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { listPendingMessages } from "../../../components/onboarding/api";
import type { PendingMessageSummary } from "../../../components/types";
import { PageShell } from "../../../components/loft/PageShell";
import { Chip } from "../../../components/loft/Chip";
import { PendingDetailPanel } from "./_components/PendingDetailPanel";

// Pending review — split-pane layout matching redesign/pending.jsx.
// Left column (320px): queue of pending messages. Right column: detail
// of the selected message, driven by the ?id= URL param. Clicking a
// queue row updates the URL; the detail panel re-loads on id change.
//
// The detail panel is a separate component so it can update its own
// state (drafts, save in flight) without forcing a queue reflow on
// every keystroke. After approve/reject, the panel calls onChanged()
// which refreshes the queue and auto-advances to the next pending row.

function formatExpiresIn(iso?: string): string {
  if (!iso) return "";
  const ms = new Date(iso).getTime() - Date.now();
  if (ms <= 0) return "expired";
  const min = Math.floor(ms / 60_000);
  if (min < 60) return `in ${min}m`;
  const h = Math.floor(min / 60);
  return h < 24 ? `in ${h}h` : `in ${Math.floor(h / 24)}d`;
}

function formatQueuedAgo(iso: string): string {
  const sec = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

function initialsFor(email: string): string {
  const local = email.split("@")[0] || email;
  return (
    local
      .split(/[.\-_]/)
      .slice(0, 2)
      .map((s) => s.charAt(0).toUpperCase())
      .join("") || "?"
  );
}

function hueFor(email: string): number {
  const local = email.split("@")[0] || email;
  let hash = 0;
  for (let i = 0; i < local.length; i++)
    hash = (hash * 31 + local.charCodeAt(i)) | 0;
  return Math.abs(hash) % 360;
}

function QueueRow({
  msg,
  active,
  onClick,
}: {
  msg: PendingMessageSummary;
  active: boolean;
  onClick: () => void;
}) {
  const expires = formatExpiresIn(msg.approval_expires_at);
  const queued = formatQueuedAgo(msg.created_at);
  const hue = hueFor(msg.agent_id);
  const accent = active;

  return (
    <button
      onClick={onClick}
      className="w-full text-left px-3 py-2.5 transition flex gap-2.5"
      style={{
        background: active ? "var(--bg-elev)" : "transparent",
        borderLeft: active
          ? "2px solid var(--accent-fill)"
          : "2px solid transparent",
        borderBottom: "1px solid var(--border-sub)",
      }}
    >
      <div
        className="flex items-center justify-center font-mono text-[10px] font-semibold shrink-0"
        style={{
          width: 26,
          height: 26,
          borderRadius: "50%",
          background: `hsl(${hue}, 45%, 35%)`,
          color: "#fff",
        }}
      >
        {initialsFor(msg.agent_id)}
      </div>
      <div className="min-w-0 flex-1">
        <div
          className="text-[13px] font-semibold truncate"
          style={{
            color: accent ? "var(--fg)" : "var(--fg)",
          }}
        >
          {msg.subject || "(no subject)"}
        </div>
        <div
          className="font-mono text-[10.5px] truncate"
          style={{ color: "var(--fg-subtle)" }}
        >
          {msg.agent_id} → {(msg.to ?? [])[0] || "—"}
          {msg.to && msg.to.length > 1 && ` +${msg.to.length - 1}`}
        </div>
        <div
          className="font-mono text-[10.5px] flex items-center gap-1.5 mt-1 flex-wrap"
          style={{ color: "var(--fg-subtle)" }}
        >
          {expires && (
            <span
              style={{
                color:
                  expires === "expired"
                    ? "var(--danger-strong)"
                    : expires.startsWith("in 0") || expires.match(/in \d+m$/)
                      ? "var(--warn-strong)"
                      : "var(--fg-subtle)",
              }}
            >
              expires {expires}
            </span>
          )}
          {expires && <span>·</span>}
          <span>{queued}</span>
          {msg.type && (
            <Chip tone="info" mono>
              {msg.type === "reply" ? "reply" : msg.type === "test" ? "test" : "send"}
            </Chip>
          )}
        </div>
      </div>
    </button>
  );
}

function PendingContent() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const selectedId = searchParams.get("id") ?? "";

  const [messages, setMessages] = useState<PendingMessageSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      const data = await listPendingMessages();
      setMessages(data);
      setError("");
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Failed to load pending messages",
      );
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  // Auto-select the first row when the URL has no id and there are
  // messages. Mirrors the mock — the right pane always has content
  // when the queue is non-empty.
  useEffect(() => {
    if (!selectedId && messages.length > 0) {
      router.replace(
        `/dashboard/pending?id=${encodeURIComponent(messages[0].id)}`,
        { scroll: false },
      );
    }
  }, [selectedId, messages, router]);

  const handleSelect = (id: string) => {
    router.push(`/dashboard/pending?id=${encodeURIComponent(id)}`, {
      scroll: false,
    });
  };

  // After approve/reject, refresh the queue. If the selected message
  // is no longer in the list, advance to the next pending row.
  const handleChanged = useCallback(async () => {
    try {
      const fresh = await listPendingMessages();
      setMessages(fresh);
      const stillThere = fresh.some((m) => m.id === selectedId);
      if (!stillThere && fresh.length > 0) {
        router.replace(
          `/dashboard/pending?id=${encodeURIComponent(fresh[0].id)}`,
          { scroll: false },
        );
      } else if (fresh.length === 0) {
        router.replace("/dashboard/pending", { scroll: false });
      }
    } catch {
      // best-effort — leave selectedId stale, the panel will surface
      // a "not pending" state on next click
    }
  }, [router, selectedId]);

  const expiringSoon = messages.filter((m) => {
    if (!m.approval_expires_at) return false;
    const ms = new Date(m.approval_expires_at).getTime() - Date.now();
    return ms > 0 && ms < 60 * 60 * 1000;
  }).length;

  const subtitleLine =
    messages.length > 0 ? (
      <>
        {messages.length} pending
        {expiringSoon > 0 && (
          <>
            {" · "}
            <span style={{ color: "var(--accent-strong)", fontWeight: 500 }}>
              {expiringSoon} expire within 1h
            </span>
          </>
        )}
      </>
    ) : (
      "Outbound messages your agents want to send. Approve as-is, edit, or reject."
    );

  return (
    <PageShell
      crumbs={
        selectedId
          ? ["Pending", selectedId.slice(0, 12) + "…"]
          : ["Pending"]
      }
      eyebrow="Human-in-the-loop · Inbound review"
      title={<>Pending approval</>}
      subtitle={subtitleLine}
      maxWidth={1400}
    >
      {error && (
        <div
          className="mb-4 p-3 text-[13px]"
          style={{
            background: "var(--danger-bg)",
            color: "var(--danger-strong)",
            border: "1px solid var(--danger-bg)",
            borderRadius: "var(--r-md)",
          }}
        >
          {error}
        </div>
      )}

      {loading ? (
        <div
          className="text-[13px] py-12 text-center"
          style={{ color: "var(--fg-muted)" }}
        >
          Loading…
        </div>
      ) : messages.length === 0 ? (
        <div
          className="p-12 text-center"
          style={{
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-lg)",
          }}
        >
          <p className="text-[14px]" style={{ color: "var(--fg-muted)" }}>
            No messages are waiting for approval.
          </p>
          <p
            className="text-[12px] mt-1"
            style={{ color: "var(--fg-subtle)" }}
          >
            Enable HITL on an agent to start reviewing its outbound messages
            here.
          </p>
        </div>
      ) : (
        <div
          className="grid grid-cols-1 md:grid-cols-[320px_minmax(0,1fr)] md:[height:calc(100vh-var(--chrome-h)-200px)]"
          style={{
            minHeight: 520,
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-lg)",
            overflow: "hidden",
          }}
        >
          {/* Queue. Border becomes a bottom divider on narrow viewports
              where the queue stacks above the detail pane. */}
          <div
            className="flex flex-col min-h-0 border-b md:border-b-0 md:border-r"
            style={{ borderColor: "var(--border)" }}
          >
            <div
              className="px-3 py-2.5 flex items-center justify-between"
              style={{
                background: "var(--bg-elev)",
                borderBottom: "1px solid var(--border)",
              }}
            >
              <span
                className="text-[12px] font-semibold"
                style={{ color: "var(--fg)" }}
              >
                Queue
              </span>
              <span
                className="font-mono text-[11px]"
                style={{ color: "var(--fg-subtle)" }}
              >
                {messages.length}
              </span>
            </div>
            <div className="overflow-y-auto flex-1">
              {messages.map((m) => (
                <QueueRow
                  key={m.id}
                  msg={m}
                  active={m.id === selectedId}
                  onClick={() => handleSelect(m.id)}
                />
              ))}
            </div>
          </div>

          {/* Detail */}
          <div className="min-h-0 overflow-hidden">
            {selectedId ? (
              <PendingDetailPanel
                key={selectedId}
                messageId={selectedId}
                onChanged={handleChanged}
              />
            ) : (
              <div
                className="text-[13px] py-12 text-center"
                style={{ color: "var(--fg-muted)" }}
              >
                Select a message from the queue.
              </div>
            )}
          </div>
        </div>
      )}
    </PageShell>
  );
}

export default function PendingPage() {
  return (
    <Suspense
      fallback={
        <PageShell crumbs={["Pending"]}>
          <div
            className="text-[13px] py-12 text-center"
            style={{ color: "var(--fg-muted)" }}
          >
            Loading…
          </div>
        </PageShell>
      }
    >
      <PendingContent />
    </Suspense>
  );
}
