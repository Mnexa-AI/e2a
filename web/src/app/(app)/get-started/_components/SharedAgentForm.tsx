"use client";

import { useState } from "react";
import { Field } from "../../../components/Field";
import { createAgent } from "../../../components/onboarding/api";
import { isValidSlug, isValidWebhookUrl } from "../../../components/onboarding/state";
import { track } from "../../../components/onboarding/analytics";
import type { AgentMode } from "../../../components/onboarding/types";
import type { AgentData } from "../../../components/types";
import { AGENTS_DOMAIN_DISPLAY } from "../../../../lib/site";

export function SharedAgentForm({
  onCreated,
  onBack,
}: {
  onCreated: (agent: AgentData, mode: AgentMode, webhookUrl: string) => void;
  /** Returns the user to the address-type chooser. Wired by the parent
   * to router.back() so the browser history stays consistent. */
  onBack?: () => void;
}) {
  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  const [agentMode, setAgentMode] = useState<AgentMode>("local");
  const [webhookUrl, setWebhookUrl] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const isCloud = agentMode === "cloud";

  const canSubmit =
    !loading &&
    slug.length > 0 &&
    (!isCloud || webhookUrl.length > 0);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");

    if (!isValidSlug(slug)) {
      setError("Slug must be 2\u201340 lowercase characters (letters, numbers, hyphens). No leading/trailing hyphens.");
      return;
    }

    if (isCloud && !isValidWebhookUrl(webhookUrl)) {
      setError("Webhook URL must be a valid HTTPS URL.");
      return;
    }

    setLoading(true);
    track("agent_creation_started", { shared_or_custom: "shared", agent_mode: agentMode });
    try {
      const result = await createAgent({
        slug,
        ...(name ? { name } : {}),
        agent_mode: agentMode,
        ...(isCloud ? { webhook_url: webhookUrl } : {}),
      });
      track("agent_creation_succeeded", { shared_or_custom: "shared", agent_mode: agentMode });
      onCreated(result as AgentData, agentMode, isCloud ? webhookUrl : "");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Registration failed");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div>
      {onBack && (
        <button
          type="button"
          onClick={onBack}
          className="inline-flex items-center gap-1.5 mb-4 text-[12px] transition hover:opacity-80"
          style={{ color: "var(--fg-muted)" }}
        >
          <span aria-hidden>←</span>
          Back
        </button>
      )}
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
        Choose a slug and how your agent receives email.
      </p>

      {/* How it works */}
      <div className="mb-8 p-4 bg-blue-50/50 border border-accent/20 rounded-lg text-sm text-muted space-y-3">
        <p className="font-medium text-foreground mb-1">How it works</p>
        <p>
          Your agent gets{" "}
          <code className="text-xs bg-white/60 px-1 py-0.5 rounded">
            {slug || "your-slug"}@{AGENTS_DOMAIN_DISPLAY}
          </code>
          . {isCloud
            ? "Inbound emails are delivered to your HTTPS webhook."
            : "Emails arrive in real time via CLI, SDK, or WebSocket."}
        </p>
      </div>

      {error && (
        <div className="mb-6 p-3 bg-red-50 border border-red-200 rounded-lg text-sm text-red-700">
          {error}
        </div>
      )}

      <form onSubmit={handleSubmit} className="space-y-5">
        {/* Slug */}
        <Field
          label="Slug"
          placeholder="my-agent"
          value={slug}
          onChange={(v) => setSlug(v.toLowerCase())}
          hint={`Your agent\u2019s email will be ${slug || "slug"}@${AGENTS_DOMAIN_DISPLAY}`}
        />

        {/* Display name */}
        <Field
          label="Display name"
          placeholder="My Agent"
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
