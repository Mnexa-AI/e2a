"use client";

import type { AddressType } from "../../../components/onboarding/types";
import { AGENTS_DOMAIN } from "../../../../lib/site";

export function AddressChoice({
  selected,
  onSelect,
}: {
  selected: AddressType | null;
  onSelect: (type: AddressType) => void;
}) {
  return (
    <div>
      <h2 className="text-2xl font-bold tracking-tight mb-2">
        Give your agent an email address
      </h2>
      <p className="text-muted mb-8">
        Choose the kind of email identity for your agent. You can always add more agents later.
      </p>

      <div className="grid gap-4 sm:grid-cols-2">
        <button
          type="button"
          aria-pressed={selected === "shared"}
          onClick={() => onSelect("shared")}
          className={`text-left p-5 rounded-lg border-2 transition ${
            selected === "shared"
              ? "border-accent bg-accent/5"
              : "border-border hover:border-foreground/20"
          }`}
        >
          <p className="font-medium text-sm mb-1">Shared e2a domain</p>
          <p className="text-xs text-muted mb-3">
            Get a working agent email in seconds.
          </p>
          <code className="text-xs bg-surface px-2 py-1 rounded border border-border">
            your-slug@{AGENTS_DOMAIN || "agents.example.com"}
          </code>
        </button>

        <button
          type="button"
          aria-pressed={selected === "custom"}
          onClick={() => onSelect("custom")}
          className={`text-left p-5 rounded-lg border-2 transition ${
            selected === "custom"
              ? "border-accent bg-accent/5"
              : "border-border hover:border-foreground/20"
          }`}
        >
          <p className="font-medium text-sm mb-1">Custom domain</p>
          <p className="text-xs text-muted mb-3">
            Use your own domain for branded agent addresses.
          </p>
          <code className="text-xs bg-surface px-2 py-1 rounded border border-border">
            support@mail.yourcompany.com
          </code>
        </button>
      </div>
    </div>
  );
}
