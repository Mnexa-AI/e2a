"use client";

import { useState } from "react";
import { updateAgent } from "../../../components/onboarding/api";
import { isValidWebhookUrl } from "../../../components/onboarding/state";

export function WebhookEditor({
  email,
  currentUrl,
  onUpdated,
}: {
  email: string;
  currentUrl: string;
  onUpdated: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(currentUrl);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  if (!editing) {
    return (
      <p className="text-xs text-muted">
        Webhook:{" "}
        <code className="font-mono text-foreground">{currentUrl}</code>
        <button
          onClick={() => { setEditing(true); setValue(currentUrl); }}
          className="ml-2 text-accent hover:underline transition"
        >
          Edit
        </button>
      </p>
    );
  }

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!isValidWebhookUrl(value)) {
      setError("Enter a valid HTTPS URL");
      return;
    }
    setSaving(true);
    setError("");
    try {
      await updateAgent(email, { webhook_url: value });
      setEditing(false);
      onUpdated();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to update webhook");
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={handleSave} className="space-y-2">
      <div className="flex items-center gap-2">
        <input
          type="url"
          value={value}
          onChange={(e) => { setValue(e.target.value); setError(""); }}
          className="flex-1 text-xs px-2 py-1 border border-border rounded-md font-mono"
          placeholder="https://example.com/webhook"
        />
        <button
          type="submit"
          disabled={saving}
          className="text-xs px-2 py-1 bg-foreground text-background rounded-md hover:opacity-90 transition disabled:opacity-50"
        >
          {saving ? "Saving..." : "Save"}
        </button>
        <button
          type="button"
          onClick={() => { setEditing(false); setError(""); }}
          className="text-xs px-2 py-1 border border-border rounded-md hover:bg-gray-50 transition"
        >
          Cancel
        </button>
      </div>
      {error && <p className="text-xs text-red-600">{error}</p>}
    </form>
  );
}
