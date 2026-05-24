"use client";

// Shared chrome for every per-agent route under /dashboard/agents/.
// Reads `?email=` from the URL — we use a query param instead of a
// path segment because web/ is statically exported (next.config.ts:9)
// and dynamic segments would require generateStaticParams() with the
// full set of emails enumerated at build time.

import { useEffect, useState } from "react";
import { usePathname, useSearchParams } from "next/navigation";
import { Topbar } from "../../../components/loft/Topbar";
import { AgentHeader, type AgentTab } from "../../../components/messages/AgentHeader";
import { listAgents } from "../../../components/onboarding/api";
import type { DashboardAgent } from "../../../components/types";

function detectTab(pathname: string): AgentTab {
  if (pathname.startsWith("/dashboard/agents/settings")) return "settings";
  // Default to messages — the only other live tab today, and the
  // canonical landing destination from the dashboard's "Open inbox →"
  // CTA. Any unknown sub-path under /dashboard/agents/ (404s aside)
  // also lands here so the AgentHeader has a sensible active state.
  return "messages";
}

export default function AgentLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const email = searchParams.get("email") ?? "";
  const tab = detectTab(pathname ?? "");

  const [agent, setAgent] = useState<DashboardAgent | null>(null);
  const [fetchError, setFetchError] = useState("");
  const [loading, setLoading] = useState(true);

  // Missing-email is a URL-shape problem, not a fetch error — surface
  // it as a derived value so we don't have to call setState in the
  // effect to flip the loading flag (React 19 lint rule).
  const error = email ? fetchError : "Missing ?email= query parameter";

  useEffect(() => {
    if (!email) return;
    let cancelled = false;
    listAgents()
      .then((agents) => {
        if (cancelled) return;
        const match = agents.find((a) => a.email === email);
        if (!match) {
          setFetchError(`Agent ${email} not found`);
        } else {
          setAgent(match);
        }
      })
      .catch((err) => {
        if (cancelled) return;
        setFetchError(
          err instanceof Error ? err.message : "Failed to load agent",
        );
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [email]);

  return (
    <div className="flex flex-col" data-app-surface>
      <Topbar crumbs={["Dashboard", "Agents", email || "—"]} />
      {error && (
        <div
          className="m-6 p-4 text-[13px]"
          style={{
            background: "var(--danger-bg)",
            border: "1px solid var(--danger-bg)",
            color: "var(--danger-strong)",
            borderRadius: "var(--r-md)",
          }}
        >
          {error}
        </div>
      )}
      {!error && loading && (
        <div
          className="px-7 py-8 text-[13px]"
          style={{ color: "var(--fg-muted)" }}
        >
          Loading agent…
        </div>
      )}
      {!error && !loading && agent && (
        <>
          <AgentHeader agent={agent} tab={tab} />
          {children}
        </>
      )}
    </div>
  );
}
