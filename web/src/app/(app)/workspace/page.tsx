"use client";

import { useMemo, useState } from "react";
import useSWR from "swr";
import { PageShell } from "../../components/loft/PageShell";
import { Chip } from "../../components/loft/Chip";
import { useAuth } from "../../components/AuthProvider";
import { useWorkspace } from "../../components/WorkspaceProvider";
import {
  listMembers,
  listInvitations,
  renameWorkspace,
  setMemberRole,
  removeMember,
  createInvitation,
  revokeInvitation,
  ApiError,
} from "../../components/onboarding/api";
import type {
  WorkspaceMember,
  WorkspaceRole,
  Invitation,
} from "../../components/types";
import {
  membersKey,
  invitationsKey,
  invalidateMembers,
  invalidateInvitations,
  invalidateWorkspaces,
} from "../../../lib/swrKeys";

// Friendly message for an ApiError, with special-casing for the two 409s the
// workspace surface can raise: last_admin (can't demote/remove/leave the only
// admin) and already_member (inviting someone who's already in). The server's
// envelope body text already carries a human sentence, so prefer it; the
// status check is a fallback so an opaque body still reads sensibly.
function errMessage(e: unknown, fallback: string): string {
  if (e instanceof ApiError) {
    if (e.message) {
      // Envelope body may be JSON ({"detail":"…"}) or plain text.
      try {
        const parsed = JSON.parse(e.message) as { detail?: string; title?: string };
        if (parsed.detail) return parsed.detail;
        if (parsed.title) return parsed.title;
      } catch {
        // not JSON — fall through to the raw text
      }
      return e.message;
    }
    if (e.status === 409) return "That change conflicts with the workspace's current state.";
    return `${fallback} (HTTP ${e.status}).`;
  }
  if (e instanceof Error && e.message) return e.message;
  return fallback;
}

function initialsOf(m: WorkspaceMember): string {
  const src = (m.name || m.email || "").trim();
  if (!src) return "?";
  const parts = src.split(/\s+/).filter(Boolean);
  if (parts.length >= 2) {
    return (parts[0][0] + parts[1][0]).toUpperCase();
  }
  return src.slice(0, 2).toUpperCase();
}

function formatDate(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "—";
  return d.toLocaleDateString();
}

