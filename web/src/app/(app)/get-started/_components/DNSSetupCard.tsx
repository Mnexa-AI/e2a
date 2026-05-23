"use client";

import { useEffect, useRef } from "react";
import { DNSRecord } from "../../../components/Field";
import type { DomainInfo } from "../../../components/onboarding/types";
import { track } from "../../../components/onboarding/analytics";

export function DNSSetupCard({
  domain,
}: {
  domain: DomainInfo;
}) {
  const tracked = useRef(false);
  useEffect(() => {
    if (!tracked.current) {
      track("dns_instructions_viewed", { domain: domain.domain });
      tracked.current = true;
    }
  }, [domain.domain]);

  return (
    <div>
      <h2
        className="mb-2"
        style={{
          fontFamily: "var(--f-editorial)",
          fontWeight: 400,
          fontSize: 28,
          letterSpacing: "-0.01em",
          color: "var(--fg)",
        }}
      >
        Configure DNS records
      </h2>
      <p className="mb-7 text-[14px]" style={{ color: "var(--fg-muted)" }}>
        Add these records to{" "}
        <code
          className="font-mono text-[12px] px-1.5 py-0.5"
          style={{
            background: "var(--bg-elev)",
            border: "1px solid var(--border-sub)",
            borderRadius: "var(--r-sm)",
            color: "var(--fg)",
          }}
        >
          {domain.domain}
        </code>
        &apos;s DNS to prove ownership and route email to e2a.
      </p>
      <div className="space-y-6">
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
        {domain.dns_records.dkim?.host && (
          <DNSRecord
            type="TXT"
            label="Authenticate outbound mail (DKIM)"
            fields={[
              { label: "Name", value: domain.dns_records.dkim.host },
              { label: "Content", value: domain.dns_records.dkim.value },
            ]}
          />
        )}
      </div>
      <div
        className="mt-6 p-4 text-[13px]"
        style={{
          background: "var(--warn-bg)",
          color: "var(--warn-strong)",
          border: "1px solid var(--warn-bg)",
          borderRadius: "var(--r-md)",
        }}
      >
        DNS changes can take a few minutes to propagate. Wait a bit before verifying.
      </div>
    </div>
  );
}
