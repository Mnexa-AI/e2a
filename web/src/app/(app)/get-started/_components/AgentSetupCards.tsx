"use client";

import { useState } from "react";

const MCP_URL = "https://api.e2a.dev/mcp";
const DOC_URL = "https://e2a.dev/setup.md";
const CLAUDE_PLUGIN_COMMANDS = [
  "claude plugin marketplace add tokencanopy/e2a",
  "claude plugin install e2a@e2a",
].join("\n");
const CLAUDE_MCP_COMMAND =
  "claude mcp add --transport http --scope user e2a " + MCP_URL;
const CODEX_PLUGIN_COMMAND = "codex plugin marketplace add tokencanopy/e2a";
const CODEX_MCP_COMMANDS = [
  "codex mcp add e2a --url " + MCP_URL,
  "codex mcp login e2a",
].join("\n");
const GENERIC_MCP_CONFIG = [
  "{",
  '  "mcpServers": {',
  '    "e2a": { "url": "' + MCP_URL + '" }',
  "  }",
  "}",
].join("\n");

type Client = "claude" | "codex" | "other";

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
      className="font-mono text-[12px] leading-[1.7] p-3.5 mb-2.5 overflow-x-auto whitespace-pre-wrap"
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

function MethodLabel({ children }: { children: React.ReactNode }) {
  return (
    <span
      className="inline-flex mb-2 font-mono text-[10px] font-semibold uppercase tracking-[0.08em] px-2 py-1"
      style={{
        color: "var(--accent-strong)",
        background: "var(--accent-soft)",
        borderRadius: "var(--r-sm)",
      }}
    >
      {children}
    </span>
  );
}

function SetupSteps({ children }: { children: React.ReactNode }) {
  return (
    <ol
      className="mt-3.5 mb-0 text-[12px] leading-[1.8] list-decimal pl-5"
      style={{ color: "var(--fg-muted)" }}
    >
      {children}
    </ol>
  );
}

function SecondaryMethod({ children }: { children: React.ReactNode }) {
  return (
    <div className="mt-5 pt-5" style={{ borderTop: "1px solid var(--border)" }}>
      {children}
    </div>
  );
}

function ClaudeSetup() {
  return (
    <div>
      <h3 className="text-[16px] font-semibold mb-1" style={{ color: "var(--fg)" }}>
        Claude Code setup
      </h3>
      <p className="text-[12px] mb-3 leading-[1.6]" style={{ color: "var(--fg-muted)" }}>
        Install the plugin to get both the e2a MCP tools and built-in guidance for
        handling email correctly.
      </p>
      <MethodLabel>Recommended · Plugin + MCP</MethodLabel>
      <CodeBlock>{CLAUDE_PLUGIN_COMMANDS}</CodeBlock>
      <CopyButton value={CLAUDE_PLUGIN_COMMANDS} label="Copy plugin commands" />
      <SetupSteps>
        <li>Run both commands, then open or restart Claude Code.</li>
        <li>
          Run <code>/mcp</code>, choose <strong>e2a</strong>, and authorize in your
          browser.
        </li>
        <li>Ask Claude: “Create my first inbox on the shared e2a domain.”</li>
      </SetupSteps>

      <SecondaryMethod>
        <MethodLabel>MCP only</MethodLabel>
        <p className="text-[12px] mb-3 leading-[1.6]" style={{ color: "var(--fg-muted)" }}>
          Connect the tools without installing the e2a skill.
        </p>
        <CodeBlock>{CLAUDE_MCP_COMMAND}</CodeBlock>
        <CopyButton value={CLAUDE_MCP_COMMAND} label="Copy MCP command" />
        <p className="text-[12px] mt-3 mb-0" style={{ color: "var(--fg-muted)" }}>
          Then run <code>/mcp</code> and complete OAuth in your browser.
        </p>
      </SecondaryMethod>
    </div>
  );
}

