"use client";

import Link from "next/link";
import useSWR from "swr";
import type { DashboardAgent } from "../../../components/types";
import { Chip, Dot } from "@e2a/ui";
import {
  getInboxUnread,
  UNREAD_BADGE_CAP,
} from "../../../components/onboarding/api";
import { agentUnreadKey } from "../../../../lib/swrKeys";

export function AgentCard({
  agent,
}: {
  agent: DashboardAgent;
}) {
  // Option A unread affordance: one lightweight per-card probe against the
  // messages endpoint (read_status=unread). SWR shares/dedupes the call and
  // revalidates on focus, so returning to this tab after reading an inbox
  // updates the count. Failures degrade silently to "no badge".
  const { data: unread } = useSWR(agentUnreadKey(agent.email), () =>
    getInboxUnread(agent.email).catch(() => ({ count: 0, more: false })),
  );
  const unreadCount = unread?.count ?? 0;
  const unreadLabel =
    unread?.more || unreadCount > UNREAD_BADGE_CAP
      ? `${UNREAD_BADGE_CAP}+`
      : String(unreadCount);

  return (
    <div
      style={{
        background: "var(--bg-panel)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-lg)",
        padding: "20px 22px",
      }}
    >
      {/* One compact row: inbox identity on the left, navigation on the
          right (top-aligned with the inbox name). Stacks on narrow
          viewports so the email + chip column isn't squeezed. The "Send a
          test message" action lives inside the inbox view's header, not
          here. */}
      <div className="flex flex-col md:flex-row md:items-start md:justify-between gap-3">
        <div className="min-w-0 flex-1">
          {/* Email + badges. Email is a link to the per-agent Messages view
              (Activity log) so clicking the agent's address from the
              dashboard lands on the debug surface for that agent. */}
          <div className="flex items-center gap-2 mb-2 flex-wrap">
            {unreadCount > 0 && (
              // Red unread count badge. Solid --danger with white text
              // reads in both themes; sr-only text spells it out for
              // screen readers since the number alone lacks context.
              <span
                className="inline-flex items-center justify-center shrink-0 font-mono font-semibold tabular-nums"
                title={`${unreadLabel} unread`}
                style={{
                  minWidth: 18,
                  height: 18,
                  padding: "0 5px",
                  fontSize: 11,
                  lineHeight: 1,
                  color: "#fff",
                  background: "var(--danger)",
                  borderRadius: 999,
                }}
              >
                {unreadLabel}
                <span className="sr-only"> unread messages</span>
              </span>
            )}
            {agent.name && (
              <Link
                href={`/inboxes/messages?email=${encodeURIComponent(agent.email)}`}
                className="text-[14px] font-semibold hover:underline"
                style={{ color: "var(--fg)" }}
              >
                {agent.name}
              </Link>
            )}
            <Link
              href={`/inboxes/messages?email=${encodeURIComponent(agent.email)}`}
              className="hover:underline"
              style={{
                textDecoration: "none",
                display: "inline-block",
              }}
            >
              {/* Keep the email wrapped in a real <code> so screen
                  readers announce it as code, not generic link text. */}
              <code
                className="font-mono text-[13px] px-2 py-0.5 break-all"
                style={{
                  background: "var(--bg-elev)",
                  border: "1px solid var(--border-sub)",
                  borderRadius: "var(--r-sm)",
                  color: "var(--fg)",
                }}
              >
                {agent.email}
              </code>
            </Link>
            <Chip tone={agent.domain_verified ? "success" : "warn"}>
              <Dot tone={agent.domain_verified ? "success" : "warn"} />
              {agent.domain_verified ? "Verified" : "Unverified"}
            </Chip>
          </div>

          {/* Meta info */}
          <p
            className="font-mono text-[11px]"
            style={{
              color: "var(--fg-subtle)",
              letterSpacing: "0.02em",
            }}
          >
            created {new Date(agent.created_at).toLocaleDateString()}
          </p>
        </div>

        {/* Navigation — the two canonical entry points into the per-agent
            surface. Editing (review queue) + delete live on the per-agent
            Settings page. */}
        <div className="flex items-center gap-4 flex-wrap shrink-0 md:justify-end">
          <Link
            href={`/inboxes/messages?email=${encodeURIComponent(agent.email)}`}
            className="inline-flex items-center gap-1 text-[13px] font-medium hover:underline"
            style={{ color: "var(--accent-strong)" }}
          >
            Open inbox <span aria-hidden>→</span>
          </Link>
          <Link
            href={`/inboxes/settings?email=${encodeURIComponent(agent.email)}`}
            className="inline-flex items-center gap-1 text-[13px] hover:underline"
            style={{ color: "var(--fg-muted)" }}
          >
            Settings
          </Link>
        </div>
      </div>
    </div>
  );
}

// Delete moved to /inboxes/settings → Danger zone, and the test
// send moved into the inbox view header, so the agent card is now a pure
// identity + navigation tile.
