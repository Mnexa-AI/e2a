"use client";

import { useEffect } from "react";
import useSWR from "swr";
import { PageShell } from "../../components/loft/PageShell";

// LimitsInfo matches the shape returned by GET /api/v1/users/me/limits.
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
  const res = await fetch("/api/v1/users/me/limits", { credentials: "include" });
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
              {BILLING_API && (
                <div className="flex items-center gap-2">
                  {data.upgrade_url ? (
                    // upgrade_url is set by the external provisioner on
                    // active subscriptions to point at the Stripe-hosted
                    // billing portal (via a GET on the sidecar's
                    // /api/billing/portal which 302s to a fresh portal
                    // session). Same-tab navigation: Stripe's portal
                    // already has a "Return to merchant" link that
                    // redirects back to PORTAL_RETURN_URL, so the user
                    // never gets stranded.
                    <a
                      href={data.upgrade_url}
                      className="px-3 py-1.5 rounded-md text-sm border hover:bg-background transition"
                      style={{ borderColor: "var(--border)", color: "var(--fg)" }}
                    >
                      Manage billing
                    </a>
                  ) : (
                    // No upgrade_url → user is on the free/default plan.
                    // Send them to the sidecar's checkout endpoint. The
                    // sidecar's GET /api/billing/checkout 302-redirects
                    // to Stripe-hosted Checkout for the Pro plan (the
                    // default when ?plan= is omitted).
                    <a
                      href={`${BILLING_API}/api/billing/checkout`}
                      className="px-3 py-1.5 rounded-md text-sm font-medium bg-accent text-white hover:bg-accent/90 transition"
                    >
                      Upgrade
                    </a>
                  )}
                </div>
              )}
            </div>
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
              label="Agents"
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
