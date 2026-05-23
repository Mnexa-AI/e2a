"use client";

import { useState, useEffect, useCallback, useMemo } from "react";
import type { APIKeyData } from "../../components/types";
import { PageShell } from "../../components/loft/PageShell";
import { Chip } from "../../components/loft/Chip";

type SortKey = "last_used" | "created" | "name";

function isExpired(k: APIKeyData): boolean {
  return !!k.expires_at && new Date(k.expires_at).getTime() < Date.now();
}

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

// formatExpiresIn renders the "in 28d" / "tomorrow" / "today" forms
// for the Expires column. Past timestamps return "expired" so the
// caller can tint the cell red.
function formatExpiresIn(iso: string): { label: string; expired: boolean; imminent: boolean } {
  const diff = new Date(iso).getTime() - Date.now();
  if (isNaN(diff)) return { label: "—", expired: false, imminent: false };
  if (diff <= 0) return { label: "expired", expired: true, imminent: false };
  const days = Math.floor(diff / (24 * 60 * 60 * 1000));
  if (days === 0) {
    const hr = Math.floor(diff / (60 * 60 * 1000));
    return { label: hr <= 1 ? "<1h" : `in ${hr}h`, expired: false, imminent: true };
  }
  if (days === 1) return { label: "tomorrow", expired: false, imminent: true };
  if (days < 30) return { label: `in ${days}d`, expired: false, imminent: days <= 7 };
  return {
    label: new Date(iso).toLocaleDateString(),
    expired: false,
    imminent: false,
  };
}

export default function APIKeysPage() {
  const [keys, setKeys] = useState<APIKeyData[]>([]);
  const [loading, setLoading] = useState(true);
  const [newKeyName, setNewKeyName] = useState("");
  const [creating, setCreating] = useState(false);
  const [createdKey, setCreatedKey] = useState<APIKeyData | null>(null);
  const [sort, setSort] = useState<SortKey>("last_used");
  // expiresIn: "never" or a number of days from now. The backend
  // accepts an RFC 3339 timestamp on POST /api/keys; we compute the
  // absolute time from this relative choice at submit. Custom dates
  // are deferred — the four presets cover the bulk of real workflows.
  const [expiresIn, setExpiresIn] = useState<"never" | "30" | "90" | "365">(
    "never",
  );

  const sortedKeys = useMemo(() => {
    const arr = [...keys];
    if (sort === "name") {
      arr.sort((a, b) => (a.name || "").localeCompare(b.name || ""));
    } else if (sort === "created") {
      arr.sort(
        (a, b) =>
          new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
      );
    } else {
      // last_used — NULL last_used_at sorts to the end (treated as -Infinity)
      arr.sort((a, b) => {
        const ta = a.last_used_at ? new Date(a.last_used_at).getTime() : 0;
        const tb = b.last_used_at ? new Date(b.last_used_at).getTime() : 0;
        return tb - ta;
      });
    }
    return arr;
  }, [keys, sort]);

  // Stats line: "N keys · M expired · P active". Renders nothing while the
  // list is empty (the empty-state below covers that).
  const totals = useMemo(() => {
    const expired = keys.filter(isExpired).length;
    return {
      total: keys.length,
      expired,
      active: keys.length - expired,
    };
  }, [keys]);

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
      const body: { name: string; expires_at?: string } = {
        name: newKeyName || "Default",
      };
      if (expiresIn !== "never") {
        const days = Number(expiresIn);
        const exp = new Date(Date.now() + days * 24 * 60 * 60 * 1000);
        body.expires_at = exp.toISOString();
      }
      const res = await fetch("/api/keys", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (res.ok) {
        const key = await res.json();
        setCreatedKey(key);
        setNewKeyName("");
        setExpiresIn("never");
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

      {/* Create form. Stacks vertically on phones (single column),
          breaks into a row at md+ where name takes the leftover space
          and the select + button trail. */}
      <div className="flex flex-col md:flex-row md:items-end gap-3 mb-6">
        <div className="md:flex-1 md:min-w-[200px]">
          <label
            htmlFor="apikey-name"
            className="block text-[12px] font-medium mb-1"
            style={{ color: "var(--fg-muted)" }}
          >
            Key name (optional)
          </label>
          <input
            id="apikey-name"
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
        <div className="md:min-w-[140px]">
          <label
            htmlFor="apikey-expires"
            className="block text-[12px] font-medium mb-1"
            style={{ color: "var(--fg-muted)" }}
          >
            Expires
          </label>
          <select
            id="apikey-expires"
            value={expiresIn}
            onChange={(e) =>
              setExpiresIn(
                e.target.value as "never" | "30" | "90" | "365",
              )
            }
            className="w-full px-3 py-2 text-[13px] cursor-pointer"
            style={{
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-md)",
              color: "var(--fg)",
            }}
          >
            <option value="never">Never</option>
            <option value="30">In 30 days</option>
            <option value="90">In 90 days</option>
            <option value="365">In 1 year</option>
          </select>
        </div>
        <button
          onClick={handleCreate}
          disabled={creating}
          className="w-full md:w-auto px-4 py-2 text-[13px] font-medium transition disabled:opacity-50"
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
        <>
          <div className="flex items-center gap-3 mb-3 flex-wrap">
            <p
              className="font-mono text-[11px]"
              style={{ color: "var(--fg-subtle)", letterSpacing: "0.02em" }}
            >
              {totals.total} {totals.total === 1 ? "key" : "keys"}
              {totals.expired > 0 && ` · ${totals.expired} expired`}
              {totals.active !== totals.total && ` · ${totals.active} active`}
            </p>
            <span className="flex-1" />
            <label
              className="font-mono text-[11px] flex items-center gap-1.5"
              style={{ color: "var(--fg-subtle)", letterSpacing: "0.02em" }}
            >
              Sort:
              <select
                value={sort}
                onChange={(e) => setSort(e.target.value as SortKey)}
                className="font-mono text-[11px] bg-transparent border-none cursor-pointer"
                style={{ color: "var(--fg-muted)" }}
              >
                <option value="last_used">last used ▾</option>
                <option value="created">created ▾</option>
                <option value="name">name ▾</option>
              </select>
            </label>
          </div>
          <div
            className="overflow-x-auto"
            style={{
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-lg)",
            }}
          >
            <table className="w-full text-[13px] min-w-[640px]">
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
                  <th className="px-4 py-2.5 font-semibold">Expires</th>
                  <th className="px-4 py-2.5 font-semibold"></th>
                </tr>
              </thead>
              <tbody>
                {sortedKeys.map((k, i) => (
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
                  <td className="px-4 py-3 font-mono text-[12px]">
                    {k.expires_at ? (
                      (() => {
                        const exp = formatExpiresIn(k.expires_at);
                        return (
                          <span
                            style={{
                              color: exp.expired
                                ? "var(--danger-strong)"
                                : exp.imminent
                                  ? "var(--warn-strong)"
                                  : "var(--fg-muted)",
                              fontWeight: exp.expired || exp.imminent ? 500 : 400,
                            }}
                            title={new Date(k.expires_at).toLocaleString()}
                          >
                            {exp.label}
                          </span>
                        );
                      })()
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
        </>
      )}
    </PageShell>
  );
}
