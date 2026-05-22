"use client";

import { useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import { listDomains } from "../../components/onboarding/api";
import { track } from "../../components/onboarding/analytics";
import { PageShell } from "../../components/loft/PageShell";
import { AddressChoice } from "./_components/AddressChoice";
import { SharedAgentForm } from "./_components/SharedAgentForm";
import { CustomDomainChecklist } from "./_components/CustomDomainChecklist";
import { SuccessPanel } from "./_components/SuccessPanel";
import type { AddressType, AgentMode } from "../../components/onboarding/types";
import type { DomainInfo } from "../../components/onboarding/types";
import type { AgentData } from "../../components/types";

type Step = "choose" | "shared_form" | "custom_checklist" | "success";

const PAGE_HEADER = {
  eyebrow: "Onboarding · est. 3 minutes",
  title: (
    <>
      Wire up your first{" "}
      <em style={{ color: "var(--accent-strong)" }}>agent inbox.</em>
    </>
  ),
  subtitle:
    "Pick how your agent gets mail, then point e2a at the place your code is running. You can change all of this later from the dashboard.",
};

export default function GetStartedPage() {
  const searchParams = useSearchParams();
  const initialMode = searchParams.get("mode") === "shared" ? "shared" : null;
  const initialDomain = searchParams.get("domain");

  const [step, setStep] = useState<Step>(
    initialMode === "shared" ? "shared_form" : "choose",
  );
  const [addressType, setAddressType] = useState<AddressType | null>(
    initialMode === "shared" ? "shared" : null,
  );
  const [agent, setAgent] = useState<AgentData | null>(null);
  const [agentMode, setAgentMode] = useState<AgentMode>("local");
  const [webhookUrl, setWebhookUrl] = useState("");
  const [domainData, setDomainData] = useState<DomainInfo | null>(null);
  const [error, setError] = useState("");
  const [bootstrapping, setBootstrapping] = useState(true);

  useEffect(() => {
    let cancelled = false;

    async function bootstrap() {
      setError("");
      setAgent(null);

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
          setError(
            err instanceof Error
              ? err.message
              : "Failed to load onboarding state",
          );
        } finally {
          if (!cancelled) setBootstrapping(false);
        }
        return;
      }

      if (initialMode === "shared") {
        setStep("shared_form");
        setAddressType("shared");
        setBootstrapping(false);
        return;
      }

      setStep("choose");
      setBootstrapping(false);
    }

    bootstrap();
    return () => {
      cancelled = true;
    };
  }, [initialDomain, initialMode]);

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
      <PageShell crumbs={["Get started"]}>
        <p
          className="py-10 text-center text-[13px]"
          style={{ color: "var(--fg-muted)" }}
        >
          Loading onboarding...
        </p>
      </PageShell>
    );
  }

  return (
    <PageShell
      crumbs={["Get started"]}
      eyebrow={PAGE_HEADER.eyebrow}
      title={PAGE_HEADER.title}
      subtitle={PAGE_HEADER.subtitle}
      maxWidth={880}
      editorial
    >
      {step === "choose" && (
        <>
          <AddressChoice
            selected={addressType}
            onSelect={handleAddressChoice}
          />
          {error && (
            <div
              className="mt-6 p-3 text-[13px]"
              style={{
                background: "var(--danger-bg)",
                border: "1px solid var(--danger-bg)",
                color: "var(--danger-strong)",
                borderRadius: "var(--r-md)",
              }}
            >
              {error}
            </div>
          )}
        </>
      )}

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

      {step === "success" && agent && (
        <SuccessPanel
          agent={agent}
          mode={agentMode}
          webhookUrl={webhookUrl || undefined}
        />
      )}
    </PageShell>
  );
}
