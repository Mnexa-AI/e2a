"use client";

// Top-level onboarding fork: hand setup to an AI agent over MCP (recommended,
// headless) or set up in the web UI (the shared/custom-domain forms). The agent
// path is recommended because it provisions the inbox end-to-end with no forms
// and no API key; the web path is the click-through fallback.

import { useState } from "react";
import type { SetupMethod } from "../../../components/onboarding/types";
import { Chip } from "../../../components/loft/Chip";

// One selectable card. The recommended card carries a standing accent border;
// hover (or selection) adds the accent + an elevated background as interactive
// feedback. Inline `background`/`border` win over Tailwind `hover:` utilities,
// so the hover state is driven from React state, not a `hover:` class. Border
// width is a constant 2px in both states so hovering never shifts layout.
function MethodCard({
  recommended = false,
  active,
  onClick,
  children,
}: {
  recommended?: boolean;
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  const [hovered, setHovered] = useState(false);
  const accent = recommended || active || hovered;
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      className="relative text-left transition focus:outline-none"
      style={{
        background: hovered ? "var(--bg-elev)" : "var(--bg-panel)",
        border: accent ? "2px solid var(--accent)" : "2px solid var(--border)",
        borderRadius: "var(--r-lg)",
        padding: 18,
        cursor: "pointer",
      }}
    >
      {children}
    </button>
  );
}

export function SetupMethodChoice({
  selected,
  onSelect,
}: {
  selected: SetupMethod | null;
  onSelect: (method: SetupMethod) => void;
}) {
  return (
    <div className="grid gap-3.5 sm:grid-cols-2">
      <MethodCard recommended active={selected === "agent"} onClick={() => onSelect("agent")}>
        <div className="flex items-center gap-2 mb-1 flex-wrap">
          <span className="text-[14px] font-semibold" style={{ color: "var(--fg)" }}>
            With an agent
          </span>
          <Chip tone="accent">Recommended</Chip>
          <Chip tone="neutral" mono>
            MCP
          </Chip>
        </div>
        <div
          className="font-mono text-[12px] mb-3"
          style={{ color: "var(--accent-strong)" }}
        >
          headless · no forms
        </div>
        <ul
          className="list-none m-0 p-0 text-[12px] leading-[1.7]"
          style={{ color: "var(--fg-muted)" }}
        >
          <li>· Your agent connects over MCP and sets up the inbox</li>
          <li>· OAuth sign-in — no API key to copy</li>
          <li>· Claude Code, Cursor, Claude Desktop…</li>
        </ul>
      </MethodCard>

      <MethodCard active={selected === "web"} onClick={() => onSelect("web")}>
        <div className="flex items-center gap-2 mb-1">
          <span className="text-[14px] font-semibold" style={{ color: "var(--fg)" }}>
            Set up in the web UI
          </span>
        </div>
        <div
          className="font-mono text-[12px] mb-3"
          style={{ color: "var(--fg-muted)" }}
        >
          shared or custom domain
        </div>
        <ul
          className="list-none m-0 p-0 text-[12px] leading-[1.7]"
          style={{ color: "var(--fg-muted)" }}
        >
          <li>· Pick a shared e2a or your own domain</li>
          <li>· Fill a short form, verify in the browser</li>
          <li>· No agent required</li>
        </ul>
      </MethodCard>
    </div>
  );
}
