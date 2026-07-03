"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "./loft/Button";

// Copy-paste prompts for driving each workspace surface from a coding
// agent (Claude Code, Cursor, …) instead of the dashboard. The templates
// prompt is kept in sync with docs/templates.md ("Using a coding
// agent?"); the others anchor on the agent-facing doc at e2a.dev/e2a.md.
export const AGENT_PROMPTS = {
  templates: {
    blurb:
      "Templates are a one-time setup your coding agent can do headlessly — paste this into Claude Code, Cursor, or any agent connected to e2a.",
    prompt: `Read https://e2a.dev/templates.md and set up e2a email templates for this project: browse the starter templates, copy the ones we need via from_starter, brand them (accent color is marked <!-- BRAND: accent -->), and wire our transactional sends using template_alias + template_data. Use the e2a MCP tools if connected (otherwise the REST API with $E2A_API_KEY), validate each template before wiring it in, and finish by listing the templates you created plus the send code you added.`,
  },
  inboxes: {
    blurb:
      "Creating and wiring an inbox is a one-time setup your coding agent can do headlessly — paste this into Claude Code, Cursor, or any agent connected to e2a.",
    prompt: `Read https://e2a.dev/e2a.md and set up e2a email for this project's agent: connect over MCP (https://api.e2a.dev/mcp) or the REST API with $E2A_API_KEY, create an agent inbox, and wire inbound delivery into this project (webhook, or WebSocket listen for local dev). Finish by sending a test email to the new inbox and replying to it in-thread to prove the send/receive loop works.`,
  },
  domains: {
    blurb:
      "Domain setup is a one-time task your coding agent can drive end to end — paste this into Claude Code, Cursor, or any agent connected to e2a.",
    prompt: `Read https://e2a.dev/e2a.md and connect our custom domain to e2a: register it, show me the DNS records to add (or add them yourself if you have access to our DNS provider), poll verification until it passes, then create the agent inboxes we need on the domain. Use the e2a MCP tools if connected, otherwise the REST API with $E2A_API_KEY.`,
  },
} as const;

export type AgentPromptCardProps = {
  blurb: string;
  prompt: string;
};

// A copy-paste prompt card: these surfaces are one-time-setup shaped, so
// the card hands the developer's coding agent the whole job instead of
// walking the human through clicks.
export function AgentPromptCard({ blurb, prompt }: AgentPromptCardProps) {
  const [copied, setCopied] = useState(false);
  const copyTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => {
      if (copyTimer.current !== null) clearTimeout(copyTimer.current);
    };
  }, []);

  const onCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(prompt);
      setCopied(true);
      if (copyTimer.current !== null) clearTimeout(copyTimer.current);
      copyTimer.current = setTimeout(() => {
        setCopied(false);
        copyTimer.current = null;
      }, 1200);
    } catch {
      // clipboard unavailable — silently ignore
    }
  }, [prompt]);

  return (
    <section
      aria-label="Set up with a coding agent"
      className="rounded-[var(--r-lg)] border p-5"
      style={{ background: "var(--bg-panel)", borderColor: "var(--border)" }}
    >
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2
            className="text-[16px] font-semibold"
            style={{ color: "var(--fg)", margin: 0 }}
          >
            Set up with a coding agent
          </h2>
          <p
            className="text-[13px] mt-1 mb-0 leading-[1.6]"
            style={{ color: "var(--fg-muted)", maxWidth: 640 }}
          >
            {blurb}
          </p>
        </div>
        <Button variant="ghost" onClick={onCopy} aria-label="Copy prompt">
          {copied ? "Copied" : "Copy prompt"}
        </Button>
      </div>
      <pre
        className="mt-4 mb-0 whitespace-pre-wrap rounded-[var(--r-md)] border p-3.5 text-[12px] leading-[1.7]"
        style={{
          fontFamily: "var(--f-mono)",
          color: "var(--fg-muted)",
          background: "var(--bg)",
          borderColor: "var(--border)",
        }}
      >
        {prompt}
      </pre>
    </section>
  );
}
