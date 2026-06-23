"use client";

import { useState } from "react";
import Link from "next/link";
import type { DashboardAgent } from "../../../components/types";
import { Chip } from "../../../components/loft/Chip";
import { Dot } from "../../../components/loft/Dot";
import { sendAgentTestEmail } from "../../../components/onboarding/api";

export function AgentCard({
  agent,
}: {
  agent: DashboardAgent;
}) {
  const [testState, setTestState] = useState<"idle" | "sending" | "sent">("idle");
  const [testError, setTestError] = useState("");

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

        {/* Actions: Test only. Editing (review queue) + delete live on
            the per-agent Settings page, reached via the bottom CTA bar.
            Connection setup lives in onboarding / the e2a skill. */}
        <div className="flex gap-2 shrink-0 md:ml-4 flex-wrap">
          {agent.domain_verified && (
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

// Delete moved to /dashboard/agents/settings → Danger zone, so the
// agent card no longer needs an overflow menu. Future per-agent
// quick-actions (e.g. Export activity CSV) would slot back in here
// or, more likely, into Settings.
