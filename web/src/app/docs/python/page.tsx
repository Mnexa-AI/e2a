"use client";

import { useEffect } from "react";

export default function PythonSDKPage() {
  useEffect(() => {
    window.location.replace("https://pypi.org/project/e2a/");
  }, []);

  return (
    <div
      className="flex items-center justify-center h-screen text-[13px]"
      style={{ background: "var(--bg)", color: "var(--fg-muted)" }}
    >
      Redirecting to PyPI...
    </div>
  );
}
