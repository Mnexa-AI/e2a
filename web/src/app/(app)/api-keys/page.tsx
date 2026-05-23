"use client";

import { useState, useEffect, useCallback } from "react";
import type { APIKeyData } from "../../components/types";
import { PageShell } from "../../components/loft/PageShell";
import { Chip } from "../../components/loft/Chip";

// Graceful degradation per REDESIGN.md §5:
// - "Last used" surfaced now that BACKEND_TODO #3 shipped api_keys.last_used_at
// - "Scopes" column intentionally omitted (BACKEND_TODO #11 — deferred indefinitely)

// formatRelative renders a "X ago" string for the Last used cell. Tight
// here because the column needs to read at-a-glance on a wide table —
// full timestamps eat horizontal space. Falls back to a date for old
// usage.
function formatRelative(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 0 || isNaN(diff)) return "—";
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return "just now";
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.floor(hr / 24);
  if (day < 30) return `${day}d ago`;
  return new Date(iso).toLocaleDateString();
}

export default function APIKeysPage() {
  const [keys, setKeys] = useState<APIKeyData[]>([]);
  const [loading, setLoading] = useState(true);
  const [newKeyName, setNewKeyName] = useState("");
  const [creating, setCreating] = useState(false);
  const [createdKey, setCreatedKey] = useState<APIKeyData | null>(null);

  const fetchKeys = useCallback(async () => {
    try {
      const res = await fetch("/api/keys");
      if (res.ok) setKeys(await res.json());
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchKeys();
  }, [fetchKeys]);

  const handleCreate = async () => {
    setCreating(true);
    try {
      const res = await fetch("/api/keys", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: newKeyName || "Default" }),
      });
      if (res.ok) {
        const key = await res.json();
        setCreatedKey(key);
        setNewKeyName("");
        fetchKeys();
      }
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (id: string) => {
    if (
      !confirm(
        "Delete this API key? Any integrations using it will stop working.",
      )
    )
      return;
    const res = await fetch(`/api/keys/${id}`, { method: "DELETE" });
    if (res.ok) fetchKeys();
  };

  return (
    <PageShell
      crumbs={["API keys"]}
      eyebrow="Workspace"
      title={<>API keys</>}
      subtitle="API keys authenticate your agents when sending or replying to emails via the API. One key works across all your agents."
    >
      {createdKey && createdKey.key && (
        <div
          className="mb-6 p-4"
          style={{
            background: "var(--success-bg)",
            border: "1px solid var(--success-bg)",
            color: "var(--success)",
            borderRadius: "var(--r-md)",
          }}
        >
          <p className="font-semibold text-[13px] mb-1.5">
            API key created — copy it now, it won&apos;t be shown again
          </p>
          <code
            className="block font-mono text-[12px] px-3 py-2 mb-2 break-all select-all"
            style={{
              background: "var(--bg-panel)",
              color: "var(--fg)",
              borderRadius: "var(--r-sm)",
            }}
          >
            {createdKey.key}
          </code>
          <button
            onClick={() => setCreatedKey(null)}
            className="text-[12px] underline"
            style={{ color: "var(--success)" }}
          >
            Dismiss
          </button>
        </div>
      )}

      <div className="flex items-end gap-3 mb-6 flex-wrap">
        <div className="flex-1 min-w-[200px]">
          <label
            className="block text-[12px] font-medium mb-1"
            style={{ color: "var(--fg-muted)" }}
          >
            Key name (optional)
          </label>
          <input
            type="text"
            value={newKeyName}
            onChange={(e) => setNewKeyName(e.target.value)}
            placeholder="e.g. Production"
            className="w-full px-3 py-2 text-[13px]"
            style={{
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-md)",
              color: "var(--fg)",
            }}
          />
        </div>
        <button
          onClick={handleCreate}
          disabled={creating}
          className="px-4 py-2 text-[13px] font-medium transition disabled:opacity-50"
          style={{
            background: "var(--accent-fill)",
            color: "var(--accent-fg)",
            borderRadius: "var(--r-md)",
          }}
        >
          {creating ? "Creating..." : "Create key"}
        </button>
      </div>

      {loading ? (
        <p
          className="text-[13px] py-12 text-center"
          style={{ color: "var(--fg-muted)" }}
        >
          Loading...
        </p>
      ) : keys.length === 0 ? (
        <div
          className="p-8 text-center"
          style={{
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-lg)",
          }}
        >
          <p className="text-[14px]" style={{ color: "var(--fg-muted)" }}>
            No API keys yet. Create one above.
          </p>
        </div>
      ) : (
        <div
          className="overflow-hidden"
          style={{
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-lg)",
          }}
        >
          <table className="w-full text-[13px]">
            <thead>
              <tr
                className="text-left font-mono text-[10px] uppercase"
                style={{
                  background: "var(--bg-elev)",
                  color: "var(--fg-subtle)",
                  letterSpacing: "0.08em",
                }}
              >
                <th className="px-4 py-2.5 font-semibold">Name</th>
                <th className="px-4 py-2.5 font-semibold">Prefix</th>
                <th className="px-4 py-2.5 font-semibold">Created</th>
                <th className="px-4 py-2.5 font-semibold">Last used</th>
                <th className="px-4 py-2.5 font-semibold"></th>
              </tr>
            </thead>
            <tbody>
              {keys.map((k, i) => (
                <tr
                  key={k.id}
                  style={{
                    borderTop:
                      i > 0 ? "1px solid var(--border-sub)" : "none",
                  }}
                >
                  <td className="px-4 py-3" style={{ color: "var(--fg)" }}>
                    {k.name || (
                      <span style={{ color: "var(--fg-subtle)" }}>Unnamed</span>
                    )}
                  </td>
                  <td className="px-4 py-3">
                    <Chip mono>{k.key_prefix}...</Chip>
                  </td>
                  <td
                    className="px-4 py-3 font-mono text-[12px]"
                    style={{ color: "var(--fg-muted)" }}
                  >
                    {new Date(k.created_at).toLocaleDateString()}
                  </td>
                  <td
                    className="px-4 py-3 font-mono text-[12px]"
                    style={{ color: "var(--fg-muted)" }}
                  >
                    {k.last_used_at ? (
                      formatRelative(k.last_used_at)
                    ) : (
                      <span style={{ color: "var(--fg-subtle)" }}>Never</span>
                    )}
                  </td>
                  <td className="px-4 py-3 text-right">
                    <button
                      onClick={() => handleDelete(k.id)}
                      className="text-[12px] transition"
                      style={{ color: "var(--danger-strong)" }}
                    >
                      Revoke
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </PageShell>
  );
}
