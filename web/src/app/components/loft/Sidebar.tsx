"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import type { ReactNode } from "react";
import { useAuth } from "../AuthProvider";
import { usePendingCount } from "../hooks/usePendingCount";

type IconKey = "plus" | "grid" | "clock" | "globe" | "key" | "settings" | "msg" | "shield";

const ICONS: Record<IconKey, ReactNode> = {
  plus: (
    <>
      <circle cx="12" cy="12" r="9.5" />
      <path d="M12 8v8M8 12h8" />
    </>
  ),
  grid: (
    <>
      <rect x="3.5" y="3.5" width="7" height="7" rx="1.5" />
      <rect x="13.5" y="3.5" width="7" height="7" rx="1.5" />
      <rect x="3.5" y="13.5" width="7" height="7" rx="1.5" />
      <rect x="13.5" y="13.5" width="7" height="7" rx="1.5" />
    </>
  ),
  clock: (
    <>
      <circle cx="12" cy="12" r="9.5" />
      <polyline points="12 6.5 12 12 16 14" />
    </>
  ),
  globe: (
    <>
      <circle cx="12" cy="12" r="9.5" />
      <path d="M3 12h18" />
      <path d="M12 3a16 16 0 010 18 16 16 0 010-18z" />
    </>
  ),
  key: (
    <>
      <circle cx="8" cy="14" r="4" />
      <path d="M11 11l9-9M15 6l3 3M18 3l3 3" />
    </>
  ),
  settings: (
    <>
      <circle cx="12" cy="12" r="2.5" />
      <path d="M19 12a7 7 0 00-.1-1.3l2-1.6-2-3.5-2.4 1a7 7 0 00-2.2-1.3l-.3-2.6h-4l-.4 2.6a7 7 0 00-2.2 1.3l-2.4-1-2 3.5 2 1.6A7 7 0 005 12c0 .4 0 .9.1 1.3l-2 1.6 2 3.5 2.4-1a7 7 0 002.2 1.3l.4 2.6h4l.3-2.6a7 7 0 002.2-1.3l2.4 1 2-3.5-2-1.6c.1-.4.1-.9.1-1.3z" />
    </>
  ),
  msg: <path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z" />,
  // Shield-with-checkmark — denotes the signing-secret integrity guard.
  // Matches the "API keys" icon's silhouette weight so the credential
  // pair reads as visually related in the sidebar.
  shield: (
    <>
      <path d="M12 3l8 3v6c0 4.5-3.5 8-8 9-4.5-1-8-4.5-8-9V6l8-3z" />
      <path d="M9 12l2 2 4-4" />
    </>
  ),
};

type NavItem = {
  href: string;
  label: string;
  icon: IconKey;
  matchPrefix?: boolean;
};

const NAV_ITEMS: NavItem[] = [
  { href: "/get-started", label: "Get started", icon: "plus" },
  { href: "/dashboard", label: "Agents", icon: "grid" },
  {
    href: "/dashboard/pending",
    label: "Pending",
    icon: "clock",
    matchPrefix: true,
  },
  { href: "/domains", label: "Domains", icon: "globe" },
  { href: "/api-keys", label: "API keys", icon: "key" },
  { href: "/webhook-secrets", label: "Webhook secrets", icon: "shield" },
];

function NavIcon({ kind }: { kind: IconKey }) {
  return (
    <svg
      width="17"
      height="17"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      {ICONS[kind]}
    </svg>
  );
}

function isActive(pathname: string, item: NavItem | { href: string; matchPrefix?: boolean }) {
  if (item.matchPrefix) return pathname === item.href || pathname.startsWith(item.href + "/");
  return pathname === item.href;
}

function userInitials(user: { name?: string; email: string }): string {
  if (user.name) {
    const parts = user.name.trim().split(/\s+/);
    return ((parts[0]?.[0] ?? "") + (parts[1]?.[0] ?? ""))
      .toUpperCase()
      .slice(0, 2);
  }
  return user.email.slice(0, 2).toUpperCase();
}

