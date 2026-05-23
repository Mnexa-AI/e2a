"use client";

import { useCallback, useEffect, useState } from "react";
import { useAuth } from "../../components/AuthProvider";
import { PageShell } from "../../components/loft/PageShell";
import type { DashboardStats } from "../../components/types";

export default function SettingsPage() {
  const { user } = useAuth();

  if (!user) return null;

  return (
    <PageShell
      crumbs={["Settings"]}
      eyebrow="Account"
      title={<>Settings</>}
      subtitle="Account profile, webhook signing secrets, data export, and account deletion."
      maxWidth={920}
    >
      <div className="space-y-12">
        <ProfileSection user={user} />
        <UsageSection />
        <SigningSecretsSection />
        <ExportSection />
        <NotificationsSection />
        <DangerZone />
      </div>
    </PageShell>
  );
}

function SectionHeading({
  title,
  subtitle,
  tone = "default",
}: {
  title: string;
  subtitle?: React.ReactNode;
  tone?: "default" | "danger";
}) {
  return (
    <div className="mb-4">
      <h2
        className="mb-1"
        style={{
          fontFamily: "var(--f-editorial)",
          fontWeight: 400,
          fontSize: 26,
          letterSpacing: "-0.01em",
          color: tone === "danger" ? "var(--danger-strong)" : "var(--fg)",
        }}
      >
        {title}
      </h2>
      {subtitle && (
        <p
          className="text-[13px] leading-[1.6] max-w-2xl"
          style={{ color: "var(--fg-muted)" }}
        >
          {subtitle}
        </p>
      )}
    </div>
  );
}

function ProfileSection({
  user,
}: {
  user: { id: string; email: string; name: string; created_at: string };
}) {
  const { setUser } = useAuth();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(user.name);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const handleSave = async () => {
    setError("");
    setSaving(true);
    try {
      const res = await fetch("/api/auth/me", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ name: draft }),
      });
      if (!res.ok) {
        setError((await res.text()) || `Failed (${res.status})`);
        setSaving(false);
        return;
      }
      const updated = await res.json();
      setUser(updated);
      setEditing(false);
    } catch {
      setError("Network error");
    } finally {
      setSaving(false);
    }
  };

  return (
    <section>
      <SectionHeading title="Profile" />
      <div
        className="p-5"
        style={{
          background: "var(--bg-panel)",
          border: "1px solid var(--border)",
          borderRadius: "var(--r-lg)",
        }}
      >
        <dl className="grid grid-cols-[140px_1fr] gap-y-3 gap-x-6 text-[13px]">
          <dt style={{ color: "var(--fg-muted)" }}>Name</dt>
          <dd className="flex items-center gap-2 flex-wrap" style={{ color: "var(--fg)" }}>
            {editing ? (
              <>
                <input
                  type="text"
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  disabled={saving}
                  maxLength={80}
                  className="text-[13px] px-2 py-1"
                  style={{
                    background: "var(--bg-elev)",
                    border: "1px solid var(--border)",
                    borderRadius: "var(--r-sm)",
                    color: "var(--fg)",
                    minWidth: 200,
                  }}
                />
                <button
                  type="button"
                  onClick={handleSave}
                  disabled={saving || draft.trim().length === 0 || draft !== draft.trim()}
                  className="text-[11px] px-2 py-0.5 disabled:opacity-50 disabled:cursor-not-allowed"
                  style={{
                    color: "var(--accent-fg)",
                    background: "var(--accent-fill)",
                    border: "1px solid var(--accent-fill)",
                    borderRadius: "var(--r-sm)",
                  }}
                >
                  {saving ? "Saving…" : "Save"}
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setEditing(false);
                    setDraft(user.name);
                    setError("");
                  }}
                  disabled={saving}
                  className="text-[11px] px-2 py-0.5"
                  style={{
                    color: "var(--fg-muted)",
                    border: "1px solid var(--border-sub)",
                    background: "var(--bg-elev)",
                    borderRadius: "var(--r-sm)",
                  }}
                >
                  Cancel
                </button>
                {error && (
                  <span className="text-[11px]" style={{ color: "var(--danger-strong)" }}>
                    {error}
                  </span>
                )}
              </>
            ) : (
              <>
                <span>{user.name || "—"}</span>
                <button
                  type="button"
                  onClick={() => {
                    setDraft(user.name);
                    setEditing(true);
                  }}
                  className="text-[11px] px-2 py-0.5"
                  style={{
                    color: "var(--fg-muted)",
                    border: "1px solid var(--border-sub)",
                    background: "var(--bg-elev)",
                    borderRadius: "var(--r-sm)",
                  }}
                >
                  Edit
                </button>
              </>
            )}
          </dd>
          <dt style={{ color: "var(--fg-muted)" }}>Email</dt>
          <dd style={{ color: "var(--fg)" }}>{user.email}</dd>
          <dt style={{ color: "var(--fg-muted)" }}>User ID</dt>
          <dd className="font-mono text-[12px]" style={{ color: "var(--fg)" }}>
            {user.id}
          </dd>
          <dt style={{ color: "var(--fg-muted)" }}>Member since</dt>
          <dd style={{ color: "var(--fg)" }}>{formatDate(user.created_at)}</dd>
        </dl>
      </div>
    </section>
  );
}

