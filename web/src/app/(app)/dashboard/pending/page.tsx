"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { listPendingMessages } from "../../../components/onboarding/api";
import type { PendingMessageSummary } from "../../../components/types";
import { PageShell } from "../../../components/loft/PageShell";
import { Chip } from "../../../components/loft/Chip";

function formatExpiresIn(iso?: string): string {
  if (!iso) return "—";
  const expiresAt = new Date(iso).getTime();
  const now = Date.now();
  const diff = expiresAt - now;
  if (diff <= 0) return "expired";
  const minutes = Math.floor(diff / 60_000);
  if (minutes < 60) return `in ${minutes}m`;
  const hours = Math.floor(minutes / 60);
  const mins = minutes % 60;
  if (hours < 24) return mins === 0 ? `in ${hours}h` : `in ${hours}h ${mins}m`;
  const days = Math.floor(hours / 24);
  const h = hours % 24;
  return h === 0 ? `in ${days}d` : `in ${days}d ${h}h`;
}

function joinRecipients(to: string[], cc?: string[]): string {
  const all = [...to, ...(cc ?? [])];
  if (all.length === 0) return "(no recipients)";
  if (all.length === 1) return all[0];
  return `${all[0]} + ${all.length - 1} more`;
}

export default function PendingPage() {
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

  // Topbar pulse — surfaces the count + how many expire within 1h.
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
      "Messages your agents want to send that are waiting on your review."
    );

  return (
    <PageShell
      crumbs={["Pending"]}
      eyebrow="Human-in-the-loop"
      title={<>Pending approval</>}
      subtitle={subtitleLine}
    >
      {error && (
        <div
          className="mb-6 p-3 text-[13px]"
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
          Loading...
        </div>
      ) : messages.length === 0 ? (
        <div
          className="p-8 text-center"
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
        <div className="space-y-2">
          {messages.map((m) => (
            <Link
              key={m.id}
              href={`/dashboard/pending/review?id=${encodeURIComponent(m.id)}`}
              className="block p-4 transition"
              style={{
                background: "var(--bg-panel)",
                border: "1px solid var(--border)",
                borderRadius: "var(--r-lg)",
              }}
            >
              <div className="flex items-start justify-between gap-4">
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 mb-1.5 flex-wrap">
                    <span
                      className="text-[14px] font-semibold truncate"
                      style={{ color: "var(--fg)" }}
                    >
                      {m.subject || "(no subject)"}
                    </span>
                    {m.type && (
                      <Chip tone="info" mono>
                        {m.type}
                      </Chip>
                    )}
                  </div>
                  <p
                    className="text-[12px] font-mono truncate"
                    style={{ color: "var(--fg-muted)" }}
                  >
                    <span style={{ color: "var(--fg)" }}>{m.agent_id}</span>
                    {" → "}
                    {joinRecipients(m.to, m.cc)}
                  </p>
                </div>
                <div
                  className="text-[12px] shrink-0 text-right"
                  style={{ color: "var(--fg-muted)" }}
                >
                  <div style={{ color: "var(--warn-strong)" }}>
                    {formatExpiresIn(m.approval_expires_at)}
                  </div>
                  <div
                    className="text-[11px] mt-0.5 font-mono"
                    style={{ color: "var(--fg-subtle)" }}
                  >
                    {new Date(m.created_at).toLocaleString()}
                  </div>
                </div>
              </div>
            </Link>
          ))}
        </div>
      )}
    </PageShell>
  );
}