export default function WorkspacePage() {
  const { user } = useAuth();
  const { activeWorkspace, role, loading: wsLoading, refresh } = useWorkspace();
  const wsId = activeWorkspace?.id ?? null;
  const isAdmin = role === "admin";

  // Members — readable by any live member. Keyed by workspace id so two
  // workspaces don't share a cache.
  const {
    data: members = [],
    error: membersError,
    isLoading: membersLoading,
  } = useSWR<WorkspaceMember[]>(
    wsId ? membersKey(wsId) : null,
    () => listMembers(wsId as string),
  );

  // Pending invitations — admin-only endpoint, so only fetch when admin.
  const {
    data: invitations = [],
    isLoading: invitationsLoading,
  } = useSWR<Invitation[]>(
    wsId && isAdmin ? invitationsKey(wsId) : null,
    () => listInvitations(wsId as string),
  );

  // ── Rename (admin-only inline affordance) ──────────────
  const [renaming, setRenaming] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const [renameBusy, setRenameBusy] = useState(false);
  const [renameError, setRenameError] = useState("");

  const startRename = () => {
    setRenameValue(activeWorkspace?.name ?? "");
    setRenameError("");
    setRenaming(true);
  };

  const submitRename = async () => {
    if (!wsId) return;
    const name = renameValue.trim();
    if (!name) {
      setRenameError("Enter a workspace name.");
      return;
    }
    setRenameBusy(true);
    setRenameError("");
    try {
      await renameWorkspace(wsId, name);
      await invalidateWorkspaces();
      await refresh();
      setRenaming(false);
    } catch (e) {
      setRenameError(errMessage(e, "Could not rename the workspace"));
    } finally {
      setRenameBusy(false);
    }
  };

  // ── Row-level mutations (admin-only) ───────────────────
  const [rowBusy, setRowBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState("");

  const refreshMembers = async () => {
    if (!wsId) return;
    await invalidateMembers(wsId);
  };

  const handleRoleChange = async (m: WorkspaceMember, next: WorkspaceRole) => {
    if (!wsId || next === m.role) return;
    setRowBusy(m.user_id);
    setActionError("");
    try {
      await setMemberRole(wsId, m.user_id, next);
      await refreshMembers();
    } catch (e) {
      setActionError(errMessage(e, "Could not change the member's role"));
    } finally {
      setRowBusy(null);
    }
  };

  const handleRemove = async (m: WorkspaceMember) => {
    if (!wsId) return;
    const isSelf = m.user_id === user?.id;
    const verb = isSelf ? "leave" : "remove";
    const prompt = isSelf
      ? "Leave this workspace? You'll lose access until you're invited back."
      : `Remove ${m.name || m.email} from this workspace?`;
    if (!confirm(prompt)) return;
    setRowBusy(m.user_id);
    setActionError("");
    try {
      await removeMember(wsId, m.user_id);
      await refreshMembers();
      if (isSelf) {
        // Leaving the active workspace — drop back to the switcher's
        // default and refetch the list.
        await invalidateWorkspaces();
        await refresh();
      }
    } catch (e) {
      setActionError(errMessage(e, `Could not ${verb} this member`));
    } finally {
      setRowBusy(null);
    }
  };

  // ── Invite (toggle-inline form, admin-only) ────────────
  const [showInvite, setShowInvite] = useState(false);
  const [inviteEmail, setInviteEmail] = useState("");
  const [inviteRole, setInviteRole] = useState<WorkspaceRole>("member");
  const [inviteBusy, setInviteBusy] = useState(false);
  const [inviteError, setInviteError] = useState("");
  const [inviteSuccess, setInviteSuccess] = useState("");

  const submitInvite = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!wsId) return;
    const email = inviteEmail.trim();
    if (!email) {
      setInviteError("Enter an email address to invite.");
      return;
    }
    setInviteBusy(true);
    setInviteError("");
    setInviteSuccess("");
    try {
      await createInvitation(wsId, email, inviteRole);
      await invalidateInvitations(wsId);
      setInviteSuccess(`Invitation sent to ${email}.`);
      setInviteEmail("");
      setInviteRole("member");
    } catch (e) {
      setInviteError(errMessage(e, "Could not send the invitation"));
    } finally {
      setInviteBusy(false);
    }
  };

  const handleRevoke = async (inv: Invitation) => {
    if (!wsId) return;
    if (!confirm(`Revoke the invitation for ${inv.email}? Its accept link stops working.`))
      return;
    setRowBusy(inv.id);
    setActionError("");
    try {
      await revokeInvitation(wsId, inv.id);
      await invalidateInvitations(wsId);
    } catch (e) {
      setActionError(errMessage(e, "Could not revoke the invitation"));
    } finally {
      setRowBusy(null);
    }
  };

  // ── Stats ──────────────────────────────────────────────
  const stats = useMemo(() => {
    const admins = members.filter((m) => m.role === "admin").length;
    return {
      members: members.length,
      admins,
      pending: invitations.length,
    };
  }, [members, invitations]);

  const loadError = membersError
    ? errMessage(membersError, "Failed to load members")
    : "";

  // ── Render ─────────────────────────────────────────────
  const title = activeWorkspace ? (
    renaming ? (
      <span className="inline-flex flex-wrap items-center gap-2">
        <input
          autoFocus
          type="text"
          value={renameValue}
          onChange={(e) => setRenameValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") void submitRename();
            if (e.key === "Escape") setRenaming(false);
          }}
          maxLength={100}
          className="px-2 py-1 text-[20px]"
          style={{
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-md)",
            color: "var(--fg)",
            fontWeight: 700,
            minWidth: 220,
          }}
        />
        <button
          onClick={() => void submitRename()}
          disabled={renameBusy}
          className="px-3 py-1.5 text-[12px] font-medium transition disabled:opacity-50"
          style={{
            background: "var(--accent-fill)",
            color: "var(--accent-fg)",
            borderRadius: "var(--r-md)",
          }}
        >
          {renameBusy ? "Saving…" : "Save"}
        </button>
        <button
          onClick={() => setRenaming(false)}
          disabled={renameBusy}
          className="px-3 py-1.5 text-[12px] font-medium transition disabled:opacity-50"
          style={{
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            color: "var(--fg)",
            borderRadius: "var(--r-md)",
          }}
        >
          Cancel
        </button>
      </span>
    ) : (
      <span className="inline-flex items-center gap-2.5">
        {activeWorkspace.name}
        {isAdmin && (
          <button
            onClick={startRename}
            className="text-[12px] font-normal transition"
            style={{ color: "var(--accent-strong)" }}
            title="Rename workspace"
          >
            Rename
          </button>
        )}
      </span>
    )
  ) : (
    "Workspace"
  );

  return (
    <PageShell
      crumbs={["Workspace"]}
      eyebrow="Workspace"
      title={title}
      subtitle={
        isAdmin
          ? "Manage who can access this workspace and their roles."
          : "The people who can access this workspace."
      }
      actions={
        isAdmin && wsId ? (
          <button
            onClick={() => {
              setShowInvite((v) => !v);
              setInviteError("");
              setInviteSuccess("");
            }}
            className="px-4 py-2 text-[13px] font-medium transition"
            style={{
              background: showInvite ? "var(--bg-panel)" : "var(--accent-fill)",
              color: showInvite ? "var(--fg)" : "var(--accent-fg)",
              border: showInvite ? "1px solid var(--border)" : "none",
              borderRadius: "var(--r-md)",
            }}
          >
            {showInvite ? "Cancel" : "Invite member"}
          </button>
        ) : null
      }
    >
      {renameError && (
        <Banner tone="danger" onDismiss={() => setRenameError("")}>
          {renameError}
        </Banner>
      )}
      {actionError && (
        <Banner tone="danger" onDismiss={() => setActionError("")}>
          {actionError}
        </Banner>
      )}
      {loadError && <Banner tone="danger">{loadError}</Banner>}

      {/* Stats strip */}
      <div className="grid grid-cols-3 gap-3 mb-6">
        {[
          { label: "Members", value: String(stats.members) },
          { label: "Admins", value: String(stats.admins) },
          { label: "Pending invites", value: isAdmin ? String(stats.pending) : "—" },
        ].map((s) => (
          <div
            key={s.label}
            className="px-4 py-3.5"
            style={{
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-lg)",
            }}
          >
            <div
              className="font-mono text-[11px] font-semibold uppercase mb-1.5"
              style={{ color: "var(--fg-subtle)", letterSpacing: "0.08em" }}
            >
              {s.label}
            </div>
            <div
              className="text-[24px]"
              style={{
                fontFamily: "var(--f-ui)",
                fontWeight: 600,
                color: "var(--fg)",
                letterSpacing: "-0.01em",
                lineHeight: 1.1,
              }}
            >
              {s.value}
            </div>
          </div>
        ))}
      </div>

      {/* Invite form (admin-only toggle-inline) */}
      {isAdmin && showInvite && (
        <form
          onSubmit={submitInvite}
          className="p-5 space-y-4 mb-6"
          style={{
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-lg)",
          }}
        >
          <p className="text-[14px] font-semibold" style={{ color: "var(--fg)" }}>
            Invite a member
          </p>
          {inviteError && (
            <Banner tone="danger" onDismiss={() => setInviteError("")} inline>
              {inviteError}
            </Banner>
          )}
          {inviteSuccess && (
            <Banner tone="success" onDismiss={() => setInviteSuccess("")} inline>
              {inviteSuccess}
            </Banner>
          )}
          <div className="flex flex-col md:flex-row md:items-end gap-3">
            <div className="md:flex-1 md:min-w-[220px]">
              <label
                htmlFor="invite-email"
                className="block text-[12px] font-medium mb-1"
                style={{ color: "var(--fg-muted)" }}
              >
                Email
              </label>
              <input
                id="invite-email"
                type="email"
                value={inviteEmail}
                onChange={(e) => setInviteEmail(e.target.value)}
                placeholder="teammate@company.com"
                className="w-full px-3 py-2 text-[13px]"
                style={{
                  background: "var(--bg-elev)",
                  border: "1px solid var(--border)",
                  borderRadius: "var(--r-md)",
                  color: "var(--fg)",
                }}
              />
            </div>
            <div className="md:min-w-[150px]">
              <label
                htmlFor="invite-role"
                className="block text-[12px] font-medium mb-1"
                style={{ color: "var(--fg-muted)" }}
              >
                Role
              </label>
              <select
                id="invite-role"
                value={inviteRole}
                onChange={(e) => setInviteRole(e.target.value as WorkspaceRole)}
                className="w-full px-3 py-2 text-[13px] cursor-pointer"
                style={{
                  background: "var(--bg-elev)",
                  border: "1px solid var(--border)",
                  borderRadius: "var(--r-md)",
                  color: "var(--fg)",
                }}
              >
                <option value="member">Member</option>
                <option value="admin">Admin</option>
              </select>
            </div>
            <button
              type="submit"
              disabled={inviteBusy || !inviteEmail.trim()}
              className="w-full md:w-auto px-4 py-2 text-[13px] font-medium transition disabled:opacity-50"
              style={{
                background: "var(--accent-fill)",
                color: "var(--accent-fg)",
                borderRadius: "var(--r-md)",
              }}
            >
              {inviteBusy ? "Sending…" : "Send invite"}
            </button>
          </div>
        </form>
      )}

      {/* Members table */}
      <p
        className="font-mono text-[10px] uppercase font-semibold mb-3"
        style={{ color: "var(--fg-subtle)", letterSpacing: "0.08em" }}
      >
        Members
      </p>
      {wsLoading || membersLoading ? (
        <p className="text-[13px] py-12 text-center" style={{ color: "var(--fg-muted)" }}>
          Loading…
        </p>
      ) : members.length === 0 ? (
        <EmptyPanel>No members yet.</EmptyPanel>
      ) : (
        <div
          className="overflow-x-auto"
          style={{
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-lg)",
          }}
        >
          <table className="w-full text-[13px] min-w-[640px]">
            <thead>
              <tr
                className="text-left font-mono text-[10px] uppercase"
                style={{
                  background: "var(--bg-elev)",
                  color: "var(--fg-subtle)",
                  letterSpacing: "0.08em",
                }}
              >
                <th className="px-4 py-2.5 font-semibold">Member</th>
                <th className="px-4 py-2.5 font-semibold">Role</th>
                <th className="px-4 py-2.5 font-semibold">Joined</th>
                {isAdmin && <th className="px-4 py-2.5 font-semibold"></th>}
              </tr>
            </thead>
            <tbody>
              {members.map((m, i) => {
                const isSelf = m.user_id === user?.id;
                const busy = rowBusy === m.user_id;
                return (
                  <tr
                    key={m.user_id}
                    style={{ borderTop: i > 0 ? "1px solid var(--border-sub)" : "none" }}
                  >
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-3 min-w-0">
                        <span
                          className="inline-flex items-center justify-center shrink-0 rounded-full text-[11px] font-semibold"
                          style={{
                            width: 30,
                            height: 30,
                            background: "var(--bg-elev)",
                            color: "var(--fg-muted)",
                          }}
                        >
                          {initialsOf(m)}
                        </span>
                        <div className="min-w-0">
                          <div className="truncate" style={{ color: "var(--fg)" }}>
                            {m.name || (
                              <span style={{ color: "var(--fg-subtle)" }}>Unnamed</span>
                            )}
                            {isSelf && (
                              <span
                                className="ml-1.5 text-[11px]"
                                style={{ color: "var(--fg-subtle)" }}
                              >
                                (you)
                              </span>
                            )}
                          </div>
                          <div
                            className="truncate font-mono text-[11px]"
                            style={{ color: "var(--fg-muted)" }}
                          >
                            {m.email}
                          </div>
                        </div>
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      <Chip tone={m.role === "admin" ? "accent" : "neutral"}>
                        {m.role}
                      </Chip>
                    </td>
                    <td
                      className="px-4 py-3 font-mono text-[12px]"
                      style={{ color: "var(--fg-muted)" }}
                    >
                      {formatDate(m.created_at)}
                    </td>
                    {isAdmin && (
                      <td className="px-4 py-3">
                        <div className="flex items-center justify-end gap-2">
                          <select
                            aria-label={`Role for ${m.email}`}
                            value={m.role}
                            disabled={busy}
                            onChange={(e) =>
                              void handleRoleChange(m, e.target.value as WorkspaceRole)
                            }
                            className="px-2 py-1 text-[12px] cursor-pointer disabled:opacity-50"
                            style={{
                              background: "var(--bg-elev)",
                              border: "1px solid var(--border)",
                              borderRadius: "var(--r-md)",
                              color: "var(--fg)",
                            }}
                          >
                            <option value="member">member</option>
                            <option value="admin">admin</option>
                          </select>
                          <button
                            onClick={() => void handleRemove(m)}
                            disabled={busy}
                            className="text-[12px] transition disabled:opacity-50"
                            style={{ color: "var(--danger-strong)" }}
                          >
                            {isSelf ? "Leave" : "Remove"}
                          </button>
                        </div>
                      </td>
                    )}
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Pending invitations table (admin-only) */}
      {isAdmin && (
        <>
          <p
            className="font-mono text-[10px] uppercase font-semibold mt-8 mb-3"
            style={{ color: "var(--fg-subtle)", letterSpacing: "0.08em" }}
          >
            Pending invitations
          </p>
          {invitationsLoading ? (
            <p
              className="text-[13px] py-8 text-center"
              style={{ color: "var(--fg-muted)" }}
            >
              Loading…
            </p>
          ) : invitations.length === 0 ? (
            <EmptyPanel>No pending invitations.</EmptyPanel>
          ) : (
            <div
              className="overflow-x-auto"
              style={{
                background: "var(--bg-panel)",
                border: "1px solid var(--border)",
                borderRadius: "var(--r-lg)",
              }}
            >
              <table className="w-full text-[13px] min-w-[640px]">
                <thead>
                  <tr
                    className="text-left font-mono text-[10px] uppercase"
                    style={{
                      background: "var(--bg-elev)",
                      color: "var(--fg-subtle)",
                      letterSpacing: "0.08em",
                    }}
                  >
                    <th className="px-4 py-2.5 font-semibold">Email</th>
                    <th className="px-4 py-2.5 font-semibold">Role</th>
                    <th className="px-4 py-2.5 font-semibold">Invited by</th>
                    <th className="px-4 py-2.5 font-semibold">Expires</th>
                    <th className="px-4 py-2.5 font-semibold"></th>
                  </tr>
                </thead>
                <tbody>
                  {invitations.map((inv, i) => {
                    const busy = rowBusy === inv.id;
                    return (
                      <tr
                        key={inv.id}
                        style={{
                          borderTop: i > 0 ? "1px solid var(--border-sub)" : "none",
                        }}
                      >
                        <td className="px-4 py-3" style={{ color: "var(--fg)" }}>
                          {inv.email}
                        </td>
                        <td className="px-4 py-3">
                          <Chip tone={inv.role === "admin" ? "accent" : "neutral"}>
                            {inv.role}
                          </Chip>
                        </td>
                        <td
                          className="px-4 py-3 font-mono text-[12px]"
                          style={{ color: "var(--fg-muted)" }}
                        >
                          {inv.invited_by || "—"}
                        </td>
                        <td
                          className="px-4 py-3 font-mono text-[12px]"
                          style={{ color: "var(--fg-muted)" }}
                        >
                          {formatDate(inv.expires_at)}
                        </td>
                        <td className="px-4 py-3 text-right">
                          <button
                            onClick={() => void handleRevoke(inv)}
                            disabled={busy}
                            className="text-[12px] transition disabled:opacity-50"
                            style={{ color: "var(--danger-strong)" }}
                          >
                            Revoke
                          </button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </PageShell>
  );
}

// ── Local presentational helpers ─────────────────────────

function Banner({
  tone,
  children,
  onDismiss,
  inline = false,
}: {
  tone: "danger" | "success";
  children: React.ReactNode;
  onDismiss?: () => void;
  inline?: boolean;
}) {
  const isDanger = tone === "danger";
  return (
    <div
      className={`${inline ? "" : "mb-6"} p-3 text-[13px] flex items-start gap-3`}
      style={{
        background: isDanger ? "var(--danger-bg)" : "var(--success-bg)",
        color: isDanger ? "var(--danger-strong)" : "var(--success)",
        border: `1px solid ${isDanger ? "var(--danger-bg)" : "var(--success-bg)"}`,
        borderRadius: "var(--r-md)",
      }}
    >
      <span className="flex-1">{children}</span>
      {onDismiss && (
        <button
          onClick={onDismiss}
          className="text-[12px] underline shrink-0"
          style={{ color: isDanger ? "var(--danger-strong)" : "var(--success)" }}
        >
          Dismiss
        </button>
      )}
    </div>
  );
}

function EmptyPanel({ children }: { children: React.ReactNode }) {
  return (
    <div
      className="p-8 text-center"
      style={{
        background: "var(--bg-panel)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-lg)",
      }}
    >
      <p className="text-[14px]" style={{ color: "var(--fg-muted)" }}>
        {children}
      </p>
    </div>
  );
}
