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
      <h2 className="text-2xl font-bold tracking-tight mb-2">Configure DNS records</h2>
      <p className="text-muted mb-8">
        Add these records to{" "}
        <code className="text-xs bg-surface px-1.5 py-0.5 rounded border border-border">{domain.domain}</code>
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
      </div>
      <div className="mt-6 p-4 bg-amber-50 border border-amber-200 rounded-lg text-sm text-amber-800">
        DNS changes can take a few minutes to propagate. Wait a bit before verifying.
      </div>
    </div>
  );
}
