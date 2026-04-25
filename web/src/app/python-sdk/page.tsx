"use client";

import { useEffect } from "react";

export default function PythonSDKPage() {
  useEffect(() => {
    window.location.replace("https://pypi.org/project/e2a/");
  }, []);

  return (
    <div className="flex items-center justify-center h-screen text-muted text-sm">
      Redirecting to PyPI...
    </div>
  );
}
