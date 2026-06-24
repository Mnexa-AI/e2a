"use client";

// Invitation accept page (§4.6) — /invite/accept?token=…
//
// The (app) layout auth-gates this route: an unauthenticated visitor sees the
// "Sign in to access this page" gate and returns here after Google OAuth. Once
// signed in, we POST the token to /v1/invitations/{token}/accept and handle the
// three designed outcomes:
//
//   200 → joined: switch the active workspace into the joined one and route to
//         /workspace with a success state.
//   403 → email mismatch: the signed-in Google account differs from the invited
//         email; tell the user who they're signed in as and to switch accounts.
//   410 → gone: the invite was revoked, expired, or its workspace was torn down;
//         show an expired-invite state with a path back to /dashboard.

import { Suspense, useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { PageShell } from "../../../components/loft/PageShell";
import { useAuth } from "../../../components/AuthProvider";
import { useWorkspace } from "../../../components/WorkspaceProvider";
import { acceptInvitation, ApiError } from "../../../components/onboarding/api";
import type { Workspace } from "../../../components/types";

type Phase =
  | { kind: "idle" }
  | { kind: "accepting" }
  | { kind: "joined"; workspace: Workspace }
  | { kind: "mismatch"; message: string; signedInAs: string }
  | { kind: "gone"; message: string }
  | { kind: "error"; message: string }
  | { kind: "missing" };

// Pulls a human message + the signed-in address out of the error envelope the
// accept endpoint returns. The body is the e2a envelope
// ({"error":{"message":…,"details":{"signed_in_as":…}}}); fall back to the raw
// text when it isn't JSON.
function parseEnvelope(e: ApiError): { message: string; signedInAs: string } {
  let message = e.message;
  let signedInAs = "";
  try {
    const parsed = JSON.parse(e.message) as {
      error?: { message?: string; details?: { signed_in_as?: string } };
      message?: string;
      detail?: string;
    };
    if (parsed.error?.message) message = parsed.error.message;
    else if (parsed.message) message = parsed.message;
    else if (parsed.detail) message = parsed.detail;
    if (parsed.error?.details?.signed_in_as) {
      signedInAs = parsed.error.details.signed_in_as;
    }
  } catch {
    // not JSON — keep the raw text
  }
  return { message, signedInAs };
}

function AcceptInner() {
  const router = useRouter();
  const params = useSearchParams();
  const token = params.get("token") ?? "";
  const { user } = useAuth();
  const { enterWorkspace } = useWorkspace();

  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  // Accept is a one-shot mutation; guard against React 18 strict-mode double
  // invocation and re-renders so we never POST the token twice.
  const attempted = useRef(false);

  const accept = useCallback(async () => {
    if (!token) {
      setPhase({ kind: "missing" });
      return;
    }
    setPhase({ kind: "accepting" });
    try {
      const ws = await acceptInvitation(token);
      // Land in the joined workspace before routing so the destination page
      // resolves under the right tenant.
      await enterWorkspace(ws.id, ws.role);
      setPhase({ kind: "joined", workspace: ws });
    } catch (e) {
      if (e instanceof ApiError) {
        const { message, signedInAs } = parseEnvelope(e);
        if (e.status === 403) {
          setPhase({ kind: "mismatch", message, signedInAs });
          return;
        }
        if (e.status === 410) {
          setPhase({ kind: "gone", message });
          return;
        }
        setPhase({ kind: "error", message });
        return;
      }
      setPhase({
        kind: "error",
        message: e instanceof Error ? e.message : "Something went wrong accepting this invitation.",
      });
    }
  }, [token, enterWorkspace]);

  // Auto-accept once the user is known. The (app) layout has already gated on
  // auth, so reaching here means we're signed in.
  useEffect(() => {
    if (!user) return;
    if (attempted.current) return;
    attempted.current = true;
    void accept();
  }, [user, accept]);

  // Once joined, bounce to the workspace page after a beat so the success
  // state is visible.
  useEffect(() => {
    if (phase.kind !== "joined") return;
    const t = setTimeout(() => router.push("/workspace"), 1200);
    return () => clearTimeout(t);
  }, [phase, router]);

  const signedInAs = phase.kind === "mismatch" && phase.signedInAs
    ? phase.signedInAs
    : user?.email ?? "";

  return (
    <PageShell
      crumbs={["Workspace", "Accept invitation"]}
      eyebrow="Workspace"
      title="Accept invitation"
      subtitle="Join a workspace you've been invited to."
      maxWidth={620}
    >
      {(phase.kind === "idle" || phase.kind === "accepting") && (
        <StatusPanel>
          <p className="text-[14px]" style={{ color: "var(--fg-muted)" }}>
            Accepting your invitation…
          </p>
        </StatusPanel>
      )}

      {phase.kind === "joined" && (
        <>
          <Banner tone="success">
            You&apos;ve joined{" "}
            <strong>{phase.workspace.name}</strong>. Taking you to the workspace…
          </Banner>
          <StatusPanel>
            <p className="text-[14px] mb-4" style={{ color: "var(--fg-muted)" }}>
              You&apos;re now a {phase.workspace.role ?? "member"} of{" "}
              <strong style={{ color: "var(--fg)" }}>{phase.workspace.name}</strong>.
            </p>
            <Link href="/workspace" className="accent-btn">
              Go to workspace
            </Link>
          </StatusPanel>
        </>
      )}

      {phase.kind === "mismatch" && (
        <>
          <Banner tone="danger">{phase.message}</Banner>
          <StatusPanel>
            <p className="text-[14px] mb-3 leading-[1.6]" style={{ color: "var(--fg-muted)" }}>
              You&apos;re signed in as{" "}
              <strong style={{ color: "var(--fg)" }}>{signedInAs || "this account"}</strong>
              , but this invite is for a different email address. Switch to the
              invited account, then open the invite link again.
            </p>
            <div className="flex flex-wrap gap-2">
              <SwitchAccountButton />
              <Link href="/dashboard" className="ghost-btn">
                Back to dashboard
              </Link>
            </div>
          </StatusPanel>
        </>
      )}

      {phase.kind === "gone" && (
        <>
          <Banner tone="warn">This invitation is no longer valid.</Banner>
          <StatusPanel>
            <p className="text-[14px] mb-4 leading-[1.6]" style={{ color: "var(--fg-muted)" }}>
              {phase.message ||
                "This invitation has expired, was revoked, or its workspace was removed. Ask a workspace admin to send you a fresh invite."}
            </p>
            <Link href="/dashboard" className="accent-btn">
              Back to dashboard
            </Link>
          </StatusPanel>
        </>
      )}

      {phase.kind === "missing" && (
        <>
          <Banner tone="warn">No invitation token.</Banner>
          <StatusPanel>
            <p className="text-[14px] mb-4 leading-[1.6]" style={{ color: "var(--fg-muted)" }}>
              This link is missing its invitation token. Open the accept link
              from your invitation email again, or ask an admin to re-send it.
            </p>
            <Link href="/dashboard" className="accent-btn">
              Back to dashboard
            </Link>
          </StatusPanel>
        </>
      )}

      {phase.kind === "error" && (
        <>
          <Banner tone="danger">Couldn&apos;t accept this invitation.</Banner>
          <StatusPanel>
            <p className="text-[14px] mb-4 leading-[1.6]" style={{ color: "var(--fg-muted)" }}>
              {phase.message || "Something went wrong. Try the link again in a moment."}
            </p>
            <div className="flex flex-wrap gap-2">
              <button
                type="button"
                onClick={() => {
                  attempted.current = false;
                  void accept();
                }}
                className="accent-btn"
              >
                Try again
              </button>
              <Link href="/dashboard" className="ghost-btn">
                Back to dashboard
              </Link>
            </div>
          </StatusPanel>
        </>
      )}

      <style jsx>{`
        :global(.accent-btn) {
          display: inline-flex;
          align-items: center;
          padding: 8px 16px;
          font-size: 13px;
          font-weight: 500;
          background: var(--accent-fill);
          color: var(--accent-fg);
          border-radius: var(--r-md);
          transition: background 0.15s;
        }
        :global(.ghost-btn) {
          display: inline-flex;
          align-items: center;
          padding: 8px 16px;
          font-size: 13px;
          font-weight: 500;
          background: var(--bg-panel);
          color: var(--fg);
          border: 1px solid var(--border);
          border-radius: var(--r-md);
        }
      `}</style>
    </PageShell>
  );
}

// "Switch account" re-runs the Google OAuth login; returning here re-attempts
// the accept under the freshly signed-in identity.
function SwitchAccountButton() {
  return (
    <a href="/api/auth/login" className="accent-btn">
      Switch Google account
    </a>
  );
}

function StatusPanel({ children }: { children: React.ReactNode }) {
  return (
    <div
      className="p-6"
      style={{
        background: "var(--bg-panel)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-lg)",
      }}
    >
      {children}
    </div>
  );
}

function Banner({
  tone,
  children,
}: {
  tone: "danger" | "success" | "warn";
  children: React.ReactNode;
}) {
  const palette =
    tone === "danger"
      ? { bg: "var(--danger-bg)", fg: "var(--danger-strong)" }
      : tone === "success"
        ? { bg: "var(--success-bg)", fg: "var(--success)" }
        : { bg: "var(--warn-bg)", fg: "var(--warn-strong)" };
  return (
    <div
      className="mb-5 p-3 text-[13px] leading-[1.5]"
      style={{
        background: palette.bg,
        color: palette.fg,
        border: `1px solid ${palette.bg}`,
        borderRadius: "var(--r-md)",
      }}
    >
      {children}
    </div>
  );
}

export default function AcceptInvitationPage() {
  // useSearchParams requires a Suspense boundary under the App Router /
  // static export.
  return (
    <Suspense fallback={null}>
      <AcceptInner />
    </Suspense>
  );
}
