"use client";

import Link from "next/link";
import type { ResumeOption } from "../../../components/onboarding/selectors";
import { track } from "../../../components/onboarding/analytics";
import { useEffect } from "react";

export function ResumePanel({
  options,
  onResume,
  onCreateAnother,
}: {
  options: ResumeOption[];
  onResume: (option: ResumeOption) => void;
  onCreateAnother: () => void;
}) {
  useEffect(() => {
    track("onboarding_resume_shown", {
      option_count: options.length,
      has_existing_agents: options.some((o) => o.type === "has_agents"),
    });
  }, [options.length]);

  const hasAgents = options.find((o) => o.type === "has_agents");
  const domainOptions = options.filter(
    (o): o is Extract<ResumeOption, { type: "verify_domain" | "create_agent" }> =>
      o.type !== "has_agents",
  );

  const handleResume = (option: ResumeOption) => {
    track("onboarding_resume_selected", {
      resume_type: option.type,
      ...(option.type !== "has_agents" ? { domain: option.domain.domain, verified: option.domain.verified } : {}),
    });
    onResume(option);
  };

  return (
    <div>
      <h2 className="text-2xl font-bold tracking-tight mb-2">Welcome back</h2>
      <p className="text-muted mb-8">
        Pick up where you left off, or start something new.
      </p>

      <div className="space-y-3">
        {/* Existing agents */}
        {hasAgents && hasAgents.type === "has_agents" && (
          <div className="p-4 rounded-lg border border-border bg-surface">
            <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
              <div className="min-w-0">
                <p className="text-sm font-medium">
                  You have {hasAgents.count} agent{hasAgents.count !== 1 ? "s" : ""} set up
                </p>
                <p className="text-xs text-muted mt-0.5">
                  Manage mode, webhooks, and delivery from the Agents page.
                </p>
              </div>
              <Link
                href="/dashboard"
                className="shrink-0 px-3 py-1.5 text-xs font-medium bg-foreground text-background rounded-md hover:opacity-90 transition self-start sm:self-auto"
              >
                Go to Agents
              </Link>
            </div>
          </div>
        )}

        {/* Domain actions */}
        {domainOptions.map((option) => (
          <button
            key={option.domain.domain}
            type="button"
            onClick={() => handleResume(option)}
            className="w-full text-left p-4 rounded-lg border border-border hover:border-foreground/20 transition flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2"
          >
            <div className="min-w-0">
              <code className="text-sm font-mono font-medium break-all">{option.domain.domain}</code>
              <span
                className={`ml-2 inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${
                  option.type === "create_agent"
                    ? "bg-green-100 text-green-700"
                    : "bg-amber-100 text-amber-700"
                }`}
              >
                {option.type === "create_agent" ? "Verified" : "Unverified"}
              </span>
            </div>
            <span className="text-xs text-accent font-medium shrink-0">
              {option.type === "create_agent" ? "Create inbox on this domain" : "Resume verification"}
            </span>
          </button>
        ))}

        {/* Secondary actions */}
        <div className="pt-3 border-t border-border flex items-center gap-4">
          <button
            type="button"
            onClick={onCreateAnother}
            className="text-sm text-accent hover:underline"
          >
            Create another agent
          </button>
          <Link href="/domains" className="text-sm text-muted hover:text-foreground transition">
            Manage domains
          </Link>
        </div>
      </div>
    </div>
  );
}
