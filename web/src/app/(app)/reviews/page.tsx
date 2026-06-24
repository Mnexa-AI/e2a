"use client";

import { Suspense, useCallback } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import useSWR, { mutate } from "swr";
import { listPendingMessages } from "../../components/onboarding/api";
import {
  invalidateAgents,
  invalidateAllAgentMessages,
  invalidateMessageDetail,
  pendingMessagesKey,
} from "../../../lib/swrKeys";
import type { PendingMessageSummary } from "../../components/types";
import { PageShell } from "../../components/loft/PageShell";
import { PendingRow } from "./_components/PendingRow";

// Pending review — a single-column "outbound holds" inbox. Each row is an
// agent-drafted reply awaiting approval; clicking expands it read-first
// (body + Details) with an Approve / Edit / Reject action bar (accordion:
// one open at a time, tracked via ?id=). This converges the pending
// surface onto the same row language as the agent inbox and folds in the
// old master-detail PendingDetailPanel.

function PendingContent() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const selectedId = searchParams.get("id") ?? "";

  // Shared SWR key with the Sidebar's usePendingCount so the queue and
  // the badge share one fetch + cache entry.
  const {
    data: messages = [],
    error: swrError,
    isLoading,
  } = useSWR<PendingMessageSummary[]>(pendingMessagesKey, () =>
    listPendingMessages(),
  );
  const loading = isLoading && messages.length === 0;
  const error = swrError
    ? swrError instanceof Error
      ? swrError.message
      : "Failed to load pending messages"
    : "";

  // Accordion toggle: open a row (?id=) or collapse it if already open.
  const handleToggle = useCallback(
    (id: string) => {
      if (id === selectedId) {
        router.replace("/reviews", { scroll: false });
      } else {
        router.replace(`/reviews?id=${encodeURIComponent(id)}`, {
          scroll: false,
        });
      }
    },
    [selectedId, router],
  );

  // After approve/reject: refetch the queue, collapse to a clean list,
  // and invalidate the derived caches (sidebar badge, agent cards, the
  // inbox views) so the resolved row drops everywhere — mirroring what
  // the focus page used to do.
  const handleResolved = useCallback(async () => {
    void Promise.all([
      selectedId ? invalidateMessageDetail(selectedId) : Promise.resolve(),
      invalidateAgents(),
      invalidateAllAgentMessages(),
    ]);
    router.replace("/reviews", { scroll: false });
    await mutate(pendingMessagesKey);
  }, [router, selectedId]);

  const selected = messages.find((m) => m.id === selectedId) ?? null;

  return (
    <PageShell
      crumbs={
        selected
          ? ["Pending", (selected.subject || selectedId).slice(0, 28) + "…"]
          : ["Pending"]
      }
      eyebrow="Review · Message holds"
      title={<>Pending review</>}
      subtitle={
        messages.length > 0
          ? `${messages.length} held ${messages.length === 1 ? "message" : "messages"} awaiting review`
          : "Inbound or outbound messages held by a review gate land here. Approve or reject each one."
      }
      maxWidth={900}
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
          data-testid="pending-empty"
          className="p-12 text-center"
          style={{
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-lg)",
          }}
        >
          <p className="text-[14px]" style={{ color: "var(--fg-muted)" }}>
            Nothing waiting for review.
          </p>
          <p className="text-[12px] mt-1" style={{ color: "var(--fg-subtle)" }}>
            Inbound or outbound messages held by an inbox&apos;s review gate
            appear here. Configure holds in an inbox&apos;s Settings →
            Protection.
          </p>
        </div>
      ) : (
        <div
          style={{
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-lg)",
            overflow: "hidden",
          }}
        >
          {messages.map((m) => (
            <PendingRow
              key={m.id}
              summary={m}
              expanded={m.id === selectedId}
              onToggle={() => handleToggle(m.id)}
              onResolved={handleResolved}
            />
          ))}
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
