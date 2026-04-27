"use client";

import { useCallback, useEffect, useState } from "react";
import { useAuth } from "../../components/AuthProvider";

export default function SettingsPage() {
  const { user } = useAuth();

  // Auth provider gates the (app) layout above us — by the time this
  // renders we always have a user. The narrow check just keeps the
  // type system happy.
  if (!user) return null;

  return (
    <div className="space-y-12">
      <header>
        <h1 className="text-2xl font-bold tracking-tight mb-1">Settings</h1>
        <p className="text-sm text-muted">
          Account profile, webhook signing secrets, data export, and account deletion.
        </p>
      </header>

      <ProfileSection user={user} />
      <SigningSecretsSection />
      <ExportSection />
      <DangerZone />
    </div>
  );
}

function ProfileSection({
  user,
}: {
  user: { id: string; email: string; name: string; created_at: string };
}) {
  return (
    <section>
      <h2 className="text-lg font-semibold mb-4">Profile</h2>
      <dl className="grid grid-cols-[140px_1fr] gap-y-3 gap-x-6 text-sm">
        <dt className="text-muted">Name</dt>
        <dd>{user.name || "—"}</dd>
        <dt className="text-muted">Email</dt>
        <dd>{user.email}</dd>
        <dt className="text-muted">User ID</dt>
        <dd className="font-mono text-xs">{user.id}</dd>
        <dt className="text-muted">Member since</dt>
        <dd>{formatDate(user.created_at)}</dd>
      </dl>
    </section>
  );
}

function ExportSection() {
  // Browser does the heavy lifting: the API sets Content-Disposition:
  // attachment, so a same-origin GET (cookie auth flows automatically)
  // triggers a download with the right filename. No client-side blob
  // juggling needed for large mailboxes.
  return (
    <section>
      <h2 className="text-lg font-semibold mb-2">Your data</h2>
      <p className="text-sm text-muted mb-4 max-w-2xl">
        Download a JSON dump of everything we store about you: profile, agents,
        domains, API key metadata, all messages with bodies, and usage events.
        Internal identifiers (Google subject, key hashes, session tokens) are
        excluded. Right of access — GDPR Article 15 / CCPA equivalent.
      </p>
      <a
        href="/api/v1/users/me/export"
        className="inline-flex items-center gap-2 px-4 py-2 bg-foreground text-background rounded-lg text-sm font-medium hover:opacity-90 transition"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
          <polyline points="7 10 12 15 17 10" />
          <line x1="12" y1="15" x2="12" y2="3" />
        </svg>
        Download export
      </a>
    </section>
  );
}

// --- Signing secrets ---

type SigningSecretSummary = {
  id: string;
  name: string;
  secret_prefix: string;
  created_at: string;
  last_signed_at?: string;
};

type CreatedSecret = {
  id: string;
  name: string;
  secret: string;        // plaintext, only shown once
  secret_prefix: string;
  created_at: string;
};

