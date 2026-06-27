"use client";

import type { AddressType } from "../../../components/onboarding/types";
import { Chip } from "@e2a/ui";
import { AGENTS_DOMAIN_DISPLAY } from "../../../../lib/site";
import { SelectableCard } from "./SelectableCard";

export function AddressChoice({
  selected,
  onSelect,
}: {
  selected: AddressType | null;
  onSelect: (type: AddressType) => void;
}) {
  return (
    <div className="grid gap-3.5 sm:grid-cols-2">
      <SelectableCard active={selected === "shared"} onClick={() => onSelect("shared")}>
        <div className="flex items-center gap-2 mb-1 flex-wrap">
          <span className="text-[14px] font-semibold" style={{ color: "var(--fg)" }}>
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
      </SelectableCard>

      <SelectableCard active={selected === "custom"} onClick={() => onSelect("custom")}>
        <div className="flex items-center gap-2 mb-1">
          <span className="text-[14px] font-semibold" style={{ color: "var(--fg)" }}>
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
      </SelectableCard>
    </div>
  );
}
