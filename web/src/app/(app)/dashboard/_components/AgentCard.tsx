"use client";

import { useEffect, useRef, useState } from "react";
import type { DashboardAgent } from "../../../components/types";
import { AgentModeSwitcher } from "./AgentModeSwitcher";
import { WebhookEditor } from "./WebhookEditor";
import { HITLEditor } from "./HITLEditor";
import { ConnectInstructions } from "./ConnectInstructions";
import { ActivityPanel } from "./ActivityPanel";
import { Chip } from "../../../components/loft/Chip";
import { Dot } from "../../../components/loft/Dot";
import { AGENTS_DOMAIN } from "../../../../lib/site";

function isSharedDomain(email: string): boolean {
  return AGENTS_DOMAIN !== "" && email.endsWith("@" + AGENTS_DOMAIN);
}

export function AgentCard({
  agent,
  onDelete,
  onUpdate,
}: {
  agent: DashboardAgent;
  onDelete: () => void;
  onUpdate: () => void;
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
      {/* Header row */}
      <div className="flex items-start justify-between">
        <div className="min-w-0 flex-1">
          {/* Email + badges */}
          <div className="flex items-center gap-2 mb-2 flex-wrap">
            {agent.name && (
              <span
                className="text-[14px] font-semibold"
                style={{ color: "var(--fg)" }}
              >
                {agent.name}
              </span>
            )}
            <code
              className="font-mono text-[13px] px-2 py-0.5"
              style={{
                background: "var(--bg-elev)",
                border: "1px solid var(--border-sub)",
                borderRadius: "var(--r-sm)",
                color: "var(--fg)",
              }}
            >
              {agent.email}
            </code>
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
            agent_{agent.id} · created{" "}
            {new Date(agent.created_at).toLocaleDateString()}
          </p>

          {/* Mode switcher */}
          <div className="mt-2">
            <AgentModeSwitcher
              email={agent.email}
              currentMode={agent.agent_mode}
              onSwitched={onUpdate}
            />
          </div>
        </div>

        {/* Actions */}
        <div className="flex gap-2 shrink-0 ml-4">
          {agent.domain_verified && (
            <>
              <button
                onClick={async () => {
                  setTestError("");
                  setTestState("sending");
                  try {
                    const res = await fetch(`/api/v1/agents/${encodeURIComponent(agent.email)}/test`, {
                      method: "POST",
                      credentials: "include",
                    });
                    if (res.ok) {
                      setTestState("sent");
                      setTimeout(() => setTestState("idle"), 3000);
                    } else {
                      const msg = await res.text();
                      setTestError(msg || `Failed (${res.status})`);
                      setTestState("idle");
                    }
                  } catch {
                    setTestError("Network error");
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
          <OverflowMenu onDelete={onDelete} />
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

      {/* Cloud: webhook editor */}
      {isCloud && (
        <div className="mt-3">
          <WebhookEditor
            email={agent.email}
            currentUrl={agent.webhook_url}
            onUpdated={onUpdate}
          />
        </div>
      )}

      {/* HITL approval settings — visible for every agent, any mode */}
      {agent.domain_verified && (
        <div className="mt-3">
          <HITLEditor
            email={agent.email}
            enabled={agent.hitl_enabled}
            ttlSeconds={agent.hitl_ttl_seconds}
            expirationAction={agent.hitl_expiration_action}
            onUpdated={onUpdate}
          />
        </div>
      )}

      {/* Connect instructions */}
      {showConnect && (
        <div className="mt-3 border-t border-border pt-4">
          <ConnectInstructions mode={isLocal ? "local" : "cloud"} />
        </div>
      )}

      {/* Per-agent stats footer (BACKEND_TODO #2). Cloud-mode agents also
          get a webhook-reachable indicator on the right; local-mode hides
          it because no webhook is involved. */}
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

      {/* Activity (subordinate) */}
      <div className="mt-3">
        <ActivityPanel email={agent.email} />
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

// OverflowMenu collapses destructive actions (currently just Delete)
// behind a kebab button to match the mock's Test / Connect / ⋯
// action triad. Closes on Escape, click-outside, or option click.
//
// Today the menu only has Delete. Future additions (e.g. Rotate
// webhook URL, Export activity CSV) slot in here without expanding
// the visible button row.
function OverflowMenu({ onDelete }: { onDelete: () => void }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <div className="relative" ref={ref}>
      <button
        aria-label="More actions"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="text-[16px] leading-none px-2.5 py-1.5 transition"
        style={{
          background: open ? "var(--bg-elev)" : "transparent",
          color: "var(--fg-muted)",
          border: "1px solid var(--border)",
          borderRadius: "var(--r-md)",
          minHeight: 32,
        }}
      >
        ⋯
      </button>
      {open && (
        <div
          role="menu"
          className="absolute right-0 mt-1 z-10 min-w-[140px] py-1"
          style={{
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-md)",
            boxShadow: "0 4px 18px rgba(0,0,0,0.08)",
          }}
        >
          <button
            role="menuitem"
            onClick={() => {
              setOpen(false);
              onDelete();
            }}
            className="block w-full text-left px-3 py-2 text-[12px] transition"
            style={{ color: "var(--danger-strong)" }}
          >
            Delete agent
          </button>
        </div>
      )}
    </div>
  );
}