function SigningSecretsSection() {
  const [secrets, setSecrets] = useState<SigningSecretSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  // The freshly-created secret stays in component state (not refetched)
  // so the plaintext can be displayed exactly once. After dismissal it
  // is gone — the API will never return it again.
  const [created, setCreated] = useState<CreatedSecret | null>(null);
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState("");
  const [newName, setNewName] = useState("");
  const [showCreateForm, setShowCreateForm] = useState(false);

  const refresh = useCallback(async () => {
    setLoading(true);
    setLoadError("");
    try {
      const res = await fetch("/api/v1/users/me/signing-secrets", { credentials: "include" });
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

  useEffect(() => { void refresh(); }, [refresh]);

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
      <h2 className="text-lg font-semibold mb-2">Webhook signing secrets</h2>
      <p className="text-sm text-muted mb-4 max-w-2xl">
        HMAC secrets used to sign your agents&apos; inbound webhook payloads.
        Pass any of these to <code className="font-mono text-xs bg-surface px-1.5 py-0.5 rounded border border-border">verify_signature()</code>{" "}
        in the SDK to confirm a payload came from e2a. The relay always signs
        with your most recently created secret; older ones stay valid for
        verification until you delete them, so rotation is: create new → swap
        in your code → delete old. Up to 5 active secrets at a time.
      </p>

      {created && <CreatedSecretBanner secret={created} onDismiss={() => setCreated(null)} />}

      {loading ? (
        <p className="text-sm text-muted">Loading…</p>
      ) : loadError ? (
        <p className="text-sm text-red-700 dark:text-red-400">{loadError}</p>
      ) : (
        <SigningSecretsTable secrets={secrets} onChange={refresh} />
      )}

      <div className="mt-4">
        {!showCreateForm ? (
          <button
            onClick={() => { setShowCreateForm(true); setCreateError(""); }}
            className="px-4 py-2 bg-foreground text-background rounded-lg text-sm font-medium hover:opacity-90 transition disabled:opacity-50"
            disabled={!loadError && secrets.length >= 5}
            title={!loadError && secrets.length >= 5 ? "Cap reached — delete one first" : undefined}
          >
            Create new secret
          </button>
        ) : (
          <div className="space-y-2 max-w-md">
            <label className="block">
              <span className="text-sm">Name (optional, e.g. <code className="font-mono text-xs">prod</code>)</span>
              <input
                autoFocus
                type="text"
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder="rolling-2026-04"
                className="mt-1 w-full border border-border rounded-lg px-3 py-2 text-sm bg-surface focus:outline-none focus:ring-2 focus:ring-foreground/20 focus:border-foreground/50 transition"
              />
            </label>
            {createError && (
              <p className="text-sm text-red-700 dark:text-red-400">{createError}</p>
            )}
            <div className="flex gap-2">
              <button
                onClick={handleCreate}
                disabled={creating}
                className="px-4 py-2 bg-foreground text-background rounded-lg text-sm font-medium hover:opacity-90 transition disabled:opacity-50"
              >
                {creating ? "Creating…" : "Create"}
              </button>
              <button
                onClick={() => { setShowCreateForm(false); setNewName(""); setCreateError(""); }}
                disabled={creating}
                className="px-4 py-2 border border-border rounded-lg text-sm hover:bg-background transition"
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

function CreatedSecretBanner({ secret, onDismiss }: { secret: CreatedSecret; onDismiss: () => void }) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(secret.secret);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Older browsers / test envs without clipboard support — silently
      // do nothing; the value is still visible in the textarea.
    }
  };

  return (
    <div className="mb-4 border border-amber-300 dark:border-amber-800/40 bg-amber-50 dark:bg-amber-950/20 rounded-lg p-4">
      <h3 className="text-sm font-semibold mb-1">Save this secret now — you can&apos;t see it again</h3>
      <p className="text-sm text-muted mb-3">
        This is the only time the full plaintext value will be shown. Copy it
        into your environment variable (commonly <code className="font-mono text-xs">E2A_HMAC_SECRET</code>) before dismissing.
      </p>
      <div className="flex gap-2 items-start">
        <input
          readOnly
          value={secret.secret}
          aria-label="Plaintext signing secret"
          onFocus={(e) => e.currentTarget.select()}
          className="flex-1 font-mono text-xs bg-surface border border-border rounded-lg px-3 py-2"
        />
        <button
          onClick={copy}
          className="px-3 py-2 border border-border rounded-lg text-sm hover:bg-background transition whitespace-nowrap"
        >
          {copied ? "Copied" : "Copy"}
        </button>
        <button
          onClick={onDismiss}
          className="px-3 py-2 border border-border rounded-lg text-sm hover:bg-background transition"
        >
          Dismiss
        </button>
      </div>
    </div>
  );
}

function SigningSecretsTable({ secrets, onChange }: { secrets: SigningSecretSummary[]; onChange: () => Promise<void> | void }) {
  if (secrets.length === 0) {
    return (
      <p className="text-sm text-muted">
        No secrets yet. New accounts always get a default one — if you&apos;re seeing this, something is wrong.
      </p>
    );
  }
  return (
    <div className="border border-border rounded-lg overflow-hidden">
      <table className="w-full text-sm">
        <thead className="bg-surface text-muted">
          <tr>
            <th className="text-left px-4 py-2 font-medium">Name</th>
            <th className="text-left px-4 py-2 font-medium">Prefix</th>
            <th className="text-left px-4 py-2 font-medium">Created</th>
            <th className="text-left px-4 py-2 font-medium">Last used</th>
            <th className="px-4 py-2"></th>
          </tr>
        </thead>
        <tbody>
          {secrets.map((s) => (
            <SigningSecretRow
              key={s.id}
              secret={s}
              isLast={secrets.length === 1}
              onChange={onChange}
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
}: {
  secret: SigningSecretSummary;
  isLast: boolean;
  onChange: () => Promise<void> | void;
}) {
  const [confirming, setConfirming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState("");

  const handleDelete = async () => {
    setDeleting(true);
    setError("");
    try {
      const res = await fetch(`/api/v1/users/me/signing-secrets/${encodeURIComponent(secret.id)}`, {
        method: "DELETE",
        credentials: "include",
      });
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

  return (
    <tr className="border-t border-border">
      <td className="px-4 py-3">{secret.name || "—"}</td>
      <td className="px-4 py-3 font-mono text-xs">{secret.secret_prefix}…</td>
      <td className="px-4 py-3 text-muted">{formatDate(secret.created_at)}</td>
      <td className="px-4 py-3 text-muted">
        {secret.last_signed_at ? formatRelative(secret.last_signed_at) : "Never"}
      </td>
      <td className="px-4 py-3 text-right">
        {error && <span className="text-xs text-red-700 dark:text-red-400 mr-2">{error}</span>}
        {confirming ? (
          <span className="inline-flex gap-1">
            <button
              onClick={handleDelete}
              disabled={deleting}
              className="px-2 py-1 bg-red-600 text-white rounded text-xs hover:bg-red-700 transition disabled:opacity-50"
            >
              {deleting ? "Deleting…" : "Confirm"}
            </button>
            <button
              onClick={() => { setConfirming(false); setError(""); }}
              disabled={deleting}
              className="px-2 py-1 border border-border rounded text-xs hover:bg-background transition"
            >
              Cancel
            </button>
          </span>
        ) : (
          <button
            onClick={() => setConfirming(true)}
            disabled={isLast}
            title={isLast ? "Cannot delete your only signing secret — create a new one first" : undefined}
            className="px-2 py-1 border border-border rounded text-xs hover:bg-background transition disabled:opacity-50 disabled:cursor-not-allowed"
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
      // Session is gone server-side along with the user; bouncing back
      // to the marketing landing page is the cleanest exit.
      window.location.href = "/?account_deleted=1";
    } catch (err) {
      setState("error");
      setErrorMessage(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <section>
      <h2 className="text-lg font-semibold mb-2 text-red-700 dark:text-red-400">
        Danger zone
      </h2>
      <div className="border border-red-200 dark:border-red-800/40 rounded-lg p-5">
        <h3 className="text-sm font-medium mb-1">Delete account</h3>
        <p className="text-sm text-muted mb-4 max-w-2xl">
          Permanently delete your account along with all your agents, domains,
          messages, API keys, and sessions, in a single Postgres transaction.
          <strong className="text-foreground"> This is irreversible.</strong>{" "}
          Right of deletion — GDPR Article 17 / CCPA &quot;Do Not Sell or Share&quot;.
        </p>
        {!open ? (
          <button
            onClick={() => setOpen(true)}
            className="px-4 py-2 border border-red-300 dark:border-red-800 text-red-700 dark:text-red-400 rounded-lg text-sm font-medium hover:bg-red-50 dark:hover:bg-red-950/30 transition"
          >
            Delete account…
          </button>
        ) : (
          <div className="space-y-3">
            <label className="block">
              <span className="text-sm">
                Type <code className="font-mono text-xs bg-surface px-1.5 py-0.5 rounded border border-border">DELETE</code> to confirm:
              </span>
              <input
                autoFocus
                type="text"
                value={confirmText}
                onChange={(e) => setConfirmText(e.target.value)}
                placeholder="DELETE"
                className="mt-1 w-full max-w-xs border border-border rounded-lg px-3 py-2 text-sm font-mono bg-surface focus:outline-none focus:ring-2 focus:ring-red-500/30 focus:border-red-500 transition"
              />
            </label>
            {state === "error" && (
              <p className="text-sm text-red-700 dark:text-red-400">
                Failed: {errorMessage || "unknown error"}
              </p>
            )}
            <div className="flex gap-2">
              <button
                onClick={handleDelete}
                disabled={!ready || state === "deleting"}
                className="px-4 py-2 bg-red-600 text-white rounded-lg text-sm font-medium hover:bg-red-700 transition disabled:opacity-50 disabled:cursor-not-allowed"
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
                className="px-4 py-2 border border-border rounded-lg text-sm hover:bg-background transition"
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
