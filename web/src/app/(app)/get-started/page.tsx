"use client";

import { useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import { listDomains } from "../../components/onboarding/api";
import { track } from "../../components/onboarding/analytics";
import { AddressChoice } from "./_components/AddressChoice";
import { SharedAgentForm } from "./_components/SharedAgentForm";
import { CustomDomainChecklist } from "./_components/CustomDomainChecklist";
import { SuccessPanel } from "./_components/SuccessPanel";
import type { AddressType, AgentMode } from "../../components/onboarding/types";
import type { DomainInfo } from "../../components/onboarding/types";
import type { AgentData } from "../../components/types";

// ── Page steps ───────────────────────────────────────────

type Step = "choose" | "shared_form" | "custom_checklist" | "success";

// ── Page component ───────────────────────────────────────

export default function GetStartedPage() {
  const searchParams = useSearchParams();
  const initialMode = searchParams.get("mode") === "shared" ? "shared" : null;
  const initialDomain = searchParams.get("domain");

  const [step, setStep] = useState<Step>(initialMode === "shared" ? "shared_form" : "choose");
  const [addressType, setAddressType] = useState<AddressType | null>(
    initialMode === "shared" ? "shared" : null,
  );
  const [agent, setAgent] = useState<AgentData | null>(null);
  const [agentMode, setAgentMode] = useState<AgentMode>("local");
  const [webhookUrl, setWebhookUrl] = useState("");
  const [domainData, setDomainData] = useState<DomainInfo | null>(null);
  const [error, setError] = useState("");
  const [bootstrapping, setBootstrapping] = useState(true);

  // Bootstrap: check query params and existing state
  useEffect(() => {
    let cancelled = false;

    async function bootstrap() {
      setError("");
      setAgent(null);

      // ?domain= deep link — go straight to that domain's checklist
      if (initialDomain) {
        setAddressType("custom");
        try {
          const domains = await listDomains();
          if (cancelled) return;

          const matchedDomain = domains.find((d) => d.domain === initialDomain);
          if (!matchedDomain) {
            setDomainData(null);
            setStep("choose");
            setAddressType(null);
            setError(`Domain ${initialDomain} not found in your account`);
          } else {
            setDomainData(matchedDomain);
            setStep("custom_checklist");
          }
        } catch (err) {
          if (cancelled) return;
          setDomainData(null);
          setStep("choose");
          setAddressType(null);
          setError(err instanceof Error ? err.message : "Failed to load onboarding state");
        } finally {
          if (!cancelled) setBootstrapping(false);
        }
        return;
      }

      // ?mode=shared — skip to shared form
      if (initialMode === "shared") {
        setStep("shared_form");
        setAddressType("shared");
        setBootstrapping(false);
        return;
      }

      // Plain /get-started — always show the address-type chooser
      setStep("choose");
      setBootstrapping(false);
    }

    bootstrap();
    return () => { cancelled = true; };
  }, [initialDomain, initialMode]);

  // Address type selection handler
  const handleAddressChoice = (type: AddressType) => {
    setAddressType(type);
    setError("");
    track("address_type_selected", { type });
    if (type === "shared") {
      setStep("shared_form");
    } else {
      setStep("custom_checklist");
    }
  };


  if (bootstrapping) {
    return (
      <div className="py-12 text-center text-sm text-muted">
        Loading onboarding...
      </div>
    );
  }

  return (
    <>
      {/* Step 1: Choose address type */}
      {step === "choose" && (
        <>
          <AddressChoice selected={addressType} onSelect={handleAddressChoice} />
          {error && (
            <div className="mt-6 p-3 bg-red-50 border border-red-200 rounded-lg text-sm text-red-700">
              {error}
            </div>
          )}
        </>
      )}

      {/* Shared-domain flow */}
      {step === "shared_form" && (
        <SharedAgentForm
          onCreated={(agentData, mode, wh) => {
            setAgent(agentData);
            setAgentMode(mode);
            setWebhookUrl(wh);
            setStep("success");
          }}
        />
      )}

      {/* Custom-domain checklist flow */}
      {step === "custom_checklist" && (
        <CustomDomainChecklist
          initialDomain={domainData}
          onComplete={(agentData, mode, wh) => {
            setAgent(agentData);
            setAgentMode(mode);
            setWebhookUrl(wh);
            setStep("success");
          }}
        />
      )}

      {/* Success — mode-aware */}
      {step === "success" && agent && (
        <SuccessPanel agent={agent} mode={agentMode} webhookUrl={webhookUrl || undefined} />
      )}
    </>
  );
}
