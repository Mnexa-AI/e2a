"use client";

import { useEffect, useState } from "react";
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
      subtitle="Account profile, data export, and account deletion."
      maxWidth={920}
    >
      <div className="space-y-12">
        <ProfileSection user={user} />
        <UsageSection />
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
