"use client";

import { useState } from "react";
import { DNSRecord } from "../../../components/Field";
import { Chip } from "../../../components/loft/Chip";
import { Dot } from "../../../components/loft/Dot";
import {
  verifyDomain,
  deleteDomain,
} from "../../../components/onboarding/api";
import type {
  DomainInfo,
  DomainSendingStatus,
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

// SendingStatusChip reflects the async SES sending-identity verification for
// the WHOLE domain (all-or-nothing: DKIM + custom MAIL FROM together), not a
// single record — so it lives in the section header, not per-row. Mirrors the
// `sending_status` column. Unknown values fall through to neutral (open set).
function SendingStatusChip({
  status,
}: {
  status: DomainSendingStatus | undefined;
}) {
  if (status === "verified")
    return (
      <Chip tone="success">
        <Dot tone="success" />
        Sending enabled
      </Chip>
    );
  if (status === "failed")
    return (
      <Chip tone="danger">
        <Dot tone="danger" />
        Failed
      </Chip>
    );
  // pending OR any unknown/future value: the chip only renders when records
  // exist (provisioned), so a neutral in-progress label reads truer than
  // "Not set up". "verified"/"failed" are handled above.
  return <Chip tone="info">Verifying…</Chip>;
}

// splitMX parses the backend's combined "10 host.example.com" MX value into the
// (priority, host) pair most DNS providers ask for as separate fields. Returns
// null if the value isn't "<priority> <host>" so the caller can fall back to a
// single Content field rather than fabricating a priority.
function splitMX(value: string): { priority: string; host: string } | null {
  const m = value.match(/^\s*(\d+)\s+(.+?)\.?\s*$/);
  return m ? { priority: m[1], host: m[2] } : null;
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
  // Cached per-record diagnostic from the most recent verify probe. Until
  // the user clicks "View DNS records" + retries, this is null and the
  // chips render as "—" placeholders.
  const [probe, setProbe] = useState<VerifyDomainResponse | null>(null);

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
          </div>
          <p
            className="text-[12px]"
            style={{ color: "var(--fg-muted)" }}
          >
            {agentCount === 0
              ? "No inboxes"
              : agentCount === 1
                ? "1 inbox"
                : `${agentCount} inboxes`}
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
              Create inbox
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
          most recent verify probe. The DKIM row renders for domains
          with a stored keypair; pre-migration domains have no `dkim`
          block in dns_records and the row is omitted. */}
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

          {/* Outbound sending (decision 4 / Slice 4). Rendered only when the
              backend has provisioned a sending identity (sending_dns_records
              present) — so when the feature is gated off (ses_region unset),
              this whole block is absent and the card is unchanged. These are
              the custom MAIL FROM subdomain's MX + SPF; the DKIM record above
              is reused via BYODKIM. SES verifies them as a unit, surfaced by
              the single status chip (no per-record probe). */}
          {(domain.sending_dns_records?.length ?? 0) > 0 && (
            <div
              className="space-y-3 pt-4"
              style={{ borderTop: "1px solid var(--border)" }}
            >
              <div className="flex items-center gap-2 flex-wrap">
                <p
                  className="font-mono text-[10px] uppercase font-semibold"
                  style={{
                    color: "var(--fg-subtle)",
                    letterSpacing: "0.08em",
                  }}
                >
                  Outbound sending
                </p>
                <SendingStatusChip status={domain.sending_status} />
              </div>
              <p className="text-[12px]" style={{ color: "var(--fg-muted)" }}>
                Publish these so mail sends as{" "}
                <code className="font-mono">@{domain.domain}</code> with no
                “via e2a”. The DKIM record above is also required.
              </p>

              {domain.sending_status === "failed" && domain.sending_error && (
                <div
                  className="p-3 text-[12px]"
                  style={{
                    background: "var(--danger-bg)",
                    color: "var(--danger-strong)",
                    border: "1px solid var(--danger-bg)",
                    borderRadius: "var(--r-md)",
                  }}
                >
                  {domain.sending_error}
                </div>
              )}

              {domain.sending_dns_records!.map((rec, i) => {
                const isMX = rec.type.toUpperCase() === "MX";
                const mx = isMX ? splitMX(rec.value) : null;
                return (
                  <div key={`${rec.type}-${rec.name}-${i}`} className="space-y-2">
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
                        {rec.type}
                      </span>
                      <span
                        className="text-[12px]"
                        style={{ color: "var(--fg-muted)" }}
                      >
                        {isMX
                          ? "Return path for bounces (MAIL FROM)"
                          : "Authorize sending (SPF)"}
                      </span>
                    </div>
                    <DNSRecord
                      type=""
                      label=""
                      fields={
                        mx
                          ? [
                              { label: "Name", value: rec.name },
                              { label: "Mail server", value: mx.host },
                              { label: "Priority", value: mx.priority },
                            ]
                          : [
                              { label: "Name", value: rec.name },
                              { label: "Content", value: rec.value },
                            ]
                      }
                    />
                  </div>
                );
              })}
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
