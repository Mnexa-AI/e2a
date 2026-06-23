import { Fragment, type ReactNode } from "react";
import Link from "next/link";

const SEARCH_ENABLED = process.env.NEXT_PUBLIC_SEARCH_ENABLED === "true";

// A crumb is either a plain label (non-clickable) or a label + href that
// renders as a link. The trailing crumb is always plain (it's the current
// page) even if an href is supplied.
export type Crumb = string | { label: string; href?: string };

export type TopbarProps = {
  crumbs?: Crumb[];
  right?: ReactNode;
  className?: string;
};

function SearchAffordance() {
  return (
    <div
      // 280px makes sense on desktop; on phones it would eat the entire
      // topbar so the breadcrumbs vanish. Drop the min-width on mobile
      // and let the affordance shrink to its content (the label + ⌘K
      // pill). The full search overlay will live on its own when the
      // search feature actually ships — this is just the affordance.
      className="hidden sm:flex items-center gap-2 px-3 py-1.5 md:min-w-[280px] font-mono text-[12px]"
      style={{
        background: "var(--bg-elev)",
        border: "1px solid var(--border-sub)",
        borderRadius: "var(--r-md)",
        color: "var(--fg-subtle)",
      }}
    >
      <svg
        width="13"
        height="13"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth={2}
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden
      >
        <circle cx="11" cy="11" r="7" />
        <path d="M21 21l-4.3-4.3" />
      </svg>
      <span>Search inboxes, messages…</span>
      <span
        className="ml-auto px-1.5 py-px text-[10px] font-mono"
        style={{
          background: "var(--bg-panel)",
          border: "1px solid var(--border)",
          color: "var(--fg-subtle)",
          borderRadius: 3,
        }}
      >
        ⌘K
      </span>
    </div>
  );
}

export function Topbar({ crumbs = [], right, className = "" }: TopbarProps) {
  const trailing = right ?? (SEARCH_ENABLED ? <SearchAffordance /> : null);
  return (
    <div
      className={`flex items-center justify-between px-4 md:px-7 gap-3 ${className}`}
      style={{
        background: "var(--bg-panel)",
        borderBottom: "1px solid var(--border)",
        minHeight: "var(--chrome-h)",
      }}
    >
      <div
        className="flex items-center gap-2.5 text-[12px] min-w-0 overflow-hidden"
        style={{ color: "var(--fg-muted)" }}
      >
        {crumbs.map((c, i) => {
          const label = typeof c === "string" ? c : c.label;
          const href = typeof c === "string" ? undefined : c.href;
          const isLast = i === crumbs.length - 1;
          const color = isLast ? "var(--fg)" : "var(--fg-muted)";
          return (
            <Fragment key={`${i}:${label}`}>
              {i > 0 && <span aria-hidden className="shrink-0">/</span>}
              {href && !isLast ? (
                <Link
                  href={href}
                  className="truncate hover:underline"
                  style={{ color }}
                >
                  {label}
                </Link>
              ) : (
                <span className="truncate" style={{ color }}>
                  {label}
                </span>
              )}
            </Fragment>
          );
        })}
      </div>
      <div className="flex items-center gap-2.5 shrink-0">{trailing}</div>
    </div>
  );
}
