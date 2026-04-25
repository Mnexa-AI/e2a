"use client";

import { useEffect } from "react";

export default function DocsPage() {
  useEffect(() => {
    window.location.replace("/scalar.html");
  }, []);

  return (
    <div className="flex items-center justify-center h-screen text-muted text-sm">
      Loading API docs...
    </div>
  );
}
