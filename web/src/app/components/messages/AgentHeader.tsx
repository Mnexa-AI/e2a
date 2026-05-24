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
import type { DashboardAgent } from "../types";

export type AgentTab = "overview" | "messages" | "webhooks" | "settings";

// Each tab's `ready` flag controls whether it's a live link or a
// disabled placeholder. As we ship more screens, flip these.
const TABS: { key: AgentTab; label: string; slug: string; ready: boolean }[] = [
  { key: "overview", label: "Overview", slug: "overview", ready: false },
  { key: "messages", label: "Messages", slug: "messages", ready: true },
  { key: "webhooks", label: "Webhooks", slug: "webhooks", ready: false },
  { key: "settings", label: "Settings", slug: "settings", ready: false },
];

function formatRelativeAge(iso: string | null | undefined): string | null {
  if (!iso) return null;
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 0 || isNaN(diff)) return null;
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return "just now";
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  return `${Math.floor(hr / 24)}d ago`;
}

export function AgentHeader({
  agent,
  tab,
}: {
  agent: DashboardAgent;
  tab: AgentTab;
}) {
  const isCloud = agent.agent_mode !== "local";
  const lastDelivery = formatRelativeAge(agent.last_delivery_at);
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
            color: active
              ? "var(--fg)"
              : t.ready
                ? "var(--fg-muted)"
                : "var(--fg-subtle)",
            borderBottom: active
              ? "2px solid var(--accent)"
              : "2px solid transparent",
            marginBottom: -1,
            textDecoration: "none",
          } as const;
          if (!t.ready) {
            return (
              <span
                key={t.key}
                aria-disabled="true"
                title="Shipping next"
                style={{ ...baseStyle, cursor: "not-allowed", opacity: 0.6 }}
              >
                {t.label}
              </span>
            );
          }
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
