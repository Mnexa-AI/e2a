"use client";

import { useState } from "react";
import { DNSRecord } from "../../../components/Field";
import { Chip } from "../../../components/loft/Chip";
import { Dot } from "../../../components/loft/Dot";
import {
  verifyDomain,
  deleteDomain,
  setDomainPrimary,
} from "../../../components/onboarding/api";
import type {
  DomainInfo,
  VerifyDomainResponse,
} from "../../../components/onboarding/types";
import { track } from "../../../components/onboarding/analytics";

// Renders a found/missing/deferred chip next to each per-record row in
// the DNS expansion. "deferred" only appears on legacy pre-migration
// domains that don't have a stored DKIM keypair yet — re-claiming the
// domain provisions a key and flips the chip to found/missing on the
// next probe.
function RecordStatusChip({
  status,
}: {
  status: "found" | "missing" | "deferred" | undefined;
}) {
  if (!status) return null;
  if (status === "found")
    return (
      <Chip tone="success">
        <Dot tone="success" />
        Found
      </Chip>
    );
  if (status === "deferred")
    return <Chip tone="neutral">No key registered</Chip>;
  return (
    <Chip tone="warn">
      <Dot tone="warn" />
      Missing
    </Chip>
  );
}

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
  const [promoting, setPromoting] = useState(false);
  // Cached per-record diagnostic from the most recent verify probe. Until
  // the user clicks "View DNS records" + retries, this is null and the
  // chips render as "—" placeholders.
  const [probe, setProbe] = useState<VerifyDomainResponse | null>(null);

  const handleSetPrimary = async () => {
    setPromoting(true);
    try {
      await setDomainPrimary(domain.domain);
      onVerified(); // reuse the parent's refresh — refetches the list
    } catch (err) {
      alert(
        err instanceof Error
          ? err.message
          : "Failed to set primary domain",
      );
    } finally {
      setPromoting(false);
    }
  };

  const handleVerify = async () => {
    setVerifyError("");
    setVerifying(true);
    track("domain_verify_attempted", { domain: domain.domain });
    try {
      const result = await verifyDomain(domain.domain);
      setProbe(result);
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
              <span
                title="Your default domain. New agents are created here by default and onboarding flows surface it first."
                style={{ cursor: "help" }}
              >
                <Chip tone="neutral">Primary</Chip>
              </span>
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
        <div className="flex gap-2 shrink-0 flex-wrap">
          {/* Make-primary action: only shown for verified, non-primary
              domains. PATCH /api/v1/domains/{domain} atomically swaps
              the primary flag on the server side. */}
          {domain.verified && !domain.is_primary && (
            <button
              onClick={handleSetPrimary}
              disabled={promoting}
              title="Mark this domain as your default. New agents will be created under it by default, and onboarding flows surface it first. You can have one primary domain at a time."
              className="text-[12px] px-3 py-1.5 transition disabled:opacity-50"
              style={{
                background: "var(--bg-panel)",
                color: "var(--fg)",
                border: "1px solid var(--border)",
                borderRadius: "var(--r-md)",
              }}
            >
              {promoting ? "Setting…" : "Make primary"}
            </button>
          )}
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

      {/* DNS records — per-record status chips populated from the
          most recent verify probe (BACKEND_TODO #4). The DKIM row
          renders for domains with a stored keypair (BACKEND_TODO #5);
          pre-migration domains have no `dkim` block in dns_records and
          the row is omitted. */}
      {showDNS && (
        <div
          className="mt-3 pt-4 space-y-4"
          style={{ borderTop: "1px solid var(--border)" }}
        >
          <div className="flex items-center justify-between gap-2 flex-wrap">
            <p
              className="font-mono text-[10px] uppercase font-semibold"
              style={{ color: "var(--fg-subtle)", letterSpacing: "0.08em" }}
            >
              DNS records
              {(probe || domain.last_checked_at) && (
                <span
                  className="ml-2 font-normal normal-case"
                  style={{ color: "var(--fg-subtle)", letterSpacing: "0.02em" }}
                >
                  · last checked{" "}
                  {new Date(
                    domain.last_checked_at || Date.now(),
                  ).toLocaleString()}
                </span>
              )}
            </p>
            <button
              onClick={handleVerify}
              disabled={verifying}
              className="text-[11px] px-2 py-0.5 transition disabled:opacity-50"
              style={{
                color: "var(--fg-muted)",
                border: "1px solid var(--border-sub)",
                background: "var(--bg-elev)",
                borderRadius: "var(--r-sm)",
              }}
            >
              {verifying ? "Probing…" : "Re-check"}
            </button>
          </div>

          <div className="space-y-2">
            <div className="flex items-center gap-2 flex-wrap">
              <span
                className="font-mono text-[11px] px-2 py-0.5"
                style={{
                  background: "var(--bg-elev)",
                  border: "1px solid var(--border-sub)",
                  borderRadius: "var(--r-sm)",
                  color: "var(--fg)",
                  minWidth: 36,
                  textAlign: "center",
                }}
              >
                MX
              </span>
              <span className="text-[12px]" style={{ color: "var(--fg-muted)" }}>
                Route email to e2a
              </span>
              <span className="flex-1" />
              <RecordStatusChip status={probe?.mx} />
            </div>
            <DNSRecord
              type=""
              label=""
              fields={[
                { label: "Name", value: domain.domain },
                { label: "Mail server", value: domain.dns_records.mx.value },
                {
                  label: "Priority",
                  value: String(domain.dns_records.mx.priority),
                },
              ]}
            />
          </div>

          <div className="space-y-2">
            <div className="flex items-center gap-2 flex-wrap">
              <span
                className="font-mono text-[11px] px-2 py-0.5"
                style={{
                  background: "var(--bg-elev)",
                  border: "1px solid var(--border-sub)",
                  borderRadius: "var(--r-sm)",
                  color: "var(--fg)",
                  minWidth: 36,
                  textAlign: "center",
                }}
              >
                TXT
              </span>
              <span className="text-[12px]" style={{ color: "var(--fg-muted)" }}>
                Prove domain ownership (also drives SPF check)
              </span>
              <span className="flex-1" />
              <RecordStatusChip status={probe?.spf} />
            </div>
            <DNSRecord
              type=""
              label=""
              fields={[
                { label: "Name", value: domain.domain },
                { label: "Content", value: domain.verification_token },
              ]}
            />
          </div>

          {/* DKIM row — only present once the backend has a stored
              keypair for this domain. The TXT name is the per-domain
              selector at "{selector}._domainkey.{domain}" and the
              value contains the base64 public key. */}
          {domain.dns_records.dkim?.host && (
            <div className="space-y-2">
              <div className="flex items-center gap-2 flex-wrap">
                <span
                  className="font-mono text-[11px] px-2 py-0.5"
                  style={{
                    background: "var(--bg-elev)",
                    border: "1px solid var(--border-sub)",
                    borderRadius: "var(--r-sm)",
                    color: "var(--fg)",
                    minWidth: 36,
                    textAlign: "center",
                  }}
                >
                  TXT
                </span>
                <span
                  className="text-[12px]"
                  style={{ color: "var(--fg-muted)" }}
                >
                  Authenticate outbound mail (DKIM)
                </span>
                <span className="flex-1" />
                <RecordStatusChip status={probe?.dkim} />
              </div>
              <DNSRecord
                type=""
                label=""
                fields={[
                  { label: "Name", value: domain.dns_records.dkim.host },
                  { label: "Content", value: domain.dns_records.dkim.value },
                ]}
              />
            </div>
          )}

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
