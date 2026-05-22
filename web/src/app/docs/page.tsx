"use client";

import { useEffect } from "react";

export default function DocsPage() {
  useEffect(() => {
    window.location.replace("/scalar.html");
  }, []);

  return (
    <div
      className="flex items-center justify-center h-screen text-[13px]"
      style={{ background: "var(--bg)", color: "var(--fg-muted)" }}
    >
      Loading API docs...
    </div>
  );
}
