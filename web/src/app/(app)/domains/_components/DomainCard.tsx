"use client";

import { useState } from "react";
import { DNSRecord } from "../../../components/Field";
import { Chip } from "../../../components/loft/Chip";
import { Dot } from "../../../components/loft/Dot";
import { verifyDomain, deleteDomain } from "../../../components/onboarding/api";
import type { DomainInfo } from "../../../components/onboarding/types";
import { track } from "../../../components/onboarding/analytics";

export function DomainCard({
  domain,
  agentCount,
  onVerified,
  onDeleted,
}: {
  domain: DomainInfo;
  agentCount: number;
  onVerified: () => void;
  onDeleted: () => void;
}) {
  const [showDNS, setShowDNS] = useState(false);
  const [verifying, setVerifying] = useState(false);
  const [verifyError, setVerifyError] = useState("");
  const [deleting, setDeleting] = useState(false);

  const handleVerify = async () => {
    setVerifyError("");
    setVerifying(true);
    track("domain_verify_attempted", { domain: domain.domain });
    try {
      await verifyDomain(domain.domain);
      track("domain_verify_succeeded", { domain: domain.domain });
      onVerified();
    } catch (err) {
      track("domain_verify_failed", { domain: domain.domain });
      setVerifyError(
        err instanceof Error ? err.message : "Verification failed",
      );
    } finally {
      setVerifying(false);
    }
  };

  const handleDelete = async () => {
    if (!confirm(`Delete domain ${domain.domain}? This cannot be undone.`))
      return;
    setDeleting(true);
    try {
      await deleteDomain(domain.domain);
      onDeleted();
    } catch (err) {
      alert(err instanceof Error ? err.message : "Failed to delete domain");
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div
      style={{
        background: "var(--bg-panel)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-lg)",
        padding: "20px 22px",
      }}
    >
      {/* Header row */}
      <div className="flex items-start justify-between flex-wrap gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2 mb-2 flex-wrap">
            <code
              className="font-mono text-[14px] font-semibold px-2 py-0.5"
              style={{
                color: "var(--fg)",
                background: "var(--bg-elev)",
                border: "1px solid var(--border-sub)",
                borderRadius: "var(--r-sm)",
              }}
            >
              {domain.domain}
            </code>
            <Chip tone={domain.verified ? "success" : "warn"}>
              <Dot tone={domain.verified ? "success" : "warn"} />
              {domain.verified ? "Verified" : "Unverified"}
            </Chip>
            {domain.is_primary && (
              <Chip tone="neutral">Primary</Chip>
            )}
          </div>
          <p
            className="text-[12px]"
            style={{ color: "var(--fg-muted)" }}
          >
            {agentCount === 0
              ? "No agents"
              : agentCount === 1
                ? "1 agent"
                : `${agentCount} agents`}
          </p>
          <p
            className="font-mono text-[11px] mt-0.5"
            style={{
              color: "var(--fg-subtle)",
              letterSpacing: "0.02em",
            }}
          >
            Added {new Date(domain.created_at).toLocaleDateString()}
            {domain.verified_at && (
              <>
                {" · verified "}
                {new Date(domain.verified_at).toLocaleDateString()}
              </>
            )}
            {domain.last_checked_at && (
              <>
                {" · last checked "}
                {new Date(domain.last_checked_at).toLocaleDateString()}
              </>
            )}
          </p>
        </div>
        <div className="flex gap-2 shrink-0">
          {domain.verified ? (
            <a
              href={`/get-started?domain=${encodeURIComponent(domain.domain)}`}
              className="text-[12px] px-3 py-1.5 font-medium transition"
              style={{
                background: "var(--accent-fill)",
                color: "var(--accent-fg)",
                borderRadius: "var(--r-md)",
              }}
            >
              Create agent
            </a>
          ) : (
            <button
              onClick={handleVerify}
              disabled={verifying}
              className="text-[12px] px-3 py-1.5 font-medium transition disabled:opacity-50"
              style={{
                background: "var(--fg)",
                color: "var(--bg)",
                borderRadius: "var(--r-md)",
              }}
            >
              {verifying ? "Verifying..." : "Verify domain"}
            </button>
          )}
          <button
            onClick={handleDelete}
            disabled={deleting}
            className="text-[12px] px-3 py-1.5 transition disabled:opacity-50"
            style={{
              color: "var(--danger-strong)",
              border: "1px solid var(--danger-bg)",
              background: "transparent",
              borderRadius: "var(--r-md)",
            }}
          >
            Delete
          </button>
        </div>
      </div>

      {/* Verify error */}
      {verifyError && (
        <div
          className="mt-3 p-3 text-[13px]"
          style={{
            background: "var(--danger-bg)",
            color: "var(--danger-strong)",
            border: "1px solid var(--danger-bg)",
            borderRadius: "var(--r-md)",
          }}
        >
          {verifyError}
          <p className="mt-1 text-[11px]">
            DNS changes can take a few minutes to propagate. Wait a bit and
            try again.
          </p>
        </div>
      )}

      {/* DNS toggle */}
      <div className="mt-3">
        <button
          onClick={() => setShowDNS(!showDNS)}
          className="text-[12px] transition flex items-center gap-1"
          style={{ color: "var(--fg-muted)" }}
        >
          {showDNS ? "Hide" : "View"} DNS records
          <span className="text-[10px]">
            {showDNS ? "▲" : "▼"}
          </span>
        </button>
      </div>

      {/* DNS records — MX + TXT only per BACKEND_TODO #4/#5 (DKIM row hidden until per-domain DKIM ships) */}
      {showDNS && (
        <div
          className="mt-3 pt-4 space-y-4"
          style={{ borderTop: "1px solid var(--border)" }}
        >
          <DNSRecord
            type="MX"
            label="Route email to e2a"
            fields={[
              { label: "Name", value: domain.domain },
              { label: "Mail server", value: domain.dns_records.mx.value },
              {
                label: "Priority",
                value: String(domain.dns_records.mx.priority),
              },
            ]}
          />
          <DNSRecord
            type="TXT"
            label="Prove domain ownership"
            fields={[
              { label: "Name", value: domain.domain },
              { label: "Content", value: domain.verification_token },
            ]}
          />
          {!domain.verified && (
            <div
              className="p-3 text-[12px]"
              style={{
                background: "var(--warn-bg)",
                color: "var(--warn-strong)",
                border: "1px solid var(--warn-bg)",
                borderRadius: "var(--r-md)",
              }}
            >
              DNS changes can take a few minutes to propagate. Wait a bit
              before verifying.
            </div>
          )}
        </div>
      )}
    </div>
  );
}