function CodexSetup() {
  return (
    <div>
      <h3 className="text-[16px] font-semibold mb-1" style={{ color: "var(--fg)" }}>
        Codex setup
      </h3>
      <p className="text-[12px] mb-3 leading-[1.6]" style={{ color: "var(--fg-muted)" }}>
        Install the plugin to add the e2a skill and hosted MCP server together.
      </p>
      <MethodLabel>Recommended · Plugin + MCP</MethodLabel>
      <CodeBlock>{CODEX_PLUGIN_COMMAND}</CodeBlock>
      <CopyButton value={CODEX_PLUGIN_COMMAND} label="Copy marketplace command" />
      <SetupSteps>
        <li>
          Launch Codex, run <code>/plugins</code>, search for <strong>e2a</strong>,
          and install it.
        </li>
        <li>Follow the browser prompt to authorize e2a.</li>
        <li>Ask Codex: “Create my first inbox on the shared e2a domain.”</li>
      </SetupSteps>
      <p className="text-[12px] mt-3 mb-0" style={{ color: "var(--fg-muted)" }}>
        Codex desktop: open <strong>Plugins → Add more +</strong> and paste{" "}
        <code>https://github.com/tokencanopy/e2a</code>.
      </p>

      <SecondaryMethod>
        <MethodLabel>MCP only</MethodLabel>
        <p className="text-[12px] mb-3 leading-[1.6]" style={{ color: "var(--fg-muted)" }}>
          Add the hosted server directly if you only need the MCP tools.
        </p>
        <CodeBlock>{CODEX_MCP_COMMANDS}</CodeBlock>
        <CopyButton value={CODEX_MCP_COMMANDS} label="Copy MCP commands" />
      </SecondaryMethod>
    </div>
  );
}

function OtherSetup() {
  return (
    <div>
      <h3 className="text-[16px] font-semibold mb-1" style={{ color: "var(--fg)" }}>
        Connect any MCP client
      </h3>
      <p className="text-[12px] mb-3 leading-[1.6]" style={{ color: "var(--fg-muted)" }}>
        For Cursor, Claude Desktop, VS Code, Windsurf, Goose, Zed, and other clients
        that support remote MCP servers and OAuth.
      </p>
      <MethodLabel>Hosted MCP endpoint</MethodLabel>
      <CodeBlock>{MCP_URL}</CodeBlock>
      <CopyButton value={MCP_URL} label="Copy MCP URL" />

      <SecondaryMethod>
        <MethodLabel>Common mcp.json format</MethodLabel>
        <CodeBlock>{GENERIC_MCP_CONFIG}</CodeBlock>
        <CopyButton value={GENERIC_MCP_CONFIG} label="Copy JSON config" />
      </SecondaryMethod>

      <SetupSteps>
        <li>Add e2a as a remote HTTP MCP server in your client.</li>
        <li>Complete OAuth when your browser opens. No API key is required.</li>
        <li>Ask your agent to create an inbox on the shared e2a domain.</li>
      </SetupSteps>
      <p className="text-[12px] mt-3 mb-0" style={{ color: "var(--fg-muted)" }}>
        Need client-specific config? Read the{" "}
        <a
          href={DOC_URL}
          target="_blank"
          rel="noreferrer"
          className="underline"
          style={{ color: "var(--accent-strong)" }}
        >
          full connection guide
        </a>
        .
      </p>
    </div>
  );
}

export function AgentSetupCards({ onBack }: { onBack: () => void }) {
  const [client, setClient] = useState<Client>("claude");

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

      <div
        className="p-5"
        style={{
          background: "var(--bg-panel)",
          border: "1px solid var(--border)",
          borderRadius: "var(--r-lg)",
        }}
      >
        <h2 className="text-[18px] font-semibold mb-1" style={{ color: "var(--fg)" }}>
          Connect e2a to your agent
        </h2>
        <p className="text-[12px] mb-4 leading-[1.6]" style={{ color: "var(--fg-muted)" }}>
          Choose your client. Plugins include the e2a skill and MCP tools; direct MCP
          connections provide the tools only.
        </p>

        <div
          className="grid grid-cols-1 sm:grid-cols-3 gap-2 mb-5"
          role="group"
          aria-label="Choose your agent client"
        >
          {[
            ["claude", "Claude Code"],
            ["codex", "Codex"],
            ["other", "Other agents"],
          ].map(([value, label]) => {
            const selected = client === value;
            return (
              <button
                key={value}
                type="button"
                aria-pressed={selected}
                onClick={() => setClient(value as Client)}
                className="px-3 py-2 text-[12px] font-semibold transition"
                style={{
                  color: selected ? "var(--accent-strong)" : "var(--fg-muted)",
                  background: selected ? "var(--accent-soft)" : "var(--bg)",
                  border: selected
                    ? "1px solid var(--accent-strong)"
                    : "1px solid var(--border)",
                  borderRadius: "var(--r-md)",
                }}
              >
                {label}
              </button>
            );
          })}
        </div>

        {client === "claude" && <ClaudeSetup />}
        {client === "codex" && <CodexSetup />}
        {client === "other" && <OtherSetup />}
      </div>
    </div>
  );
}
