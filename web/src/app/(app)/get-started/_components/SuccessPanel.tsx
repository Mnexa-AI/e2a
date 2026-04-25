"use client";

import { useState } from "react";
import type { AgentMode } from "../../../components/onboarding/types";
import type { AgentData } from "../../../components/types";

type TestState = "idle" | "sending" | "delivered";

export function SuccessPanel({
  agent,
  mode,
  webhookUrl,
}: {
  agent: AgentData;
  mode: AgentMode;
  webhookUrl?: string;
}) {
  const [testState, setTestState] = useState<TestState>("idle");
  const [sendError, setSendError] = useState("");

  async function sendTestEmail() {
    setSendError("");
    setTestState("sending");
    try {
      const res = await fetch(`/api/v1/agents/${encodeURIComponent(agent.email)}/test`, {
        method: "POST",
        credentials: "include",
      });
      if (res.ok) {
        setTestState("delivered");
      } else {
        const msg = await res.text();
        console.error("Test email failed:", res.status, msg);
        setSendError(msg || `Request failed (${res.status})`);
        setTestState("idle");
      }
    } catch (err) {
      console.error("Failed to send test email:", err);
      setSendError("Network error — check your connection and try again.");
      setTestState("idle");
    }
  }

  return (
    <div>
      <div className="mb-6 p-4 bg-green-50 border border-green-200 rounded-lg text-sm text-green-800">
        <span className="font-medium">Agent registered!</span>{" "}
        Your agent&apos;s email is{" "}
        <code className="text-xs bg-white/60 px-1.5 py-0.5 rounded font-mono">
          {agent.email}
        </code>
      </div>

      <div className="mb-8">
        {testState === "idle" && (
          <button
            type="button"
            onClick={sendTestEmail}
            className="w-full bg-foreground text-background py-3 rounded-lg text-sm font-medium hover:opacity-90 transition"
          >
            Send a test email to {agent.email} →
          </button>
        )}
        {testState === "sending" && (
          <button
            type="button"
            disabled
            className="w-full bg-surface text-muted py-3 rounded-lg text-sm font-medium border border-border cursor-not-allowed"
          >
            Sending…
          </button>
        )}
        {testState === "delivered" && (
          <>
            <button
              type="button"
              disabled
              className="w-full bg-green-600 text-white py-3 rounded-lg text-sm font-medium flex items-center justify-center gap-2 cursor-default"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="20 6 9 17 4 12" />
              </svg>
              Email is waiting in {agent.email}&apos;s inbox
            </button>
            <p className="mt-3 text-sm text-muted text-center">
              Connect your agent below to receive it. It will be the first message that arrives.
            </p>
          </>
        )}
        {sendError && (
          <p className="mt-3 text-sm text-red-600">{sendError}</p>
        )}
      </div>

      <h2 className="text-2xl font-bold tracking-tight mb-2">
        Connect your agent
      </h2>
      <p className="text-muted mb-6">
        Give the following commands to your agent to learn the e2a skill. Works with OpenClaw, Claude Code, Gemini CLI, or any agent that supports skills.
      </p>

      {/* Webhook summary for cloud-mode agents */}
      {mode === "cloud" && webhookUrl && (
        <div className="mb-6 p-4 border border-border rounded-lg space-y-2">
          <p className="text-sm font-medium">Webhook endpoint</p>
          <code className="block text-xs font-mono bg-surface px-3 py-2 rounded border border-border break-all">
            {webhookUrl}
          </code>
          <p className="text-xs text-muted">
            Inbound emails to <code className="bg-surface px-1 py-0.5 rounded border border-border">{agent.email}</code> will
            be POSTed to this URL as JSON.
          </p>
        </div>
      )}

      <div className="space-y-4">
        <CodeBlock
          title="Install the e2a skill"
          code={`mkdir -p .claude/skills/e2a\ncurl -o .claude/skills/e2a/SKILL.md \\\n  https://raw.githubusercontent.com/Mnexa-AI/e2a/main/skills/SKILL.md`}
        />
        <p className="text-sm text-muted">
          Then use{" "}
          <code className="bg-surface px-1.5 py-0.5 rounded border border-border text-xs">/e2a</code>{" "}
          in your agent to get started. The skill walks through login, agent registration, and listening for emails automatically.
        </p>
      </div>

      <ApiKeyNote />

      <a
        href="/dashboard"
        className="mt-8 block w-full text-center bg-foreground text-background py-3 rounded-lg text-sm font-medium hover:opacity-90 transition"
      >
        Go to Agents
      </a>
    </div>
  );
}

function ApiKeyNote() {
  return (
    <div className="mt-6 p-4 border border-border rounded-lg text-sm text-muted">
      <p>
        <span className="font-medium text-foreground">API key required.</span>{" "}
        The CLI can create and save one automatically via{" "}
        <code className="bg-surface px-1 py-0.5 rounded border border-border">e2a login</code>.
        {" "}For direct API or SDK usage, manage keys on the{" "}
        <a href="/api-keys" className="text-accent hover:underline">API Keys</a>{" "}
        page. Full docs:{" "}
        <a href="https://www.npmjs.com/package/@e2a/cli" target="_blank" rel="noopener noreferrer" className="text-accent hover:underline">CLI</a>,{" "}
        <a href="/api-docs" className="text-accent hover:underline">API</a>,{" "}
        <a href="https://pypi.org/project/e2a/" target="_blank" rel="noopener noreferrer" className="text-accent hover:underline">Python SDK</a>.
      </p>
    </div>
  );
}

function CodeBlock({ title, code }: { title?: string; code: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard.writeText(code);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div>
      {title && <p className="text-xs font-medium mb-1.5">{title}</p>}
      <div className="relative group">
        <pre className="bg-[#1a1a2e] text-[#e0e0e0] text-sm font-mono p-4 rounded-lg overflow-x-auto">
          <code>{code}</code>
        </pre>
        <button
          type="button"
          onClick={copy}
          className="absolute top-2 right-2 px-2 py-1 text-[10px] font-medium bg-white/10 text-white/70 rounded hover:bg-white/20 transition opacity-0 group-hover:opacity-100"
        >
          {copied ? "Copied!" : "Copy"}
        </button>
      </div>
    </div>
  );
}
