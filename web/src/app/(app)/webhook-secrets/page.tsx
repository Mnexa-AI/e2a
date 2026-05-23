"use client";

import { useCallback, useEffect, useState } from "react";
import { PageShell } from "../../components/loft/PageShell";

// /webhook-secrets owns the user's HMAC signing-secret lifecycle —
// create, reveal, rotate, delete. Lifted out of /settings into a
// dedicated nav entry so it sits alongside "API keys" in the sidebar
// (both are credential surfaces; surfacing them at the same depth
// makes them easier to find).

// Inline-code chip used inline in the subtitle copy. Pulled out so
// the two usages in the page header stay byte-identical.
const inlineCodeStyle: React.CSSProperties = {
  fontFamily: "var(--f-mono)",
  fontSize: 12,
  padding: "1px 6px",
  background: "var(--bg-elev)",
  border: "1px solid var(--border-sub)",
  borderRadius: "var(--r-sm)",
  color: "var(--fg)",
};

type SigningSecretSummary = {
  id: string;
  name: string;
  secret: string;
  secret_prefix: string;
  created_at: string;
  last_signed_at?: string;
};

type CreatedSecret = {
  id: string;
  name: string;
  secret: string;
  secret_prefix: string;
  created_at: string;
};

export default function WebhookSecretsPage() {
  const [secrets, setSecrets] = useState<SigningSecretSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [created, setCreated] = useState<CreatedSecret | null>(null);
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState("");
  const [newName, setNewName] = useState("");
  const [showCreateForm, setShowCreateForm] = useState(false);

  const refresh = useCallback(async () => {
    setLoading(true);
    setLoadError("");
    try {
      const res = await fetch("/api/v1/users/me/signing-secrets", {
        credentials: "include",
      });
      if (!res.ok) {
        setLoadError(`Failed to load (HTTP ${res.status})`);
        setLoading(false);
        return;
      }
      const body = await res.json();
      setSecrets(body.secrets ?? []);
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const handleCreate = async () => {
    setCreating(true);
    setCreateError("");
    try {
      const res = await fetch("/api/v1/users/me/signing-secrets", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: newName.trim() }),
      });
      if (!res.ok) {
        const text = await res.text().catch(() => `HTTP ${res.status}`);
        setCreateError(text.trim() || `HTTP ${res.status}`);
        return;
      }
      const body: CreatedSecret = await res.json();
      setCreated(body);
      setShowCreateForm(false);
      setNewName("");
      await refresh();
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : String(err));
    } finally {
      setCreating(false);
    }
  };

  return (
    <PageShell
      crumbs={["Webhook secrets"]}
      eyebrow="Workspace"
      title={<>Webhook secrets</>}
      subtitle={
        <>
          <span style={{ display: "block" }}>
            <strong style={{ color: "var(--fg)" }}>For cloud agents only.</strong>{" "}
            When your agent runs behind a webhook
            (<code style={inlineCodeStyle}>agent_mode=cloud</code>), e2a
            HMAC-signs every inbound payload it POSTs to you so your
            handler can confirm the request really came from e2a.
          </span>
          <span style={{ display: "block", marginTop: 10 }}>
            Pass any active secret to{" "}
            <code style={inlineCodeStyle}>verify_signature()</code> in the
            SDK to validate. The relay always signs with your most
            recently created secret; older ones remain valid for
            verification until you delete them — so rotation is: create
            new → swap in your code → delete old. Up to 5 active at a
            time.
          </span>
          <span
            style={{
              display: "block",
              marginTop: 10,
              color: "var(--fg-subtle)",
              fontSize: 12,
            }}
          >
            Local-mode agents pull messages via WebSocket and don&apos;t
            need this — the WS auth handshake covers it.
          </span>
        </>
      }
    >
      {created && (
        <CreatedSecretBanner
          secret={created}
          onDismiss={() => setCreated(null)}
        />
      )}

      {loading ? (
        <p className="text-[13px]" style={{ color: "var(--fg-muted)" }}>
          Loading…
        </p>
      ) : loadError ? (
        <p className="text-[13px]" style={{ color: "var(--danger-strong)" }}>
          {loadError}
        </p>
      ) : (
        <SigningSecretsTable secrets={secrets} onChange={refresh} />
      )}

      <div className="mt-4">
        {!showCreateForm ? (
          <button
            onClick={() => {
              setShowCreateForm(true);
              setCreateError("");
            }}
            className="px-4 py-2 text-[13px] font-medium transition disabled:opacity-50"
            style={{
              background: "var(--accent-fill)",
              color: "var(--accent-fg)",
              borderRadius: "var(--r-md)",
            }}
            disabled={!loadError && secrets.length >= 5}
            title={
              !loadError && secrets.length >= 5
                ? "Cap reached — delete one first"
                : undefined
            }
          >
            Create new secret
          </button>
        ) : (
          <div className="space-y-2 max-w-md">
            <label className="block">
              <span className="text-[13px]" style={{ color: "var(--fg)" }}>
                Name (optional, e.g.{" "}
                <code
                  className="font-mono text-[12px] px-1 py-0.5"
                  style={{
                    background: "var(--bg-elev)",
                    border: "1px solid var(--border-sub)",
                    borderRadius: "var(--r-sm)",
                    color: "var(--fg)",
                  }}
                >
                  prod
                </code>
                )
              </span>
              <input
                autoFocus
                type="text"
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder="rolling-2026-04"
                className="mt-1 w-full px-3 py-2 text-[13px]"
                style={{
                  background: "var(--bg-panel)",
                  border: "1px solid var(--border)",
                  borderRadius: "var(--r-md)",
                  color: "var(--fg)",
                }}
              />
            </label>
            {createError && (
              <p
                className="text-[13px]"
                style={{ color: "var(--danger-strong)" }}
              >
                {createError}
              </p>
            )}
            <div className="flex gap-2">
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
                {creating ? "Creating…" : "Create"}
              </button>
              <button
                onClick={() => {
                  setShowCreateForm(false);
                  setNewName("");
                  setCreateError("");
                }}
                disabled={creating}
                className="px-4 py-2 text-[13px] transition"
                style={{
                  background: "var(--bg-panel)",
                  color: "var(--fg)",
                  border: "1px solid var(--border)",
                  borderRadius: "var(--r-md)",
                }}
              >
                Cancel
              </button>
            </div>
          </div>
        )}
      </div>
    </PageShell>
  );
}