// The default `className` hides the sidebar below `md` because the app
// layout swaps in a mobile slide-in sheet at those sizes. Pass an explicit
// className (e.g. "flex flex-col") to render the sidebar in any container,
// like that sheet.
export function Sidebar({
  className = "hidden md:flex md:flex-col",
}: {
  className?: string;
} = {}) {
  const pathname = usePathname() ?? "";
  const { user, signOut } = useAuth();
  const pendingCount = usePendingCount();

  return (
    <aside
      className={`${className} w-[248px] shrink-0 sticky top-0 h-screen`}
      style={{
        background: "var(--bg-panel)",
        borderRight: "1px solid var(--border)",
      }}
    >
      {/*
        Logo block — sits at the same `--chrome-h` as the page Topbar so the
        two bottom borders form one continuous divider across the viewport.
        Padding is horizontal-only; min-h + flex centering handle the rest.
      */}
      <Link
        href="/"
        className="flex items-center gap-2.5 px-5"
        style={{
          minHeight: "var(--chrome-h)",
          borderBottom: "1px solid var(--border)",
        }}
      >
        <div
          className="flex items-center justify-center font-mono font-bold text-[12px]"
          style={{
            width: 32,
            height: 32,
            borderRadius: 7,
            background: "var(--fg)",
            color: "var(--bg)",
            letterSpacing: "-0.04em",
          }}
        >
          e2a
        </div>
        <div>
          <div
            className="font-mono font-bold text-[14px] leading-none"
            style={{ color: "var(--fg)", letterSpacing: "-0.02em" }}
          >
            e2a
          </div>
          <div
            className="text-[11px] mt-0.5"
            style={{ color: "var(--fg-muted)" }}
          >
            Email for AI agents
          </div>
        </div>
      </Link>

      {/*
        Workspace/org switcher intentionally omitted until BACKEND_TODO #9
        (multi-tenant orgs) lands. Until then the bottom-of-sidebar user
        card is the canonical identity affordance — a second card here
        would just duplicate it.
      */}

      {/* Nav */}
      <nav className="flex-1 px-3 pt-3 pb-1.5">
        {NAV_ITEMS.map((item) => {
          const active = isActive(pathname, item);
          const showBadge =
            item.href === "/dashboard/pending" &&
            pendingCount !== null &&
            pendingCount > 0;
          return (
            <Link
              key={item.href}
              href={item.href}
              className="flex items-center gap-2.5 px-3 py-2 mb-px text-[13px] font-sans"
              style={{
                borderRadius: "var(--r-md)",
                fontWeight: active ? 500 : 400,
                color: active ? "var(--fg)" : "var(--fg-muted)",
                background: active ? "var(--bg-elev)" : "transparent",
                boxShadow: active ? "inset 2px 0 0 var(--accent)" : "none",
              }}
            >
              <NavIcon kind={item.icon} />
              <span className="flex-1">{item.label}</span>
              {showBadge && (
                <span
                  className="inline-flex items-center justify-center font-mono text-[10px] font-bold text-white"
                  style={{
                    minWidth: 18,
                    height: 18,
                    padding: "0 6px",
                    background: "var(--accent)",
                    borderRadius: 999,
                  }}
                >
                  {pendingCount}
                </span>
              )}
            </Link>
          );
        })}
      </nav>

      {/* Bottom */}
      <div
        className="px-3 pt-2.5 pb-3.5"
        style={{ borderTop: "1px solid var(--border)" }}
      >
        <Link
          href="/settings"
          className="flex items-center gap-2.5 px-3 py-2 text-[13px] font-sans"
          style={{
            borderRadius: "var(--r-md)",
            fontWeight: isActive(pathname, { href: "/settings" }) ? 500 : 400,
            color: isActive(pathname, { href: "/settings" })
              ? "var(--fg)"
              : "var(--fg-muted)",
            background: isActive(pathname, { href: "/settings" })
              ? "var(--bg-elev)"
              : "transparent",
            boxShadow: isActive(pathname, { href: "/settings" })
              ? "inset 2px 0 0 var(--accent)"
              : "none",
          }}
        >
          <NavIcon kind="settings" />
          Settings
        </Link>
        <Link
          href="/feedback"
          className="flex items-center gap-2.5 px-3 py-2 text-[13px] font-sans"
          style={{
            borderRadius: "var(--r-md)",
            color: "var(--fg-muted)",
          }}
        >
          <NavIcon kind="msg" />
          Send feedback
        </Link>

        {/* User card */}
        {user && (
          <div
            className="flex items-center gap-2.5 mt-2.5 px-2.5 py-2"
            style={{
              border: "1px solid var(--border-sub)",
              borderRadius: "var(--r-md)",
            }}
          >
            <div
              className="flex items-center justify-center text-[11px] font-bold text-white shrink-0 rounded-full"
              style={{
                width: 28,
                height: 28,
                background: "var(--av-4)",
              }}
            >
              {userInitials(user)}
            </div>
            <div className="flex-1 min-w-0">
              <div
                className="text-[12px] font-medium truncate"
                style={{ color: "var(--fg)" }}
              >
                {user.name || "User"}
              </div>
              <div
                className="font-mono text-[10px] truncate"
                style={{ color: "var(--fg-subtle)" }}
              >
                {user.email}
              </div>
            </div>
            <button
              type="button"
              onClick={signOut}
              title="Sign out"
              className="shrink-0 transition"
              style={{ color: "var(--fg-muted)" }}
            >
              <svg
                width="16"
                height="16"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.6"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden
              >
                <path d="M9 21H5a2 2 0 01-2-2V5a2 2 0 012-2h4" />
                <polyline points="16 17 21 12 16 7" />
                <line x1="21" y1="12" x2="9" y2="12" />
              </svg>
            </button>
          </div>
        )}
      </div>
    </aside>
  );
}
