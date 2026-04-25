"use client";

import { useState } from "react";
import type { DashboardAgent } from "../../../components/types";
import { AgentModeSwitcher } from "./AgentModeSwitcher";
import { WebhookEditor } from "./WebhookEditor";
import { HITLEditor } from "./HITLEditor";
import { ConnectInstructions } from "./ConnectInstructions";
import { ActivityPanel } from "./ActivityPanel";

const SHARED_DOMAIN = "agents.e2a.dev";

function isSharedDomain(email: string): boolean {
  return email.endsWith("@" + SHARED_DOMAIN);
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
    <div className="border border-border rounded-lg p-4">
      {/* Header row */}
      <div className="flex items-start justify-between">
        <div className="min-w-0 flex-1">
          {/* Email + badges */}
          <div className="flex items-center gap-2 mb-1 flex-wrap">
            {agent.name && (
              <span className="text-sm font-medium">{agent.name}</span>
            )}
            <code className="text-sm font-mono font-medium">{agent.email}</code>
            <span
              className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${
                agent.domain_verified
                  ? "bg-green-100 text-green-700"
                  : "bg-amber-100 text-amber-700"
              }`}
            >
              {agent.domain_verified ? "Verified" : "Unverified"}
            </span>
            <span
              className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${
                shared ? "bg-blue-100 text-blue-700" : "bg-purple-100 text-purple-700"
              }`}
            >
              {shared ? "Shared" : "Custom"}
            </span>
            <span
              className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${
                isLocal ? "bg-gray-100 text-gray-700" : "bg-indigo-100 text-indigo-700"
              }`}
            >
              {isLocal ? "Local" : "Cloud"}
            </span>
          </div>

          {/* Meta info */}
          <p className="text-xs text-muted">
            Created {new Date(agent.created_at).toLocaleDateString()}
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
                className={`text-xs px-3 py-1.5 rounded-md transition ${
                  testState === "sent"
                    ? "bg-green-600 text-white"
                    : testState === "sending"
                      ? "bg-surface text-muted border border-border cursor-not-allowed"
                      : "border border-border hover:bg-surface"
                }`}
              >
                {testState === "sent" ? "Sent ✓" : testState === "sending" ? "Sending…" : "Test"}
              </button>
              <button
                onClick={() => setShowConnect(!showConnect)}
                className="text-xs px-3 py-1.5 bg-foreground text-background rounded-md hover:opacity-90 transition"
              >
                {showConnect ? "Hide" : "Connect"}
              </button>
            </>
          )}
          <button
            onClick={onDelete}
            className="text-xs px-3 py-1.5 text-red-600 border border-red-200 rounded-md hover:bg-red-50 transition"
          >
            Delete
          </button>
        </div>
        {testError && (
          <p className="text-xs text-red-600 mt-1 text-right">{testError}</p>
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
