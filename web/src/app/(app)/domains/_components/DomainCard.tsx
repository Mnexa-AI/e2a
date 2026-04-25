"use client";

import { useState } from "react";
import { DNSRecord } from "../../../components/Field";
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
      setVerifyError(err instanceof Error ? err.message : "Verification failed");
    } finally {
      setVerifying(false);
    }
  };

  const handleDelete = async () => {
    if (!confirm(`Delete domain ${domain.domain}? This cannot be undone.`)) return;
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
    <div className="border border-border rounded-lg p-4">
      {/* Header row */}
      <div className="flex items-start justify-between">
        <div>
          <div className="flex items-center gap-2 mb-1">
            <code className="text-sm font-mono font-medium">{domain.domain}</code>
            <span
              className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${
                domain.verified
                  ? "bg-green-100 text-green-700"
                  : "bg-amber-100 text-amber-700"
              }`}
            >
              {domain.verified ? "Verified" : "Unverified"}
            </span>
          </div>
          <p className="text-xs text-muted">
            {agentCount === 0
              ? "No agents"
              : agentCount === 1
                ? "1 agent"
                : `${agentCount} agents`}
          </p>
          {domain.verified_at && (
            <p className="text-xs text-muted">
              Verified {new Date(domain.verified_at).toLocaleDateString()}
            </p>
          )}
          <p className="text-xs text-muted">
            Added {new Date(domain.created_at).toLocaleDateString()}
          </p>
        </div>
        <div className="flex gap-2 shrink-0">
          {domain.verified ? (
            <a
              href={`/get-started?domain=${encodeURIComponent(domain.domain)}`}
              className="text-xs px-3 py-1.5 bg-foreground text-background rounded-md font-medium hover:opacity-90 transition"
            >
              Create agent
            </a>
          ) : (
            <button
              onClick={handleVerify}
              disabled={verifying}
              className="text-xs px-3 py-1.5 bg-foreground text-background rounded-md font-medium hover:opacity-90 transition disabled:opacity-50"
            >
              {verifying ? "Verifying..." : "Verify domain"}
            </button>
          )}
          <button
            onClick={handleDelete}
            disabled={deleting}
            className="text-xs px-3 py-1.5 text-red-600 border border-red-200 rounded-md hover:bg-red-50 transition disabled:opacity-50"
          >
            Delete
          </button>
        </div>
      </div>

      {/* Verify error */}
      {verifyError && (
        <div className="mt-3 p-3 bg-red-50 border border-red-200 rounded-lg text-sm text-red-700">
          {verifyError}
          <p className="mt-1 text-xs">
            DNS changes can take a few minutes to propagate. Wait a bit and try again.
          </p>
        </div>
      )}

      {/* DNS toggle */}
      <div className="mt-3">
        <button
          onClick={() => setShowDNS(!showDNS)}
          className="text-xs text-muted hover:text-foreground transition flex items-center gap-1"
        >
          {showDNS ? "Hide" : "View"} DNS records
          <span className="text-[10px]">{showDNS ? "\u25B2" : "\u25BC"}</span>
        </button>
      </div>

      {/* DNS records */}
      {showDNS && (
        <div className="mt-3 border-t border-border pt-4 space-y-4">
          <DNSRecord
            type="MX"
            label="Route email to e2a"
            fields={[
              { label: "Name", value: domain.domain },
              { label: "Mail server", value: domain.dns_records.mx.value },
              { label: "Priority", value: String(domain.dns_records.mx.priority) },
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
            <div className="p-3 bg-amber-50 border border-amber-200 rounded-lg text-xs text-amber-800">
              DNS changes can take a few minutes to propagate. Wait a bit before verifying.
            </div>
          )}
        </div>
      )}
    </div>
  );
}
