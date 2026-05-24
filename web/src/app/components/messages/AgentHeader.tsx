"use client";

// Slimmed per-agent header for the dashboard messages view.
// v2 removed the 5-up stats strip (those signals belong on Overview)
// and the Conversations tab (Messages is threaded by default).
//
// Layout: identity row (avatar + name + email chip + Verified/Cloud/HITL
// chips) + mono meta sub-line + tab strip (Overview · Messages ·
// Webhooks · Settings). Only Messages is active in this slice;
// the other tabs render as disabled placeholders with a tooltip.

import Link from "next/link";
import { Chip } from "../loft/Chip";
import { Dot } from "../loft/Dot";
import { Eyebrow } from "../loft/Eyebrow";
import { CounterpartyAvatar } from "./CounterpartyAvatar";
import { formatRelativeAge } from "../../../lib/relativeTime";
import type { DashboardAgent } from "../types";

export type AgentTab = "messages" | "settings";

// The agent-detail surface is intentionally scoped to two tabs:
//   • Messages — the threaded inbox + focus view.
//   • Settings — per-agent editors (mode, webhook URL, HITL config,
//     delete).
// Overview + Webhooks were considered and dropped: Overview duplicated
// the dashboard agent card; Webhooks folded into Settings. When a
// third tab is added, restore the `ready` flag + disabled-tab branch
// (see git history at 63876fc).
const TABS: { key: AgentTab; label: string; slug: string }[] = [
  { key: "messages", label: "Messages", slug: "messages" },
  { key: "settings", label: "Settings", slug: "settings" },
];

export function AgentHeader({
  agent,
  tab,
}: {
  agent: DashboardAgent;
  tab: AgentTab;
}) {
  const isCloud = agent.agent_mode !== "local";
  // Suppress the meta sub-line "last delivery" segment when we have no
  // timestamp — the shared helper returns "—" which would render as
  // "· last delivery —", but the design omits the segment entirely.
  const lastDelivery = agent.last_delivery_at
    ? formatRelativeAge(agent.last_delivery_at)
    : null;
  const emailQs = encodeURIComponent(agent.email);

  return (
    <div
      style={{
        padding: "20px 28px 0",
        background: "var(--bg-panel)",
        borderBottom: "1px solid var(--border)",
      }}
    >
      {/* Identity row */}
      <div className="flex flex-col md:flex-row md:items-start md:justify-between gap-3 mb-3">
        <div className="min-w-0 flex-1">
          <Eyebrow>Agent · {agent.id}</Eyebrow>
          <div className="flex items-center gap-3 mt-2 mb-1.5 flex-wrap">
            <CounterpartyAvatar email={agent.email} name={agent.name} size={28} />
            <h1
              style={{
                fontFamily: "var(--f-ui)",
                fontSize: 22,
                fontWeight: 700,
                letterSpacing: "-0.012em",
                color: "var(--fg)",
                margin: 0,
              }}
            >
              {agent.name || agent.email.split("@")[0]}
            </h1>
            <code
              style={{
                fontFamily: "var(--f-mono)",
                fontSize: 13,
                fontWeight: 500,
                color: "var(--fg)",
                background: "var(--bg-elev)",
                padding: "3px 9px",
                borderRadius: "var(--r-sm)",
                border: "1px solid var(--border-sub)",
              }}
            >
              {agent.email}
            </code>
            {agent.domain_verified ? (
              <Chip tone="success">
                <Dot tone="success" /> Verified
              </Chip>
            ) : (
              <Chip tone="warn">
                <Dot tone="warn" /> Unverified
              </Chip>
            )}
            <Chip tone="neutral" mono>
              {isCloud ? "Cloud" : "Local"}
            </Chip>
            {agent.hitl_enabled && <Chip tone="accent">HITL on</Chip>}
          </div>
          <div
            style={{
              fontFamily: "var(--f-mono)",
              fontSize: 11,
              color: "var(--fg-subtle)",
              letterSpacing: "0.02em",
            }}
          >
            created {new Date(agent.created_at).toLocaleDateString(undefined, {
              month: "short",
              day: "numeric",
            })}
            {agent.webhook_url && (
              <>
                {" · webhook "}
                <span style={{ color: "var(--fg-muted)" }}>{agent.webhook_url}</span>
              </>
            )}
            {lastDelivery && <> · last delivery {lastDelivery}</>}
          </div>
        </div>
      </div>

      {/* Tab strip */}
      <div className="flex items-center gap-1 mt-1">
        {TABS.map((t) => {
          const active = t.key === tab;
          const baseStyle = {
            padding: "10px 14px 12px",
            fontSize: 13,
            fontWeight: active ? 600 : 400,
            color: active ? "var(--fg)" : "var(--fg-muted)",
            borderBottom: active
              ? "2px solid var(--accent)"
              : "2px solid transparent",
            marginBottom: -1,
            textDecoration: "none",
          } as const;
          const href = `/dashboard/agents/${t.slug}?email=${emailQs}`;
          return (
            <Link
              key={t.key}
              href={href}
              aria-current={active ? "page" : undefined}
              style={baseStyle}
            >
              {t.label}
            </Link>
          );
        })}
      </div>
    </div>
  );
}