function UsageSection() {
  // Pulls a 30-day window from the same /api/dashboard/stats endpoint
  // the dashboard strip uses. Backend computes inbound_window /
  // outbound_window over the requested window, plus delivery success
  // % over that same span. Pending count is always current (not
  // window-scoped). Null state during fetch renders as "—".
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let cancelled = false;
    fetch("/api/dashboard/stats?window=30")
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => {
        if (!cancelled) setStats(data);
      })
      .catch(() => {
        // Don't crash the page — null state shows "—".
      })
      .finally(() => {
        if (!cancelled) setLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const days = stats?.sample_window_days ?? 30;
  const cards: { label: string; value: string }[] = [
    {
      label: `Inbound · ${days}d`,
      value: stats ? String(stats.inbound_window) : loaded ? "0" : "—",
    },
    {
      label: `Outbound · ${days}d`,
      value: stats ? String(stats.outbound_window) : loaded ? "0" : "—",
    },
    {
      label: "Pending",
      value: stats ? String(stats.pending.count) : loaded ? "0" : "—",
    },
    {
      label: "Delivery success",
      value:
        stats && stats.delivery_success_pct > 0
          ? `${stats.delivery_success_pct}%`
          : "—",
    },
  ];

  return (
    <section>
      <SectionHeading
        title="Usage"
        subtitle="Inbound + outbound counts over the last 30 days. Pending shows messages currently waiting on HITL approval; delivery success is the share of webhook deliveries that finalised successfully."
      />
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        {cards.map((s) => (
          <div
            key={s.label}
            className="px-4 py-3.5"
            style={{
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-lg)",
            }}
          >
            <div
              className="font-mono text-[11px] font-semibold uppercase mb-1.5"
              style={{
                color: "var(--fg-subtle)",
                letterSpacing: "0.08em",
              }}
            >
              {s.label}
            </div>
            <div
              className="text-[22px] font-semibold"
              style={{
                color: "var(--fg)",
                letterSpacing: "-0.01em",
                lineHeight: 1.1,
              }}
            >
              {s.value}
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}

function NotificationsSection() {
  // Coming soon — BACKEND_TODO #12 (notification_prefs table + dispatch worker).
  return (
    <section>
      <SectionHeading
        title="Notifications"
        subtitle="Choose when e2a emails you. Coming soon."
      />
      <div
        className="p-5 space-y-3"
        style={{
          background: "var(--bg-panel)",
          border: "1px solid var(--border)",
          borderRadius: "var(--r-lg)",
        }}
      >
        {[
          "Email me when a message lands in pending review",
          "Email me when a domain finishes verifying",
          "Weekly delivery digest",
        ].map((label) => (
          <label
            key={label}
            className="flex items-center justify-between text-[13px]"
            style={{ color: "var(--fg-muted)" }}
          >
            <span>{label}</span>
            <span
              className="font-mono text-[10px] uppercase"
              style={{
                color: "var(--fg-subtle)",
                letterSpacing: "0.08em",
              }}
            >
              Coming soon
            </span>
          </label>
        ))}
      </div>
    </section>
  );
}

function ExportSection() {
  return (
    <section>
      <SectionHeading
        title="Your data"
        subtitle="Download a JSON dump of everything we store about you: profile, agents, domains, API key metadata, all messages with bodies, and usage events. Internal identifiers (Google subject, key hashes, session tokens) are excluded. Right of access — GDPR Article 15 / CCPA equivalent."
      />
      <a
        href="/api/v1/users/me/export"
        className="inline-flex items-center gap-2 px-4 py-2 text-[13px] font-medium transition"
        style={{
          background: "var(--fg)",
          color: "var(--bg)",
          borderRadius: "var(--r-md)",
        }}
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
          <polyline points="7 10 12 15 17 10" />
          <line x1="12" y1="15" x2="12" y2="3" />
        </svg>
        Download export
      </a>
    </section>
  );
}

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

function SigningSecretsSection() {
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
    <section>
      <SectionHeading
        title="Webhook signing secrets"
        subtitle={
          <>
            HMAC secrets used to sign your agents&apos; inbound webhook
            payloads. Pass any of these to{" "}
            <code
              className="font-mono text-[12px] px-1.5 py-0.5"
              style={{
                background: "var(--bg-elev)",
                border: "1px solid var(--border-sub)",
                borderRadius: "var(--r-sm)",
                color: "var(--fg)",
              }}
            >
              verify_signature()
            </code>{" "}
            in the SDK to confirm a payload came from e2a. The relay always
            signs with your most recently created secret; older ones stay
            valid for verification until you delete them, so rotation is:
            create new → swap in your code → delete old. Up to 5 active
            secrets at a time. Click <strong>Show</strong> below to reveal
            the full secret at any time.
          </>
        }
      />

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
              <span
                className="text-[13px]"
                style={{ color: "var(--fg)" }}
              >
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
    </section>
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

type DeleteState = "idle" | "deleting" | "error";

function DangerZone() {
  const [open, setOpen] = useState(false);
  const [confirmText, setConfirmText] = useState("");
  const [state, setState] = useState<DeleteState>("idle");
  const [errorMessage, setErrorMessage] = useState("");

  const ready = confirmText === "DELETE";

  const handleDelete = async () => {
    if (!ready) return;
    setState("deleting");
    setErrorMessage("");
    try {
      const res = await fetch("/api/v1/users/me?confirm=DELETE", {
        method: "DELETE",
        credentials: "include",
      });
      if (!res.ok) {
        const text = await res.text().catch(() => `HTTP ${res.status}`);
        setState("error");
        setErrorMessage(text.trim());
        return;
      }
      window.location.href = "/?account_deleted=1";
    } catch (err) {
      setState("error");
      setErrorMessage(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <section>
      <SectionHeading title="Danger zone" tone="danger" />
      <div
        className="p-5"
        style={{
          background: "var(--bg-panel)",
          border: "1px solid var(--danger-bg)",
          borderRadius: "var(--r-lg)",
        }}
      >
        <h3
          className="text-[14px] font-semibold mb-1"
          style={{ color: "var(--fg)" }}
        >
          Delete account
        </h3>
        <p
          className="mb-4 max-w-2xl text-[13px] leading-[1.6]"
          style={{ color: "var(--fg-muted)" }}
        >
          Permanently delete your account along with all your agents, domains,
          messages, API keys, and sessions, in a single Postgres transaction.{" "}
          <strong style={{ color: "var(--fg)" }}>This is irreversible.</strong>{" "}
          Right of deletion — GDPR Article 17 / CCPA &quot;Do Not Sell or
          Share&quot;.
        </p>
        {!open ? (
          <button
            onClick={() => setOpen(true)}
            className="px-4 py-2 text-[13px] font-medium transition"
            style={{
              background: "var(--bg-panel)",
              color: "var(--danger-strong)",
              border: "1px solid var(--danger-bg)",
              borderRadius: "var(--r-md)",
            }}
          >
            Delete account…
          </button>
        ) : (
          <div className="space-y-3">
            <label className="block">
              <span className="text-[13px]" style={{ color: "var(--fg)" }}>
                Type{" "}
                <code
                  className="font-mono text-[12px] px-1.5 py-0.5"
                  style={{
                    background: "var(--bg-elev)",
                    border: "1px solid var(--border-sub)",
                    borderRadius: "var(--r-sm)",
                    color: "var(--fg)",
                  }}
                >
                  DELETE
                </code>{" "}
                to confirm:
              </span>
              <input
                autoFocus
                type="text"
                value={confirmText}
                onChange={(e) => setConfirmText(e.target.value)}
                placeholder="DELETE"
                className="mt-1 w-full max-w-xs px-3 py-2 text-[13px] font-mono"
                style={{
                  background: "var(--bg-panel)",
                  border: "1px solid var(--border)",
                  borderRadius: "var(--r-md)",
                  color: "var(--fg)",
                }}
              />
            </label>
            {state === "error" && (
              <p
                className="text-[13px]"
                style={{ color: "var(--danger-strong)" }}
              >
                Failed: {errorMessage || "unknown error"}
              </p>
            )}
            <div className="flex gap-2">
              <button
                onClick={handleDelete}
                disabled={!ready || state === "deleting"}
                className="px-4 py-2 text-[13px] font-medium transition disabled:opacity-50 disabled:cursor-not-allowed"
                style={{
                  background: "var(--danger)",
                  color: "#fff",
                  borderRadius: "var(--r-md)",
                }}
              >
                {state === "deleting" ? "Deleting…" : "Delete my account"}
              </button>
              <button
                onClick={() => {
                  setOpen(false);
                  setConfirmText("");
                  setState("idle");
                  setErrorMessage("");
                }}
                disabled={state === "deleting"}
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
    </section>
  );
}

function formatDate(iso: string): string {
  try {
    return new Intl.DateTimeFormat(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
    }).format(new Date(iso));
  } catch {
    return iso;
  }
}
