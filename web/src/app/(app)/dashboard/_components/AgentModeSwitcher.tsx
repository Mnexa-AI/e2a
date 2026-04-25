"use client";

import { useState } from "react";
import { updateAgent } from "../../../components/onboarding/api";
import { isValidWebhookUrl } from "../../../components/onboarding/state";

export function AgentModeSwitcher({
  email,
  currentMode,
  onSwitched,
}: {
  email: string;
  currentMode: string;
  onSwitched: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [webhookUrl, setWebhookUrl] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const switchingToCloud = currentMode === "local";

  const handleSwitch = async () => {
    if (switchingToCloud) {
      // Need webhook URL — open inline form
      if (!open) {
        setOpen(true);
        return;
      }
      if (!isValidWebhookUrl(webhookUrl)) {
        setError("Enter a valid HTTPS URL");
        return;
      }
    }

    setSaving(true);
    setError("");
    try {
      if (switchingToCloud) {
        await updateAgent(email, { agent_mode: "cloud", webhook_url: webhookUrl });
      } else {
        await updateAgent(email, { agent_mode: "local", webhook_url: "" });
      }
      setOpen(false);
      setWebhookUrl("");
      onSwitched();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to switch mode");
    } finally {
      setSaving(false);
    }
  };

  if (!open) {
    return (
      <button
        onClick={handleSwitch}
        className="text-xs text-accent hover:underline transition"
      >
        Switch to {switchingToCloud ? "cloud" : "local"}
      </button>
    );
  }

  return (
    <div className="mt-2 p-3 border border-border rounded-lg bg-surface space-y-2">
      <p className="text-xs font-medium">
        Switch to cloud — enter your webhook URL
      </p>
      <input
        type="url"
        placeholder="https://example.com/webhook"
        value={webhookUrl}
        onChange={(e) => { setWebhookUrl(e.target.value); setError(""); }}
        className="w-full text-xs px-2 py-1.5 border border-border rounded-md font-mono"
      />
      {error && <p className="text-xs text-red-600">{error}</p>}
      <div className="flex gap-2">
        <button
          onClick={handleSwitch}
          disabled={saving}
          className="text-xs px-3 py-1.5 bg-foreground text-background rounded-md hover:opacity-90 transition disabled:opacity-50"
        >
          {saving ? "Switching..." : "Switch to cloud"}
        </button>
        <button
          onClick={() => { setOpen(false); setError(""); setWebhookUrl(""); }}
          className="text-xs px-3 py-1.5 border border-border rounded-md hover:bg-gray-50 transition"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}
