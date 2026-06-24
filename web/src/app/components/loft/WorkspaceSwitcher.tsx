"use client";

// WorkspaceSwitcher — the active-workspace selector that sits at the top of
// the sidebar, above the user card (§4.2). Lists the workspaces the human
// session belongs to, each with its role pill; clicking a row flips the
// active workspace via the WorkspaceProvider's switchWorkspace().
//
// There is intentionally NO "create workspace" affordance here (v1 scope).
// When the user belongs to a single workspace there's nothing to switch to,
// so we collapse to a static label with no dropdown chrome.

import { useEffect, useRef, useState } from "react";
import { useWorkspace } from "../WorkspaceProvider";
import { Chip } from "./Chip";
import type { WorkspaceRole } from "../types";

function RolePill({ role }: { role?: WorkspaceRole | null }) {
  if (!role) return null;
  return (
    <Chip tone={role === "admin" ? "accent" : "neutral"}>
      {role === "admin" ? "Admin" : "Member"}
    </Chip>
  );
}

function CheckIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <polyline points="20 6 9 17 4 12" />
    </svg>
  );
}

function ChevronIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <polyline points="6 9 12 15 18 9" />
    </svg>
  );
}

export function WorkspaceSwitcher() {
  const { workspaces, activeWorkspace, role, loading, switchWorkspace } =
    useWorkspace();
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  // Close on outside click / Escape.
  useEffect(() => {
    if (!open) return;
    function onClick(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  // Nothing resolved yet (or no workspace) — render nothing rather than an
  // empty shell. whoami/list seed quickly and the rest of the chrome paints
  // independently.
  if (loading || !activeWorkspace) return null;

  const single = workspaces.length <= 1;

  // Single-workspace: a static, non-interactive label — no dropdown chrome.
  if (single) {
    return (
      <div className="px-3 pt-3">
        <div
          className="flex items-center gap-2 px-3 py-2"
          style={{
            border: "1px solid var(--border-sub)",
            borderRadius: "var(--r-md)",
          }}
        >
          <div className="flex-1 min-w-0">
            <div
              className="text-[12px] font-medium truncate"
              style={{ color: "var(--fg)" }}
            >
              {activeWorkspace.name}
            </div>
          </div>
          <RolePill role={role ?? activeWorkspace.role} />
        </div>
      </div>
    );
  }

  return (
    <div ref={rootRef} className="relative px-3 pt-3">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="listbox"
        aria-expanded={open}
        className="flex w-full items-center gap-2 px-3 py-2 text-left transition"
        style={{
          border: "1px solid var(--border-sub)",
          borderRadius: "var(--r-md)",
          background: open ? "var(--bg-elev)" : "transparent",
        }}
      >
        <div className="flex-1 min-w-0">
          <div
            className="text-[12px] font-medium truncate"
            style={{ color: "var(--fg)" }}
          >
            {activeWorkspace.name}
          </div>
        </div>
        <RolePill role={role ?? activeWorkspace.role} />
        <span style={{ color: "var(--fg-muted)" }}>
          <ChevronIcon />
        </span>
      </button>

      {open && (
        <div
          role="listbox"
          className="absolute left-3 right-3 z-20 mt-1 py-1"
          style={{
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-lg)",
            boxShadow: "0 8px 24px rgba(0,0,0,0.18)",
          }}
        >
          {workspaces.map((w) => {
            const active = w.id === activeWorkspace.id;
            return (
              <button
                key={w.id}
                type="button"
                role="option"
                aria-selected={active}
                onClick={() => {
                  switchWorkspace(w.id);
                  setOpen(false);
                }}
                className="flex w-full items-center gap-2 px-3 py-2 text-left transition"
                style={{ background: "transparent" }}
              >
                <span
                  className="shrink-0"
                  style={{
                    width: 14,
                    color: active ? "var(--accent-strong)" : "transparent",
                  }}
                >
                  <CheckIcon />
                </span>
                <span
                  className="flex-1 min-w-0 text-[12px] truncate"
                  style={{
                    color: "var(--fg)",
                    fontWeight: active ? 500 : 400,
                  }}
                >
                  {w.name}
                </span>
                <RolePill role={w.role} />
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
