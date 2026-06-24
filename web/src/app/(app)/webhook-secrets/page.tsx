"use client";

// Back-compat redirect: /webhook-secrets was renamed to /webhooks.
import { useEffect } from "react";
import { useRouter } from "next/navigation";

export default function WebhookSecretsRedirect() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/webhooks");
  }, [router]);
  return null;
}
