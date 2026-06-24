"use client";

import type { AddressType } from "../../../components/onboarding/types";
import { Chip } from "../../../components/loft/Chip";
import { AGENTS_DOMAIN_DISPLAY } from "../../../../lib/site";

export function AddressChoice({
  selected,
  onSelect,
}: {
  selected: AddressType | null;
  onSelect: (type: AddressType) => void;
}) {
  return (
    <div>
      <div className="grid gap-3.5 sm:grid-cols-3">
        {/* Shared option */}
        <button
          type="button"
          aria-pressed={selected === "shared"}
          onClick={() => onSelect("shared")}
          className="relative text-left transition focus:outline-none"
          style={{
            background: "var(--bg-panel)",
            border:
              selected === "shared"
                ? "2px solid var(--accent)"
                : "2px solid var(--accent)",
            borderRadius: "var(--r-lg)",
            padding: 18,
          }}
        >
          <div className="flex items-center gap-2 mb-1 flex-wrap">
            <span
              className="text-[14px] font-semibold"
              style={{ color: "var(--fg)" }}
            >
              Shared e2a domain
            </span>
            <Chip tone="accent">Recommended</Chip>
            <Chip tone="success" mono>
              1 min
            </Chip>
          </div>
          <div
            className="font-mono text-[12px] mb-3"
            style={{ color: "var(--accent-strong)" }}
          >
            your-slug@{AGENTS_DOMAIN_DISPLAY}
          </div>
          <ul
            className="list-none m-0 p-0 text-[12px] leading-[1.7]"
            style={{ color: "var(--fg-muted)" }}
          >
            <li>· Skip DNS setup entirely</li>
            <li>· Inherits e2a&apos;s verified domain</li>
            <li>· Best for prototypes and testing</li>
          </ul>
        </button>

        {/* Custom option */}
        <button
          type="button"
          aria-pressed={selected === "custom"}
          onClick={() => onSelect("custom")}
          className="relative text-left transition focus:outline-none"
          style={{
            background: "var(--bg-panel)",
            border:
              selected === "custom"
                ? "2px solid var(--accent)"
                : "1px solid var(--border)",
            borderRadius: "var(--r-lg)",
            padding: 18,
          }}
        >
          <div className="flex items-center gap-2 mb-1">
            <span
              className="text-[14px] font-semibold"
              style={{ color: "var(--fg)" }}
            >
              Custom domain
            </span>
            <Chip tone="neutral" mono>
              ~10 min
            </Chip>
          </div>
          <div
            className="font-mono text-[12px] mb-3"
            style={{ color: "var(--fg-muted)" }}
          >
            you@<span style={{ color: "var(--fg)" }}>yourcompany.com</span>
          </div>
          <ul
            className="list-none m-0 p-0 text-[12px] leading-[1.7]"
            style={{ color: "var(--fg-muted)" }}
          >
            <li>· Use your own domain (e.g. acme.io)</li>
            <li>· Add DNS records · verify in &lt;5min</li>
            <li>· Production-ready, brand-safe</li>
          </ul>
        </button>

        {/* Agentic / MCP option — hand setup to the user's agent over MCP */}
        <button
          type="button"
          aria-pressed={selected === "agent"}
          onClick={() => onSelect("agent")}
          className="relative text-left transition focus:outline-none"
          style={{
            background: "var(--bg-panel)",
            border:
              selected === "agent"
                ? "2px solid var(--accent)"
                : "1px solid var(--border)",
            borderRadius: "var(--r-lg)",
            padding: 18,
          }}
        >
          <div className="flex items-center gap-2 mb-1">
            <span
              className="text-[14px] font-semibold"
              style={{ color: "var(--fg)" }}
            >
              With an agent
            </span>
            <Chip tone="neutral" mono>
              MCP
            </Chip>
          </div>
          <div
            className="font-mono text-[12px] mb-3"
            style={{ color: "var(--fg-muted)" }}
          >
            headless · no forms
          </div>
          <ul
            className="list-none m-0 p-0 text-[12px] leading-[1.7]"
            style={{ color: "var(--fg-muted)" }}
          >
            <li>· Your agent connects over MCP</li>
            <li>· OAuth sign-in — no API key</li>
            <li>· Claude Code, Cursor, Desktop…</li>
          </ul>
        </button>
      </div>
    </div>
  );
}
