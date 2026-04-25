"use client";

import { useState } from "react";
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
          Account profile, data export, and account deletion.
        </p>
      </header>

      <ProfileSection user={user} />
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
