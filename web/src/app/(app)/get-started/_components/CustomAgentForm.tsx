"use client";

import { useState } from "react";
import { Field } from "../../../components/Field";
import { createAgent } from "../../../components/onboarding/api";
import { isValidLocalPart, isValidWebhookUrl } from "../../../components/onboarding/state";
import { track } from "../../../components/onboarding/analytics";
import type { AgentMode } from "../../../components/onboarding/types";
import type { AgentData } from "../../../components/types";
import { invalidateAgents } from "../../../../lib/swrKeys";

export function CustomAgentForm({
  domain,
  onCreated,
}: {
  domain: string;
  onCreated: (agent: AgentData, mode: AgentMode, webhookUrl: string) => void;
}) {
  const [localPart, setLocalPart] = useState("");
  const [name, setName] = useState("");
  const [agentMode, setAgentMode] = useState<AgentMode>("local");
  const [webhookUrl, setWebhookUrl] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const isCloud = agentMode === "cloud";
  const email = localPart ? `${localPart}@${domain}` : "";

  const canSubmit =
    !loading &&
    localPart.length > 0 &&
    (!isCloud || webhookUrl.length > 0);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");

    if (!isValidLocalPart(localPart)) {
      setError("Local part must be lowercase letters, numbers, dots, or hyphens.");
      return;
    }

    if (isCloud && !isValidWebhookUrl(webhookUrl)) {
      setError("Webhook URL must be a valid HTTPS URL.");
      return;
    }

    setLoading(true);
    track("agent_creation_started", { shared_or_custom: "custom", agent_mode: agentMode, domain });
    try {
      const result = await createAgent({
        email,
        ...(name ? { name } : {}),
        agent_mode: agentMode,
        ...(isCloud ? { webhook_url: webhookUrl } : {}),
      });
      track("agent_creation_succeeded", { shared_or_custom: "custom", agent_mode: agentMode, domain });
      // Refresh the SWR `agents` cache so /dashboard shows the new
      // row immediately (otherwise keepPreviousData renders the
      // pre-create list until the next focus revalidation).
      await invalidateAgents();
      onCreated(result as AgentData, agentMode, isCloud ? webhookUrl : "");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Agent creation failed");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div>
      <h2
        className="mb-2"
        style={{
          fontFamily: "var(--f-ui)",
          fontWeight: 600,
          fontSize: 30,
          letterSpacing: "-0.01em",
          color: "var(--fg)",
        }}
      >
        Create your agent
      </h2>
      <p className="mb-7 text-[14px]" style={{ color: "var(--fg-muted)" }}>
        Your domain{" "}
        <code
          className="font-mono text-[12px] px-1.5 py-0.5"
          style={{
            background: "var(--bg-elev)",
            border: "1px solid var(--border-sub)",
            borderRadius: "var(--r-sm)",
            color: "var(--fg)",
          }}
        >
          {domain}
        </code>{" "}
        is verified. Create an agent on it.
      </p>

      {error && (
        <div className="mb-6 p-3 bg-red-50 border border-red-200 rounded-lg text-sm text-red-700">
          {error}
        </div>
      )}

      <form onSubmit={handleSubmit} className="space-y-5">
        {/* Email local part */}
        <div>
          <label className="block text-sm font-medium mb-1.5">Agent email</label>
          <div className="flex items-center gap-0">
            <input
              type="text"
              placeholder="support"
              value={localPart}
              onChange={(e) => setLocalPart(e.target.value.toLowerCase())}
              className="flex-1 px-3 py-2 rounded-l-lg border border-r-0 border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-accent/30"
            />
            <span className="px-3 py-2 rounded-r-lg border border-border bg-surface text-sm text-muted">
              @{domain}
            </span>
          </div>
          <p className="mt-1 text-xs text-muted">The local part for your agent&apos;s email address.</p>
        </div>

        {/* Display name */}
        <Field
          label="Display name"
          placeholder="Support Agent"
          value={name}
          onChange={setName}
          hint="Shown in the From header of outbound emails (optional)"
        />

        {/* Mode selector */}
        <div>
          <label className="block text-sm font-medium mb-2">Delivery mode</label>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => setAgentMode("local")}
              className={`flex-1 px-4 py-2.5 text-sm rounded-lg border transition ${
                agentMode === "local"
                  ? "border-accent bg-accent/5 text-accent font-medium"
                  : "border-border text-muted hover:text-foreground hover:border-foreground/20"
              }`}
            >
              <span className="font-medium">Local agent</span>
              <span className="block text-xs mt-0.5 opacity-80">
                CLI, SDK, or WebSocket
              </span>
            </button>
            <button
              type="button"
              onClick={() => setAgentMode("cloud")}
              className={`flex-1 px-4 py-2.5 text-sm rounded-lg border transition ${
                agentMode === "cloud"
                  ? "border-accent bg-accent/5 text-accent font-medium"
                  : "border-border text-muted hover:text-foreground hover:border-foreground/20"
              }`}
            >
              <span className="font-medium">Cloud agent</span>
              <span className="block text-xs mt-0.5 opacity-80">
                HTTPS webhook
              </span>
            </button>
          </div>
        </div>

        {/* Webhook URL — only for cloud */}
        {isCloud && (
          <Field
            label="Webhook URL"
            placeholder="https://your-agent.com/webhook"
            type="url"
            value={webhookUrl}
            onChange={setWebhookUrl}
            hint="HTTPS endpoint where e2a delivers inbound emails via POST"
          />
        )}

        <button
          type="submit"
          disabled={!canSubmit}
          className="w-full bg-foreground text-background py-3 rounded-lg text-sm font-medium hover:opacity-90 transition disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {loading ? "Creating..." : "Create agent"}
        </button>
      </form>
    </div>
  );
}