function CreatedSecretBanner({
  secret,
  onDismiss,
}: {
  secret: CreatedSecret;
  onDismiss: () => void;
}) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(secret.secret);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // No clipboard in test env — silently noop.
    }
  };

  return (
    <div
      className="mb-4 p-4"
      style={{
        background: "var(--warn-bg)",
        border: "1px solid var(--warn-bg)",
        borderRadius: "var(--r-md)",
      }}
    >
      <h3
        className="text-[14px] font-semibold mb-1"
        style={{ color: "var(--warn-strong)" }}
      >
        New signing secret created
      </h3>
      <p className="text-[13px] mb-3" style={{ color: "var(--warn-strong)" }}>
        Copy it into your environment variable (commonly{" "}
        <code
          className="font-mono text-[12px] px-1 py-0.5"
          style={{
            background: "var(--bg-panel)",
            color: "var(--fg)",
            borderRadius: "var(--r-sm)",
          }}
        >
          E2A_HMAC_SECRET
        </code>
        ). You can also reveal it later from the table below.
      </p>
      <div className="flex gap-2 items-start flex-wrap">
        <input
          readOnly
          value={secret.secret}
          aria-label="Plaintext signing secret"
          onFocus={(e) => e.currentTarget.select()}
          className="flex-1 min-w-[200px] font-mono text-[12px] px-3 py-2"
          style={{
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-md)",
            color: "var(--fg)",
          }}
        />
        <button
          onClick={copy}
          className="px-3 py-2 text-[13px] whitespace-nowrap transition"
          style={{
            background: "var(--bg-panel)",
            color: "var(--fg)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-md)",
          }}
        >
          {copied ? "Copied" : "Copy"}
        </button>
        <button
          onClick={onDismiss}
          className="px-3 py-2 text-[13px] transition"
          style={{
            background: "var(--bg-panel)",
            color: "var(--fg)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-md)",
          }}
        >
          Dismiss
        </button>
      </div>
    </div>
  );
}

