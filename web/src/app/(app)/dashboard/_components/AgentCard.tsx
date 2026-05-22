"use client";

import { useState } from "react";
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
          <button
            onClick={onDelete}
            className="text-[12px] px-3 py-1.5 transition"
            style={{
              color: "var(--danger-strong)",
              border: "1px solid var(--danger-bg)",
              background: "transparent",
              borderRadius: "var(--r-md)",
            }}
          >
            Delete
          </button>
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

      {/* Activity (subordinate) */}
      <div className="mt-3">
        <ActivityPanel email={agent.email} />
      </div>
    </div>
  );
}
