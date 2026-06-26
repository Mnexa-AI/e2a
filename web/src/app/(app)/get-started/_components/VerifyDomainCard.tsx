"use client";

import { useState } from "react";
import { verifyDomain } from "../../../components/onboarding/api";
import type { DomainInfo } from "../../../components/onboarding/types";
import { track } from "../../../components/onboarding/analytics";

export function VerifyDomainCard({
  domain,
  onVerified,
}: {
  domain: DomainInfo;
  onVerified: () => void;
}) {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const handleVerify = async () => {
    setError("");
    setLoading(true);
    track("domain_verify_attempted", { domain: domain.domain });
    try {
      await verifyDomain(domain.domain);
      track("domain_verify_succeeded", { domain: domain.domain });
      onVerified();
    } catch (err) {
      track("domain_verify_failed", { domain: domain.domain });
      setError(err instanceof Error ? err.message : "Verification failed");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div>
      <h2
        className="mb-2"
        style={{
          fontFamily: "var(--f-ui)",
          fontWeight: 600,
          fontSize: 28,
          letterSpacing: "-0.01em",
          color: "var(--fg)",
        }}
      >
        Verify domain ownership
      </h2>
      <p className="mb-7 text-[14px]" style={{ color: "var(--fg-muted)" }}>
        e2a will check that your TXT record is in place for{" "}
        <code
          className="font-mono text-[12px] px-1.5 py-0.5 break-all"
          style={{
            background: "var(--bg-elev)",
            border: "1px solid var(--border-sub)",
            borderRadius: "var(--r-sm)",
            color: "var(--fg)",
          }}
        >
          {domain.domain}
        </code>
        .
      </p>
      {error && (
        <div className="mb-6 p-3 bg-red-50 border border-red-200 rounded-lg text-sm text-red-700">
          {error}
          <p className="mt-1 text-xs">
            DNS changes can take a few minutes to propagate. Wait a bit and try again.
          </p>
        </div>
      )}
      <button
        onClick={handleVerify}
        disabled={loading}
        className="w-full bg-foreground text-background py-3 rounded-lg text-sm font-medium hover:opacity-90 transition disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {loading ? "Verifying..." : "Verify domain"}
      </button>
      <p className="mt-4 text-xs text-muted text-center">
        After adding your TXT record, it may take up to 5 minutes for e2a&apos;s DNS resolver to pick it up. If verification fails, wait a bit and try again.
      </p>
    </div>
  );
}
