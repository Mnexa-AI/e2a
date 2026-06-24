"use client";

import { PageShell } from "../../components/loft/PageShell";
import { useWorkspace } from "../../components/WorkspaceProvider";

// Stub Workspace page — the real members/invitations management UI is built
// in W3 (§4.6). This stub exists so the new sidebar nav entry doesn't dangle
// over a 404 in the meantime.
export default function WorkspacePage() {
  const { activeWorkspace } = useWorkspace();
  return (
    <PageShell
      crumbs={["Workspace"]}
      eyebrow="Workspace"
      title={activeWorkspace?.name ?? "Workspace"}
      subtitle="Members and invitations management is coming soon."
    >
      <div
        className="px-5 py-8 text-[13px]"
        style={{
          border: "1px solid var(--border)",
          borderRadius: "var(--r-lg)",
          background: "var(--bg-panel)",
          color: "var(--fg-muted)",
        }}
      >
        This workspace’s members and pending invitations will appear here.
      </div>
    </PageShell>
  );
}
