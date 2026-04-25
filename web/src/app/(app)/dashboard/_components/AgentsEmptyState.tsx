"use client";

import Link from "next/link";

export function AgentsEmptyState() {
  return (
    <div className="border border-border rounded-lg p-12 text-center">
      <h3 className="text-lg font-semibold mb-2">No agents yet</h3>
      <p className="text-sm text-muted mb-6 max-w-md mx-auto">
        Create an agent to give it an email address — either on a shared e2a
        domain or your own custom domain.
      </p>
      <div className="flex items-center justify-center gap-3">
        <Link
          href="/get-started"
          className="inline-flex items-center px-4 py-2 text-sm font-medium bg-foreground text-background rounded-lg hover:opacity-90 transition"
        >
          Create your first agent
        </Link>
        <Link
          href="/domains"
          className="inline-flex items-center px-4 py-2 text-sm font-medium border border-border rounded-lg hover:bg-surface transition"
        >
          Set up a domain
        </Link>
      </div>
    </div>
  );
}
