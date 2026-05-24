"use client";

// Per-agent Settings — composes the mode/webhook/HITL editors that
// used to live inline on the dashboard agent card, plus a danger
// zone for deletion. The dashboard card is now lean: identity +
// stats + Open-inbox / Settings CTAs only; the editors live here.

import { useEffect, useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Eyebrow } from "../../../../components/loft/Eyebrow";
import {
  deleteAgent,
  listAgents,
} from "../../../../components/onboarding/api";
import type { DashboardAgent } from "../../../../components/types";
import { AgentModeSwitcher } from "../../_components/AgentModeSwitcher";
import { WebhookEditor } from "../../_components/WebhookEditor";
import { HITLEditor } from "../../_components/HITLEditor";

export default function AgentSettingsPage() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const email = searchParams.get("email") ?? "";

  // refreshKey is incremented after each successful editor save so the
  // agent re-fetches and the children remount with fresh props.
  const [refreshKey, setRefreshKey] = useState(0);
  const [agent, setAgent] = useState<DashboardAgent | null>(null);
  const [fetchError, setFetchError] = useState("");
  const [loading, setLoading] = useState(true);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState("");

  const error = email ? fetchError : "Missing ?email= query parameter";

  useEffect(() => {
    if (!email) return;
    let cancelled = false;
    listAgents()
      .then((agents) => {
        if (cancelled) return;
        const match = agents.find((a) => a.email === email);
        if (!match) setFetchError(`Agent ${email} not found`);
        else setAgent(match);
      })
      .catch((err) => {
        if (cancelled) return;
        setFetchError(err instanceof Error ? err.message : "Failed to load agent");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [email, refreshKey]);

  const onEditorSaved = useMemo(
    () => () => setRefreshKey((k) => k + 1),
    [],
  );

  const onDelete = async () => {
    if (!agent) return;
    if (!confirm(`Delete agent ${agent.email}? This cannot be undone.`)) return;
    setDeleting(true);
    setDeleteError("");
    try {
      await deleteAgent(agent.email);
      router.push("/dashboard");
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : "Failed to delete agent");
      setDeleting(false);
    }
  };

  if (error) {
    return (
      <div
        className="m-6 p-4 text-[13px]"
        style={{
          background: "var(--danger-bg)",
          border: "1px solid var(--danger-bg)",
          color: "var(--danger-strong)",
          borderRadius: "var(--r-md)",
        }}
      >
        {error}
      </div>
    );
  }
  if (loading || !agent) {
    return (
      <div className="px-7 py-8 text-[13px]" style={{ color: "var(--fg-muted)" }}>
        Loading settings…
      </div>
    );
  }

  const isCloud = agent.agent_mode !== "local";

  return (
    <div
      data-testid="agent-settings"
      className="mx-auto"
      style={{
        padding: "24px 28px 32px",
        maxWidth: 840,
        width: "100%",
      }}
    >
      {/* Mode */}
      <Section title="Delivery mode" subtitle="Where the agent receives mail — directly via webhook (cloud) or by polling / WebSocket (local).">
        <AgentModeSwitcher
          email={agent.email}
          currentMode={agent.agent_mode}
          onSwitched={onEditorSaved}
        />
      </Section>

      {/* Webhook URL — cloud-only */}
      {isCloud && (
        <Section
          title="Webhook"
          subtitle="The HTTPS endpoint we POST inbound messages to. Updates take effect on the next delivery."
        >
          <WebhookEditor
            email={agent.email}
            currentUrl={agent.webhook_url}
            onUpdated={onEditorSaved}
          />
        </Section>
      )}

      {/* HITL — only when the domain is verified, matches the prior
          AgentCard gating (the approve / reject pipeline needs a real
          domain to deliver notifications). */}
      {agent.domain_verified && (
        <Section
          title="Human-in-the-loop approvals"
          subtitle="Hold outbound messages for review. While HITL is on, every send waits for an Approve or Reject before delivery — or auto-resolves at the TTL."
        >
          <HITLEditor
            email={agent.email}
            enabled={agent.hitl_enabled}
            ttlSeconds={agent.hitl_ttl_seconds}
            expirationAction={agent.hitl_expiration_action}
            onUpdated={onEditorSaved}
          />
        </Section>
      )}

      {/* Danger zone */}
      <section
        data-testid="danger-zone"
        className="mt-8"
        style={{
          background: "var(--bg-panel)",
          border: "1px solid var(--danger-bg)",
          borderRadius: "var(--r-lg)",
          padding: "20px 22px",
        }}
      >
        <Eyebrow>Danger zone</Eyebrow>
        <h2
          className="mt-2 mb-1.5"
          style={{
            fontFamily: "var(--f-ui)",
            fontSize: 15,
            fontWeight: 600,
            color: "var(--fg)",
          }}
        >
          Delete this agent
        </h2>
        <p
          className="mb-4"
          style={{ fontSize: 13, color: "var(--fg-muted)", lineHeight: 1.6 }}
        >
          Removes the agent and all of its messages older than the
          30-day retention window. Pending HITL drafts are auto-rejected.
          The email address becomes available for re-registration. This
          cannot be undone.
        </p>
        <button
          type="button"
          onClick={onDelete}
          disabled={deleting}
          style={{
            fontFamily: "var(--f-ui)",
            fontSize: 13,
            fontWeight: 500,
            padding: "8px 14px",
            background: "var(--bg-panel)",
            border: "1px solid var(--danger-strong)",
            borderRadius: "var(--r-md)",
            color: "var(--danger-strong)",
            cursor: deleting ? "default" : "pointer",
          }}
        >
          {deleting ? "Deleting…" : "Delete agent"}
        </button>
        {deleteError && (
          <p
            className="mt-3 text-[12px]"
            style={{ color: "var(--danger-strong)" }}
          >
            {deleteError}
          </p>
        )}
      </section>
    </div>
  );
}

function Section({
  title,
  subtitle,
  children,
}: {
  title: string;
  subtitle: string;
  children: React.ReactNode;
}) {
  return (
    <section
      className="mb-6"
      style={{
        background: "var(--bg-panel)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-lg)",
        padding: "20px 22px",
      }}
    >
      <Eyebrow>{title}</Eyebrow>
      <p
        className="mt-2 mb-4"
        style={{
          fontSize: 13,
          color: "var(--fg-muted)",
          lineHeight: 1.6,
          maxWidth: 580,
        }}
      >
        {subtitle}
      </p>
      {children}
    </section>
  );
}
