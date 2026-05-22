import { Fragment, type ReactNode } from "react";

const SEARCH_ENABLED = process.env.NEXT_PUBLIC_SEARCH_ENABLED === "true";

export type TopbarProps = {
  crumbs?: string[];
  right?: ReactNode;
  className?: string;
};

function SearchAffordance() {
  return (
    <div
      className="flex items-center gap-2 px-3 py-1.5 min-w-[280px] font-mono text-[12px]"
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
      <span>Search agents, messages…</span>
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
      className={`flex items-center justify-between px-7 py-3.5 ${className}`}
      style={{
        background: "var(--bg-panel)",
        borderBottom: "1px solid var(--border)",
      }}
    >
      <div
        className="flex items-center gap-2.5 text-[12px]"
        style={{ color: "var(--fg-muted)" }}
      >
        {crumbs.map((c, i) => (
          <Fragment key={`${i}:${c}`}>
            {i > 0 && <span aria-hidden>/</span>}
            <span
              style={{
                color:
                  i === crumbs.length - 1
                    ? "var(--fg)"
                    : "var(--fg-muted)",
              }}
            >
              {c}
            </span>
          </Fragment>
        ))}
      </div>
      <div className="flex items-center gap-2.5">{trailing}</div>
    </div>
  );
}
