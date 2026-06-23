"use client";

import { useEffect, useState } from "react";
import useSWR from "swr";
import { PageShell } from "../../components/loft/PageShell";

// LimitsInfo matches the LimitsView shape returned by GET /v1/account.
// Kept inline rather than imported from a generated client because the
// OSS SDK doesn't expose this endpoint yet (it's a dashboard-only
// surface — SDK consumers would call /agents and /messages directly).
type LimitsInfo = {
  plan_code: string;
  limits: {
    max_agents: number;
    max_domains: number;
    max_messages_month: number;
    max_storage_bytes: number;
  };
  usage: {
    agents: number;
    domains: number;
    messages_month: number;
    storage_bytes: number;
  };
  upgrade_url: string;
};

// BILLING_API is the base URL of the external limits provisioner (the
// hosted billing sidecar). When empty the dashboard renders the usage
// surface without Upgrade / Manage Billing affordances — appropriate
// for self-host deployments that don't run a paid tier. Set at build
// time via NEXT_PUBLIC_BILLING_API; populated in the prod web image.
const BILLING_API = (process.env.NEXT_PUBLIC_BILLING_API ?? "").replace(/\/$/, "");

async function fetchLimits(): Promise<LimitsInfo> {
  const res = await fetch("/v1/account", { credentials: "include" });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(`limits: HTTP ${res.status}${body ? ` — ${body}` : ""}`);
  }
  return res.json();
}

// formatBytes renders storage in the unit that makes the number
// human-legible — KB / MB / GB. Matches the formatting Resend and
// AgentMail use in their dashboards so users carrying mental models
// from those tools aren't surprised.
function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

function formatNumber(n: number): string {
  return n.toLocaleString();
}

// pctTone picks the bar color based on how close to the cap the user
// is. <70% neutral, 70-90% warning, >=90% danger. Tones reuse existing
// CSS vars so the dashboard's theme tokens own the actual colors.
function pctTone(pct: number): "neutral" | "warn" | "danger" {
  if (pct >= 90) return "danger";
  if (pct >= 70) return "warn";
  return "neutral";
}

// PLAN_CATALOG mirrors the operator-side plan catalog at
// e2a-ops/billing/internal/plans/plans.go. Hardcoded here because the
// dashboard isn't yet wired to the sidecar's GET /api/billing/plan
// listing endpoint — and even if it were, the values are stable enough
// that an extra round-trip on the upgrade page isn't worth it. If you
// change a cap on either side, update both files in the same PR.
const PLAN_CATALOG = [
  { code: "pro", name: "Pro", price: "$20/mo", chips: ["25 inboxes", "10 domains", "50k msgs/mo", "10 GiB"] },
  { code: "scale", name: "Scale", price: "$99/mo", chips: ["250 inboxes", "50 domains", "500k msgs/mo", "100 GiB"] },
] as const;

type UsageRowProps = {
  label: string;
  current: string;
  limit: string;
  pct: number;
};

function UsageRow({ label, current, limit, pct }: UsageRowProps) {
  const tone = pctTone(pct);
  const barColor =
    tone === "danger"
      ? "var(--accent-danger, #ef4444)"
      : tone === "warn"
      ? "var(--accent-warn, #f59e0b)"
      : "var(--accent)";
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline justify-between text-sm">
        <span className="text-foreground font-medium">{label}</span>
        <span className="text-muted">
          {current} <span className="text-muted/70">/ {limit}</span>
        </span>
      </div>
      <div className="h-1.5 rounded-full bg-background overflow-hidden">
        <div
          className="h-full rounded-full transition-[width] duration-300"
          style={{
            width: `${Math.min(100, Math.max(0, pct))}%`,
            background: barColor,
          }}
          aria-hidden
        />
      </div>
    </div>
  );
}

