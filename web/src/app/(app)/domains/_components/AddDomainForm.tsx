"use client";

import { useState } from "react";
import { Field } from "../../../components/Field";
import { isValidDomain } from "../../../components/onboarding/state";
import { registerDomain } from "../../../components/onboarding/api";
import { track } from "../../../components/onboarding/analytics";

export function AddDomainForm({
  onRegistered,
}: {
  onRegistered: () => void;
}) {
  const [domain, setDomain] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");

    if (!isValidDomain(domain)) {
      setError("Enter a valid domain (e.g. mail.yourcompany.com)");
      return;
    }

    setLoading(true);
    track("domain_registration_started", { domain });
    try {
      await registerDomain(domain);
      track("domain_registration_succeeded", { domain });
      setDomain("");
      onRegistered();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to register domain");
    } finally {
      setLoading(false);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="border border-border rounded-lg p-4 space-y-4">
      <p className="text-sm font-medium">Add a new domain</p>
      {error && (
        <div className="p-3 bg-red-50 border border-red-200 rounded-lg text-sm text-red-700">
          {error}
        </div>
      )}
      <Field
        label="Domain"
        placeholder="mail.yourcompany.com"
        value={domain}
        onChange={(v) => setDomain(v.toLowerCase())}
        hint="Use a subdomain you control. All emails to *@this-domain will be routed to e2a."
      />
      <button
        type="submit"
        disabled={loading || !domain}
        className="w-full bg-foreground text-background py-2.5 rounded-lg text-sm font-medium hover:opacity-90 transition disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {loading ? "Registering..." : "Register domain"}
      </button>
    </form>
  );
}
