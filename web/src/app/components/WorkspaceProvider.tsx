"use client";

// WorkspaceProvider — the active-workspace context for the authenticated
// (app) surface (§4.2/§4.4).
//
// A human web session may belong to several workspaces; exactly one is
// "active" per request, selected via the `X-E2A-Workspace` header. This
// provider owns that selection:
//
//   - fetches GET /v1/workspaces for the switcher list (+ each row's role)
//   - seeds the active workspace + role from whoami (GET /v1/account), the
//     authoritative server-resolved active workspace
//   - persists the active id to localStorage so a reload keeps the same
//     workspace before whoami round-trips
//   - stamps the active id into the central request<T> header slot so every
//     /v1 fetch rides with the right tenant selector
//   - switchWorkspace(id) flips the active id, updates the header, persists
//     it, and clears the tenant-scoped SWR cache so agents/domains/messages
//     refetch under the new tenant
//
// The header is a session-only selector: it's ignored for key/OAuth auth
// (where the workspace is intrinsic). The server re-verifies live
// membership and falls back to last-active / the default workspace when no
// header is sent, so a brief unset window on first paint is safe.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { useAuth } from "./AuthProvider";
import {
  listWorkspaces,
  setActiveWorkspaceId,
} from "./onboarding/api";
import { invalidateTenantScopedData } from "../../lib/swrKeys";
import type { Workspace, WorkspaceRole, WhoamiWorkspace } from "./types";

const STORAGE_KEY = "e2a.activeWorkspaceId";

// Minimal slice of the whoami (GET /v1/account) response we consume to seed
// the active workspace + role. Additive fields (§4.4); both omitted when
// the credential resolved no workspace.
type Whoami = {
  workspace?: WhoamiWorkspace;
  role?: WorkspaceRole;
};

type WorkspaceContextType = {
  workspaces: Workspace[];
  activeWorkspace: Workspace | null;
  role: WorkspaceRole | null;
  loading: boolean;
  switchWorkspace: (id: string) => void;
  // Refetch the workspace list (after a rename / leave / accept). The
  // members/invitations sub-resources own their own SWR keys.
  refresh: () => Promise<void>;
};

const WorkspaceContext = createContext<WorkspaceContextType>({
  workspaces: [],
  activeWorkspace: null,
  role: null,
  loading: true,
  switchWorkspace: () => {},
  refresh: async () => {},
});

export function useWorkspace() {
  return useContext(WorkspaceContext);
}

function readStoredActiveId(): string | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

function writeStoredActiveId(id: string | null): void {
  if (typeof window === "undefined") return;
  try {
    if (id) window.localStorage.setItem(STORAGE_KEY, id);
    else window.localStorage.removeItem(STORAGE_KEY);
  } catch {
    // Private-mode / quota — non-fatal; the header slot still works for
    // the session, we just don't survive a reload.
  }
}

export function WorkspaceProvider({ children }: { children: React.ReactNode }) {
  const { user } = useAuth();
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  // Lazy initializer (runs once): seed the active id + the request<T>
  // header slot from localStorage so even the first /v1 fetch (before
  // whoami resolves) carries the last-known tenant. Mirrors activeId into
  // the module-level slot the API layer reads.
  const [activeId, setActiveId] = useState<string | null>(() => {
    const stored = readStoredActiveId();
    if (stored) setActiveWorkspaceId(stored);
    return stored;
  });
  const [role, setRole] = useState<WorkspaceRole | null>(null);
  const [loading, setLoading] = useState(true);

  const applyActive = useCallback((id: string | null) => {
    setActiveId(id);
    setActiveWorkspaceId(id);
    writeStoredActiveId(id);
  }, []);

  const refresh = useCallback(async () => {
    const list = await listWorkspaces();
    setWorkspaces(list);
  }, []);

  // Seed: fetch the workspace list + whoami in parallel. whoami is the
  // authoritative active workspace (it reflects the server's resolution of
  // the header / last-active / default), so we prefer it; we fall back to a
  // stored id that's still a live membership, then to the first workspace.
  useEffect(() => {
    if (!user) {
      setWorkspaces([]);
      applyActive(null);
      setRole(null);
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    (async () => {
      const [list, whoami] = await Promise.all([
        listWorkspaces().catch(() => [] as Workspace[]),
        fetch("/api/auth/me", { credentials: "include" })
          .then((r) => (r.ok ? (r.json() as Promise<Whoami>) : null))
          // /api/auth/me may not carry workspace fields; whoami lives on
          // /v1/account, so prefer that and fall back to the list.
          .catch(() => null),
      ]);
      if (cancelled) return;

      // Pull the authoritative active workspace from /v1/account (whoami).
      let resolved: Whoami | null = whoami;
      if (!resolved?.workspace) {
        resolved = await fetch("/v1/account", { credentials: "include" })
          .then((r) => (r.ok ? (r.json() as Promise<Whoami>) : null))
          .catch(() => null);
      }
      if (cancelled) return;

      setWorkspaces(list);

      const stored = readStoredActiveId();
      const inList = (id: string | null | undefined) =>
        !!id && list.some((w) => w.id === id);

      let chosen: string | null = null;
      if (resolved?.workspace && inList(resolved.workspace.id)) {
        chosen = resolved.workspace.id;
      } else if (inList(stored)) {
        chosen = stored;
      } else if (list.length > 0) {
        chosen = list[0].id;
      }
      applyActive(chosen);

      // Role: prefer whoami's role for the resolved workspace; else read it
      // off the chosen row in the list.
      if (resolved?.role && resolved.workspace?.id === chosen) {
        setRole(resolved.role);
      } else {
        const row = list.find((w) => w.id === chosen);
        setRole(row?.role ?? null);
      }
      setLoading(false);
    })();
    return () => {
      cancelled = true;
    };
  }, [user, applyActive]);

  const switchWorkspace = useCallback(
    (id: string) => {
      if (id === activeId) return;
      const row = workspaces.find((w) => w.id === id);
      if (!row) return; // Never switch to a workspace we're not a member of.
      applyActive(id);
      setRole(row.role ?? null);
      // Tenant changed — drop the previous tenant's cached data so every
      // tenant-scoped query refetches under the new header.
      void invalidateTenantScopedData();
    },
    [activeId, workspaces, applyActive],
  );

  const activeWorkspace = useMemo(
    () => workspaces.find((w) => w.id === activeId) ?? null,
    [workspaces, activeId],
  );

  const value = useMemo<WorkspaceContextType>(
    () => ({
      workspaces,
      activeWorkspace,
      role,
      loading,
      switchWorkspace,
      refresh,
    }),
    [workspaces, activeWorkspace, role, loading, switchWorkspace, refresh],
  );

  return (
    <WorkspaceContext.Provider value={value}>
      {children}
    </WorkspaceContext.Provider>
  );
}
