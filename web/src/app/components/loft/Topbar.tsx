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
        {crumbs.map((c, i) => (
          <Fragment key={`${i}:${c}`}>
            {i > 0 && <span aria-hidden className="shrink-0">/</span>}
            <span
              className="truncate"
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
      <div className="flex items-center gap-2.5 shrink-0">{trailing}</div>
    </div>
  );
}