function SigningSecretsTable({
  secrets,
  onChange,
}: {
  secrets: SigningSecretSummary[];
  onChange: () => Promise<void> | void;
}) {
  if (secrets.length === 0) {
    return (
      <p className="text-[13px]" style={{ color: "var(--fg-muted)" }}>
        No secrets yet. New accounts always get a default one — if you&apos;re
        seeing this, something is wrong.
      </p>
    );
  }
  return (
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
            <th className="px-4 py-2 font-semibold">Name</th>
            <th className="px-4 py-2 font-semibold">Secret</th>
            <th className="px-4 py-2 font-semibold">Created</th>
            <th className="px-4 py-2 font-semibold">Last used</th>
            <th className="px-4 py-2"></th>
          </tr>
        </thead>
        <tbody>
          {secrets.map((s, i) => (
            <SigningSecretRow
              key={s.id}
              secret={s}
              isLast={secrets.length === 1}
              onChange={onChange}
              isFirstRow={i === 0}
            />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SigningSecretRow({
  secret,
  isLast,
  onChange,
  isFirstRow,
}: {
  secret: SigningSecretSummary;
  isLast: boolean;
  onChange: () => Promise<void> | void;
  isFirstRow: boolean;
}) {
  const [confirming, setConfirming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState("");
  const [revealed, setRevealed] = useState(false);
  const [copied, setCopied] = useState(false);

  const handleDelete = async () => {
    setDeleting(true);
    setError("");
    try {
      const res = await fetch(
        `/api/v1/users/me/signing-secrets/${encodeURIComponent(secret.id)}`,
        {
          method: "DELETE",
          credentials: "include",
        },
      );
      if (!res.ok) {
        const text = await res.text().catch(() => `HTTP ${res.status}`);
        setError(text.trim() || `HTTP ${res.status}`);
        return;
      }
      await onChange();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setDeleting(false);
      setConfirming(false);
    }
  };

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(secret.secret);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // No clipboard in test env — silently noop.
    }
  };

  return (
    <tr
      style={{
        borderTop: isFirstRow ? undefined : "1px solid var(--border-sub)",
      }}
    >
      <td className="px-4 py-3" style={{ color: "var(--fg)" }}>
        {secret.name || "—"}
      </td>
      <td className="px-4 py-3 font-mono text-[12px]">
        <div className="flex items-center gap-2">
          <span className="break-all" style={{ color: "var(--fg)" }}>
            {revealed ? secret.secret : `${secret.secret_prefix}…`}
          </span>
          <button
            type="button"
            onClick={() => setRevealed((v) => !v)}
            className="px-2 py-1 text-[11px] transition shrink-0"
            style={{
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              color: "var(--fg)",
              borderRadius: "var(--r-sm)",
            }}
          >
            {revealed ? "Hide" : "Show"}
          </button>
          {revealed && (
            <button
              type="button"
              onClick={handleCopy}
              className="px-2 py-1 text-[11px] transition shrink-0"
              style={{
                background: "var(--bg-panel)",
                border: "1px solid var(--border)",
                color: "var(--fg)",
                borderRadius: "var(--r-sm)",
              }}
            >
              {copied ? "Copied" : "Copy"}
            </button>
          )}
        </div>
      </td>
      <td
        className="px-4 py-3 font-mono text-[12px]"
        style={{ color: "var(--fg-muted)" }}
      >
        {formatDate(secret.created_at)}
      </td>
      <td
        className="px-4 py-3 font-mono text-[12px]"
        style={{ color: "var(--fg-muted)" }}
      >
        {secret.last_signed_at ? formatRelative(secret.last_signed_at) : "Never"}
      </td>
      <td className="px-4 py-3 text-right">
        {error && (
          <span
            className="text-[11px] mr-2"
            style={{ color: "var(--danger-strong)" }}
          >
            {error}
          </span>
        )}
        {confirming ? (
          <span className="inline-flex gap-1">
            <button
              onClick={handleDelete}
              disabled={deleting}
              className="px-2 py-1 text-[11px] transition disabled:opacity-50"
              style={{
                background: "var(--danger)",
                color: "#fff",
                borderRadius: "var(--r-sm)",
              }}
            >
              {deleting ? "Deleting…" : "Confirm"}
            </button>
            <button
              onClick={() => {
                setConfirming(false);
                setError("");
              }}
              disabled={deleting}
              className="px-2 py-1 text-[11px] transition"
              style={{
                background: "var(--bg-panel)",
                border: "1px solid var(--border)",
                color: "var(--fg)",
                borderRadius: "var(--r-sm)",
              }}
            >
              Cancel
            </button>
          </span>
        ) : (
          <button
            onClick={() => setConfirming(true)}
            disabled={isLast}
            title={
              isLast
                ? "Cannot delete your only signing secret — create a new one first"
                : undefined
            }
            className="px-2 py-1 text-[11px] transition disabled:opacity-50 disabled:cursor-not-allowed"
            style={{
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              color: "var(--fg)",
              borderRadius: "var(--r-sm)",
            }}
          >
            Delete
          </button>
        )}
      </td>
    </tr>
  );
}

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleDateString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
    });
  } catch {
    return iso;
  }
}

function formatRelative(iso: string): string {
  try {
    const ts = new Date(iso).getTime();
    const ageSec = (Date.now() - ts) / 1000;
    if (ageSec < 60) return "just now";
    if (ageSec < 3600) return `${Math.floor(ageSec / 60)}m ago`;
    if (ageSec < 86400) return `${Math.floor(ageSec / 3600)}h ago`;
    return `${Math.floor(ageSec / 86400)}d ago`;
  } catch {
    return iso;
  }
}
