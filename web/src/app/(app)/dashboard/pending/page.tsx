"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { listPendingMessages } from "../../../components/onboarding/api";
import type { PendingMessageSummary } from "../../../components/types";

// formatExpiresIn produces a human-friendly "in 2h 15m" or "expired"
// string from an ISO timestamp. Matches the UX goal from the design
// doc: reviewers should see expiry at a glance, not do the math.
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
      setError(err instanceof Error ? err.message : "Failed to load pending messages");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  return (
    <>
      <div className="mb-8">
        <h2 className="text-2xl font-bold tracking-tight mb-2">Pending approval</h2>
        <p className="text-muted">
          Messages your agents want to send that are waiting on your review.
        </p>
      </div>

      {error && (
        <div className="mb-6 p-3 bg-red-50 border border-red-200 rounded-lg text-sm text-red-700">
          {error}
        </div>
      )}

      {loading ? (
        <div className="text-sm text-muted py-12 text-center">Loading...</div>
      ) : messages.length === 0 ? (
        <div className="border border-border rounded-lg p-8 text-center">
          <p className="text-sm text-muted">No messages are waiting for approval.</p>
          <p className="text-xs text-muted mt-1">
            Enable HITL on an agent to start reviewing its outbound messages here.
          </p>
        </div>
      ) : (
        <div className="space-y-2">
          {messages.map((m) => (
            <Link
              key={m.id}
              href={`/dashboard/pending/review?id=${encodeURIComponent(m.id)}`}
              className="block border border-border rounded-lg p-4 hover:bg-surface transition"
            >
              <div className="flex items-start justify-between gap-4">
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 mb-1">
                    <span className="text-sm font-medium truncate">
                      {m.subject || "(no subject)"}
                    </span>
                    {m.type && (
                      <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-indigo-100 text-indigo-700">
                        {m.type}
                      </span>
                    )}
                  </div>
                  <p className="text-xs text-muted truncate">
                    <span className="text-foreground">{m.agent_id}</span>
                    {" → "}
                    {joinRecipients(m.to, m.cc)}
                  </p>
                </div>
                <div className="text-xs text-muted shrink-0 text-right">
                  <div>{formatExpiresIn(m.approval_expires_at)}</div>
                  <div className="text-[11px] mt-0.5">
                    {new Date(m.created_at).toLocaleString()}
                  </div>
                </div>
              </div>
            </Link>
          ))}
        </div>
      )}
    </>
  );
}
