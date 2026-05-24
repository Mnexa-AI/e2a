"use client";

// The /dashboard/agents/ path itself has no UI — the canonical entry
// points are /dashboard/agents/messages?email=… and
// /dashboard/agents/settings?email=…, both wired from the dashboard
// agent card. Bare URLs (typed by hand, stale bookmarks) get redirected
// back to /dashboard so they don't silently 404.

import { useEffect } from "react";
import { useRouter } from "next/navigation";

export default function AgentsIndexRedirect() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/dashboard");
  }, [router]);
  return (
    <div className="px-7 py-8 text-[13px]" style={{ color: "var(--fg-muted)" }}>
      Redirecting…
    </div>
  );
}
