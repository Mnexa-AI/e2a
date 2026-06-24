"use client";

// Back-compat redirect: /dashboard/pending was renamed to /reviews.
import { Suspense, useEffect } from "react";
import { useRouter, useSearchParams } from "next/navigation";

function Redir() {
  const router = useRouter();
  const id = useSearchParams().get("id");
  useEffect(() => {
    router.replace(id ? `/reviews?id=${encodeURIComponent(id)}` : "/reviews");
  }, [router, id]);
  return null;
}
export default function PendingRedirect() {
  return <Suspense fallback={null}><Redir /></Suspense>;
}