export default function BillingPage() {
  const { data, error, isLoading, mutate } = useSWR<LimitsInfo>(
    "limits",
    fetchLimits,
  );

  // Track the specific action in flight so each button can show its own
  // "Opening…" label while disabling the others. Two upgrade variants
  // because the page renders a Pro and a Scale CTA side-by-side.
  type PendingAction = "upgrade-pro" | "upgrade-scale" | "portal";
  const [actionPending, setActionPending] = useState<PendingAction | null>(null);

  // Both Upgrade and Manage Billing POST to the sidecar and follow the
  // returned `url`. POST (not GET) because the OSS session cookie is
  // SameSite=Lax — Lax permits top-level GET navigations from third
  // parties, which would make GET endpoints CSRF-able (a malicious
  // page could create real Stripe Checkout sessions for the victim).
  // POSTs from a third-party origin are blocked by Lax, so the dashboard
  // owns the call and the cross-origin attack surface is gone.
  //
  // `body` carries the plan selector for /api/billing/checkout (sidecar
  // defaults to Pro when absent). /api/billing/portal ignores it.
  async function postBilling(endpoint: string, kind: PendingAction, body?: unknown) {
    setActionPending(kind);
    try {
      const res = await fetch(endpoint, {
        method: "POST",
        credentials: "include",
        headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
        body: body !== undefined ? JSON.stringify(body) : undefined,
      });
      if (!res.ok) {
        const text = await res.text().catch(() => "");
        throw new Error(`HTTP ${res.status}${text ? `: ${text}` : ""}`);
      }
      const json = (await res.json()) as { url?: string };
      if (!json.url) {
        throw new Error("billing endpoint returned no url");
      }
      window.location.href = json.url;
    } catch (err) {
      // Best-effort recovery: surface the error to the user, clear
      // the pending state, and let them retry. We don't reset SWR
      // because the underlying limits data is unaffected by a
      // failed checkout/portal session.
      setActionPending(null);
      alert(`Could not open billing: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  // When the user navigates away (e.g. to Stripe Checkout) and hits
  // Back, the browser may restore the page from bfcache with any
  // in-flight fetch abandoned. SWR's isLoading state then sticks at
  // true and the user sees a permanent "Loading…". Force a revalidate
  // on every pageshow with persisted=true so the page never gets
  // stuck after a Back navigation.
  useEffect(() => {
    const onShow = (e: PageTransitionEvent) => {
      if (e.persisted) void mutate();
    };
    window.addEventListener("pageshow", onShow);
    return () => window.removeEventListener("pageshow", onShow);
  }, [mutate]);

  // Compute usage percentages once data is loaded. Guard zero limits
  // (treat as 0% rather than NaN/Infinity) so a misconfigured row with
  // max_*=0 doesn't paint a full red bar.
  const pct = (used: number, cap: number) => (cap > 0 ? (used / cap) * 100 : 0);

  return (
    <PageShell
      crumbs={["Billing"]}
      eyebrow="Plan & usage"
      title="Billing"
      subtitle="Your current resource caps and month-to-date usage."
    >
      {error && (
        <div
          className="rounded-lg border px-4 py-3 text-sm mb-6"
          style={{
            borderColor: "var(--border)",
            background: "var(--bg-elev)",
            color: "var(--fg-muted)",
          }}
        >
          Couldn&apos;t load your limits. {String(error.message ?? error)}
        </div>
      )}

      {isLoading && !data && (
        <div className="text-sm text-muted">Loading…</div>
      )}

      {data && (
        <div className="space-y-6">
          {/* Plan summary card */}
          <section
            className="rounded-xl border p-5"
            style={{
              borderColor: "var(--border)",
              background: "var(--surface)",
            }}
          >
            <div className="flex flex-col md:flex-row md:items-center md:justify-between gap-4">
              <div>
                <div className="text-xs uppercase tracking-wide text-muted">
                  Current plan
                </div>
                <div className="text-lg font-semibold text-foreground mt-1">
                  {data.plan_code === "default"
                    ? "Default (operator-configured)"
                    : data.plan_code}
                </div>
              </div>
              {BILLING_API && data.upgrade_url && (
                // upgrade_url present → user has an active Stripe
                // subscription. Clicking POSTs to the sidecar, which
                // returns a fresh Stripe Billing Portal URL. From the
                // Portal, users can switch plans (Pro ↔ Scale) and
                // cancel — that's why we don't render separate
                // upgrade-to-Scale buttons for paid users.
                <div className="flex items-center gap-2 flex-wrap justify-end">
                  <button
                    type="button"
                    disabled={actionPending !== null}
                    onClick={() => postBilling(data.upgrade_url, "portal")}
                    className="px-3 py-1.5 rounded-md text-sm border hover:bg-background transition disabled:opacity-50 disabled:cursor-not-allowed"
                    style={{ borderColor: "var(--border)", color: "var(--fg)" }}
                  >
                    {actionPending === "portal" ? "Opening…" : "Manage billing"}
                  </button>
                </div>
              )}
            </div>

            {/* Plan picker for free users: render Pro + Scale side-by-side
                with the caps each tier includes spelled out, so users
                aren't picking blind from "Upgrade to Pro · $20/mo" alone.
                Hidden once the user has an active subscription — they
                manage plan changes through the Stripe Billing Portal. */}
            {BILLING_API && !data.upgrade_url && (
              <div className="mt-5 grid gap-3 sm:grid-cols-2">
                {PLAN_CATALOG.map((p) => {
                  const pendingKey = `upgrade-${p.code}` as PendingAction;
                  return (
                    <div
                      key={p.code}
                      className="rounded-lg border p-4 flex flex-col gap-3"
                      style={{ borderColor: "var(--border)", background: "var(--bg-elev)" }}
                    >
                      <div>
                        <div className="text-sm font-semibold text-foreground">{p.name}</div>
                        <div className="text-xs text-muted mt-0.5">{p.price}</div>
                      </div>
                      <ul className="text-xs text-muted space-y-1">
                        {p.chips.map((c) => (
                          <li key={c}>· {c}</li>
                        ))}
                      </ul>
                      <button
                        type="button"
                        disabled={actionPending !== null}
                        onClick={() =>
                          postBilling(`${BILLING_API}/api/billing/checkout`, pendingKey, {
                            plan: p.code,
                          })
                        }
                        className="mt-auto px-3 py-1.5 rounded-md text-sm font-medium bg-accent text-white hover:bg-accent/90 transition disabled:opacity-50 disabled:cursor-not-allowed"
                      >
                        {actionPending === pendingKey ? "Opening…" : `Upgrade to ${p.name}`}
                      </button>
                    </div>
                  );
                })}
              </div>
            )}
          </section>

          {/* Usage card */}
          <section
            className="rounded-xl border p-5 space-y-5"
            style={{
              borderColor: "var(--border)",
              background: "var(--surface)",
            }}
          >
            <div>
              <div className="text-xs uppercase tracking-wide text-muted">
                This billing period
              </div>
              <div className="text-sm text-muted mt-1">
                Counters reset at the start of each calendar month (UTC).
              </div>
            </div>

            <UsageRow
              label="Inboxes"
              current={formatNumber(data.usage.agents)}
              limit={formatNumber(data.limits.max_agents)}
              pct={pct(data.usage.agents, data.limits.max_agents)}
            />
            <UsageRow
              label="Domains"
              current={formatNumber(data.usage.domains)}
              limit={formatNumber(data.limits.max_domains)}
              pct={pct(data.usage.domains, data.limits.max_domains)}
            />
            <UsageRow
              label="Messages this month"
              current={formatNumber(data.usage.messages_month)}
              limit={formatNumber(data.limits.max_messages_month)}
              pct={pct(data.usage.messages_month, data.limits.max_messages_month)}
            />
            <UsageRow
              label="Storage"
              current={formatBytes(data.usage.storage_bytes)}
              limit={formatBytes(data.limits.max_storage_bytes)}
              pct={pct(data.usage.storage_bytes, data.limits.max_storage_bytes)}
            />

            <div className="pt-2">
              <button
                onClick={() => mutate()}
                className="text-xs text-muted hover:text-foreground transition"
              >
                Refresh
              </button>
            </div>
          </section>
        </div>
      )}
    </PageShell>
  );
}
