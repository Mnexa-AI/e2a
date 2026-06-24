"use client";

// Agentic onboarding: instead of the browser forms, let the user's AI agent
// stand up an inbox headlessly over e2a's hosted MCP server. Two paths mirror
// how agents actually connect:
//   1. Paste a prompt into a no-terminal agent (Claude Desktop, a chat box) —
//      it fetches the e2a skill, connects over MCP, and provisions the inbox.
//   2. One CLI command for OAuth-capable MCP clients (Claude Code, Cursor, …).
// e2a's MCP is hosted with OAuth 2.1, so neither path needs an API key pasted.

import { useState } from "react";

// Hosted, account-scoped MCP endpoint (mcp/README.md). OAuth 2.1 → the
// `mcp add` command opens a browser consent flow; no key to copy.
const MCP_URL = "https://api.e2a.dev/mcp";
// Hosted agent-facing setup doc (web/public/e2a.md). The pasted agent fetches
// it to learn how to connect over MCP + drive e2a — it carries the connect
// instructions, so the prompt just points the agent at it.
const DOC_URL = "https://e2a.dev/e2a.md";

const PASTE_PROMPT = `Give yourself an email inbox with e2a. Read ${DOC_URL} and follow it to connect over MCP (${MCP_URL}) and set up my inbox, then walk me through it.`;

const CONNECT_COMMAND = `claude mcp add --transport http e2a ${MCP_URL}`;

function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      onClick={() => {
        navigator.clipboard?.writeText(value);
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      }}
      className="text-[12px] px-3 py-1.5 transition"
      style={{
        background: "var(--bg-panel)",
        color: "var(--fg)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-md)",
      }}
    >
      {copied ? "Copied!" : label}
    </button>
  );
}

function CodeBlock({ children }: { children: React.ReactNode }) {
  return (
    <div
      className="font-mono text-[12px] leading-[1.6] p-3.5 mb-2.5 overflow-x-auto whitespace-pre-wrap"
      style={{
        background: "var(--ink, #1c1917)",
        color: "var(--ink-fg, #e7e5e4)",
        borderRadius: "var(--r-md)",
      }}
    >
      {children}
    </div>
  );
}

function SetupCard({
  step,
  title,
  blurb,
  children,
}: {
  step: string;
  title: string;
  blurb: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div
      className="p-5"
      style={{
        background: "var(--bg-panel)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-lg)",
      }}
    >
      <div className="text-[14px] font-semibold mb-1" style={{ color: "var(--fg)" }}>
        {step} · {title}
      </div>
      <p className="text-[12px] mb-3 leading-[1.6]" style={{ color: "var(--fg-muted)" }}>
        {blurb}
      </p>
      {children}
    </div>
  );
}

export function AgentSetupCards({ onBack }: { onBack: () => void }) {
  return (
    <div>
      <button
        type="button"
        onClick={onBack}
        className="text-[12px] mb-4 inline-flex items-center gap-1 transition"
        style={{ color: "var(--fg-muted)" }}
      >
        ← Back
      </button>

      <div className="flex flex-col gap-3.5">
        <SetupCard
          step="1"
          title="Paste into your agent"
          blurb={
            <>
              For any agent without a terminal (Claude Desktop, a chat box). It
              fetches the e2a skill, connects over MCP, and sets up your inbox —
              no API key, nothing on your end.
            </>
          }
        >
          <CodeBlock>{PASTE_PROMPT}</CodeBlock>
          <CopyButton value={PASTE_PROMPT} label="Copy prompt" />
        </SetupCard>

        <SetupCard
          step="2"
          title="Or connect a client directly"
          blurb={
            <>
              For Claude Code, Cursor, Goose, Windsurf, Zed, or any MCP client
              that supports OAuth. One command — no API key to copy.
            </>
          }
        >
          <CodeBlock>
            <span style={{ color: "var(--ok, #4ade80)" }}>$ </span>
            {CONNECT_COMMAND}
          </CodeBlock>
          <CopyButton value={CONNECT_COMMAND} label="Copy command" />
          <ol
            className="mt-3.5 mb-0 text-[12px] leading-[1.8] list-decimal pl-5"
            style={{ color: "var(--fg-muted)" }}
          >
            <li>Run the command — your browser opens automatically.</li>
            <li>Sign in to e2a (the account you&apos;re in right now).</li>
            <li>
              Approve, then ask your agent to set up an inbox (e.g. “create me an
              inbox on the shared domain”).
            </li>
          </ol>
        </SetupCard>
      </div>
    </div>
  );
}
