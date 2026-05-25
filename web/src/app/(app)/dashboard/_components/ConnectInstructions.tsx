"use client";

import { useState } from "react";

export function ConnectInstructions({ mode }: { mode?: string }) {
  const isLocal = mode === "local";

  return (
    <div className="space-y-3">
      <p className="text-xs text-muted">
        {isLocal
          ? "Install the e2a skill to connect your agent. Works with OpenClaw, Claude Code, Gemini CLI, or any agent that supports skills."
          : "Install the e2a skill to set up your webhook endpoint."}
      </p>
      <p className="text-xs font-medium text-foreground">Install the e2a skill</p>
      <CodeBlock code={`mkdir -p .claude/skills/e2a\ncurl -o .claude/skills/e2a/SKILL.md \\\n  https://raw.githubusercontent.com/Mnexa-AI/e2a/main/skills/using-e2a/SKILL.md`} />
      <p className="text-xs text-muted">
        Then use <code className="text-[11px] bg-surface px-1 py-0.5 rounded border border-border">/e2a</code> in your agent to get started.
        {isLocal
          ? " The skill walks through login, agent registration, and listening for emails automatically."
          : " The skill includes instructions for implementing a webhook endpoint to receive emails."}
      </p>
    </div>
  );
}

function CodeBlock({ code }: { code: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard.writeText(code);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="relative group">
      <pre className="bg-[#1a1a2e] text-[#e0e0e0] text-xs font-mono p-3 rounded-lg overflow-x-auto">
        <code>{code}</code>
      </pre>
      <button
        type="button"
        onClick={copy}
        className="absolute top-1.5 right-1.5 px-2 py-0.5 text-[10px] font-medium bg-white/10 text-white/70 rounded hover:bg-white/20 transition opacity-0 group-hover:opacity-100"
      >
        {copied ? "Copied!" : "Copy"}
      </button>
    </div>
  );
}
