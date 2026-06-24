"use client";

// Back-compat redirect: /dashboard (the old Inboxes home) was renamed to
// /inboxes during the route-IA normalization.
import { useEffect } from "react";
import { useRouter } from "next/navigation";

export default function DashboardRedirect() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/inboxes");
  }, [router]);
  return null;
}
