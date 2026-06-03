"use client";

import { useState } from "react";
import Link from "next/link";
import type { DashboardAgent } from "../../../components/types";
import { ConnectInstructions } from "./ConnectInstructions";
import { Chip } from "../../../components/loft/Chip";
import { Dot } from "../../../components/loft/Dot";
import { sendAgentTestEmail } from "../../../components/onboarding/api";
import { AGENTS_DOMAIN } from "../../../../lib/site";

function isSharedDomain(email: string): boolean {
  return AGENTS_DOMAIN !== "" && email.endsWith("@" + AGENTS_DOMAIN);
}

export function AgentCard({
  agent,
}: {
  agent: DashboardAgent;
}) {
  const [showConnect, setShowConnect] = useState(false);
  const [testState, setTestState] = useState<"idle" | "sending" | "sent">("idle");
  const [testError, setTestError] = useState("");

  const isLocal = agent.agent_mode === "local";
  const isCloud = agent.agent_mode !== "local";
  const shared = isSharedDomain(agent.email);

  return (
    <div
      style={{
        background: "var(--bg-panel)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-lg)",
        padding: "20px 22px",
      }}
    >
      {/* Header row. Stacks on narrow viewports so the email + chip column
          doesn't get squeezed by the action buttons. */}
      <div className="flex flex-col md:flex-row md:items-start md:justify-between gap-3">
        <div className="min-w-0 flex-1">
          {/* Email + badges. Email is a link to the per-agent Messages view
              (Activity log) so clicking the agent's address from the
              dashboard lands on the debug surface for that agent. */}
          <div className="flex items-center gap-2 mb-2 flex-wrap">
            {agent.name && (
              <Link
                href={`/dashboard/agents/messages?email=${encodeURIComponent(agent.email)}`}
                className="text-[14px] font-semibold hover:underline"
                style={{ color: "var(--fg)" }}
              >
                {agent.name}
              </Link>
            )}
            <Link
              href={`/dashboard/agents/messages?email=${encodeURIComponent(agent.email)}`}
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
            <Chip tone={shared ? "info" : "accent"}>
              {shared ? "Shared" : "Custom"}
            </Chip>
            <Chip tone="neutral" mono>
              {isLocal ? "Local" : "Cloud"}
            </Chip>
            {agent.hitl_enabled && <Chip tone="accent">HITL on</Chip>}
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

        {/* Actions: Test + Connect. Edits (mode, webhook URL, HITL,
            delete) live on the per-agent Settings page; the bottom
            CTA bar wires that destination. */}
        <div className="flex gap-2 shrink-0 md:ml-4 flex-wrap">
          {agent.domain_verified && (
            <>
              <button
                onClick={async () => {
                  setTestError("");
                  setTestState("sending");
                  try {
                    await sendAgentTestEmail(agent.email);
                    setTestState("sent");
                    setTimeout(() => setTestState("idle"), 3000);
                  } catch (err) {
                    setTestError(
                      err instanceof Error ? err.message : "Network error",
                    );
                    setTestState("idle");
                  }
                }}
                disabled={testState === "sending"}
                className="text-[12px] px-3 py-1.5 transition disabled:cursor-not-allowed"
                style={{
                  background:
                    testState === "sent"
                      ? "var(--success)"
                      : testState === "sending"
                        ? "var(--bg-elev)"
                        : "var(--bg-panel)",
                  color:
                    testState === "sent"
                      ? "#fff"
                      : testState === "sending"
                        ? "var(--fg-muted)"
                        : "var(--fg)",
                  border: "1px solid var(--border)",
                  borderRadius: "var(--r-md)",
                }}
              >
                {testState === "sent"
                  ? "Sent ✓"
                  : testState === "sending"
                    ? "Sending…"
                    : "Test"}
              </button>
              <button
                onClick={() => setShowConnect(!showConnect)}
                className="text-[12px] px-3 py-1.5 transition"
                style={{
                  background: "var(--fg)",
                  color: "var(--bg)",
                  borderRadius: "var(--r-md)",
                }}
              >
                {showConnect ? "Hide" : "Connect"}
              </button>
            </>
          )}
        </div>
        {testError && (
          <p
            className="text-[12px] mt-1 text-right"
            style={{ color: "var(--danger-strong)" }}
          >
            {testError}
          </p>
        )}
      </div>

      {/* Connect instructions — inline on the card because they're a
          first-mile onboarding affordance, not ongoing config. */}
      {showConnect && (
        <div className="mt-3 border-t border-border pt-4">
          <ConnectInstructions mode={isLocal ? "local" : "cloud"} />
        </div>
      )}

      {/* Per-agent stats footer. Cloud-mode agents also get a
          webhook-reachable indicator on the right; local-mode hides it
          because no webhook is involved. */}
      <div
        className="mt-4 pt-4 grid grid-cols-2 md:grid-cols-4 gap-4"
        style={{ borderTop: "1px solid var(--border-sub)" }}
      >
        <AgentStat label="Inbound · 7d" value={agent.inbound_7d} />
        <AgentStat label="Outbound · 7d" value={agent.outbound_7d} />
        <AgentStat label="Pending" value={agent.pending_count} />
        <AgentStat
          label={isCloud ? "Webhook" : "Last delivery"}
          value={
            isCloud
              ? agent.webhook_healthy === undefined
                ? "—"
                : agent.webhook_healthy
                  ? "reachable"
                  : "unreachable"
              : agent.last_delivery_at
                ? formatRelativeAge(agent.last_delivery_at)
                : "—"
          }
          tone={
            isCloud
              ? agent.webhook_healthy === false
                ? "danger"
                : "muted"
              : "muted"
          }
        />
      </div>

      {/* Bottom CTA bar — the two canonical entry points into the
          per-agent surface. Name + email chip also link to "Open inbox →"
          so there are multiple discoverable paths to the same place. */}
      <div
        className="mt-3 pt-3 flex items-center gap-4 flex-wrap"
        style={{ borderTop: "1px solid var(--border-sub)" }}
      >
        <Link
          href={`/dashboard/agents/messages?email=${encodeURIComponent(agent.email)}`}
          className="inline-flex items-center gap-1 text-[13px] font-medium hover:underline"
          style={{ color: "var(--accent-strong)" }}
        >
          Open inbox <span aria-hidden>→</span>
        </Link>
        <Link
          href={`/dashboard/agents/settings?email=${encodeURIComponent(agent.email)}`}
          className="inline-flex items-center gap-1 text-[13px] hover:underline"
          style={{ color: "var(--fg-muted)" }}
        >
          Settings
        </Link>
      </div>
    </div>
  );
}

// formatRelativeAge converts an ISO timestamp into a compact relative
// label for the per-agent stats row. Newer than 60s → "just now",
// otherwise the smallest unit that fits.
function formatRelativeAge(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 0 || isNaN(diff)) return "—";
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return "just now";
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  return `${Math.floor(hr / 24)}d ago`;
}

function AgentStat({
  label,
  value,
  tone = "muted",
}: {
  label: string;
  value: number | string | undefined | null;
  tone?: "muted" | "danger";
}) {
  const display =
    value === undefined || value === null
      ? "—"
      : typeof value === "number"
        ? String(value)
        : value;
  return (
    <div>
      <div
        className="font-mono text-[10px] font-semibold uppercase mb-1"
        style={{ color: "var(--fg-subtle)", letterSpacing: "0.08em" }}
      >
        {label}
      </div>
      <div
        className="text-[16px] font-medium"
        style={{
          color: tone === "danger" ? "var(--danger-strong)" : "var(--fg)",
        }}
      >
        {display}
      </div>
    </div>
  );
}

// Delete moved to /dashboard/agents/settings → Danger zone, so the
// agent card no longer needs an overflow menu. Future per-agent
// quick-actions (e.g. Export activity CSV) would slot back in here
// or, more likely, into Settings.
