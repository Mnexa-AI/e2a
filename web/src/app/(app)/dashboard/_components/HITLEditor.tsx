"use client";

import { useState } from "react";
import { updateAgent } from "../../../components/onboarding/api";

// TTL presets in seconds; "custom" maps to an inline number input.
const TTL_PRESETS: Array<{ label: string; seconds: number }> = [
  { label: "1 hour", seconds: 3600 },
  { label: "1 day", seconds: 86400 },
  { label: "7 days", seconds: 604800 },
];

const MAX_TTL = 604800; // must match internal/identity HITLMaxTTLSeconds

// presetLabelFor returns the human label for a known TTL, or "custom" for
// any value that doesn't match a preset.
function presetLabelFor(seconds: number): string {
  const match = TTL_PRESETS.find((p) => p.seconds === seconds);
  return match ? match.label : "custom";
}

export function HITLEditor({
  email,
  enabled,
  ttlSeconds,
  expirationAction,
  onUpdated,
}: {
  email: string;
  enabled: boolean;
  ttlSeconds: number;
  expirationAction: "approve" | "reject";
  onUpdated: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [draftEnabled, setDraftEnabled] = useState(enabled);
  const [draftTTL, setDraftTTL] = useState(ttlSeconds);
  const [draftAction, setDraftAction] =
    useState<"approve" | "reject">(expirationAction);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const resetDrafts = () => {
    setDraftEnabled(enabled);
    setDraftTTL(ttlSeconds);
    setDraftAction(expirationAction);
    setError("");
  };

  if (!editing) {
    const status = enabled
      ? `Enabled · ${presetLabelFor(ttlSeconds)} · auto-${expirationAction} on expiry`
      : "Disabled";
    return (
      <p className="text-xs text-muted">
        HITL: <span className="text-foreground">{status}</span>
        <button
          onClick={() => {
            resetDrafts();
            setEditing(true);
          }}
          className="ml-2 text-accent hover:underline transition"
        >
          Edit
        </button>
      </p>
    );
  }

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (draftTTL <= 0 || draftTTL > MAX_TTL) {
      setError(`TTL must be between 1 and ${MAX_TTL} seconds (7 days).`);
      return;
    }
    setSaving(true);
    setError("");
    try {
      await updateAgent(email, {
        hitl_enabled: draftEnabled,
        hitl_ttl_seconds: draftTTL,
        hitl_expiration_action: draftAction,
      });
      setEditing(false);
      onUpdated();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to update HITL");
    } finally {
      setSaving(false);
    }
  };

  const currentPreset = presetLabelFor(draftTTL);

  return (
    <form onSubmit={handleSave} className="space-y-3 border border-border rounded-md p-3">
      <label className="flex items-center gap-2 text-xs">
        <input
          type="checkbox"
          checked={draftEnabled}
          onChange={(e) => setDraftEnabled(e.target.checked)}
          className="h-3.5 w-3.5"
        />
        <span>
          Require human approval before outbound messages are sent
        </span>
      </label>

      <div
        className={`space-y-2 ${draftEnabled ? "" : "opacity-50 pointer-events-none"}`}
        aria-disabled={!draftEnabled}
      >
        <div>
          <div className="text-xs text-muted mb-1">Approval window</div>
          <div className="flex items-center gap-1 flex-wrap">
            {TTL_PRESETS.map((p) => (
              <button
                key={p.label}
                type="button"
                onClick={() => setDraftTTL(p.seconds)}
                className={`text-xs px-2 py-1 rounded-md border transition ${
                  draftTTL === p.seconds
                    ? "bg-foreground text-background border-foreground"
                    : "border-border hover:bg-surface"
                }`}
              >
                {p.label}
              </button>
            ))}
            <label className="flex items-center gap-1 text-xs">
              <span
                className={`px-2 py-1 rounded-md border ${
                  currentPreset === "custom"
                    ? "bg-foreground text-background border-foreground"
                    : "border-border"
                }`}
              >
                custom
              </span>
              <input
                type="number"
                min={1}
                max={MAX_TTL}
                value={draftTTL}
                onChange={(e) => setDraftTTL(parseInt(e.target.value, 10) || 0)}
                className="w-24 text-xs px-2 py-1 border border-border rounded-md"
                aria-label="TTL in seconds"
              />
              <span className="text-muted">sec</span>
            </label>
          </div>
        </div>

        <div>
          <div className="text-xs text-muted mb-1">
            If no action is taken before the window closes
          </div>
          <div className="flex items-center gap-2">
            <label className="flex items-center gap-1.5 text-xs">
              <input
                type="radio"
                name={`hitl-action-${email}`}
                value="reject"
                checked={draftAction === "reject"}
                onChange={() => setDraftAction("reject")}
              />
              <span>Auto-reject (discard)</span>
            </label>
            <label className="flex items-center gap-1.5 text-xs">
              <input
                type="radio"
                name={`hitl-action-${email}`}
                value="approve"
                checked={draftAction === "approve"}
                onChange={() => setDraftAction("approve")}
              />
              <span>Auto-approve (send)</span>
            </label>
          </div>
        </div>

        <p className="text-[11px] text-muted leading-snug">
          While HITL is enabled, the full body and attachments of held
          messages are stored for up to the approval window above, then
          scrubbed on any terminal transition. Review notifications are
          emailed to your account email with only recipients and subject —
          the body is shown on the review page behind a token-gated link,
          not in the email itself.
        </p>
      </div>

      <div className="flex items-center gap-2">
        <button
          type="submit"
          disabled={saving}
          className="text-xs px-3 py-1.5 bg-foreground text-background rounded-md hover:opacity-90 transition disabled:opacity-50"
        >
          {saving ? "Saving…" : "Save"}
        </button>
        <button
          type="button"
          onClick={() => {
            setEditing(false);
            resetDrafts();
          }}
          className="text-xs px-3 py-1.5 border border-border rounded-md hover:bg-surface transition"
        >
          Cancel
        </button>
        {error && <p className="text-xs text-red-600">{error}</p>}
      </div>
    </form>
  );
}
