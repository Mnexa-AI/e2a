import type { ReactNode } from "react";
import { Topbar } from "./Topbar";
import { Eyebrow } from "./Eyebrow";

export type PageShellProps = {
  crumbs?: string[];
  topbarRight?: ReactNode;
  eyebrow?: string;
  title?: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  maxWidth?: number | string;
};

export function PageShell({
  crumbs,
  topbarRight,
  eyebrow,
  title,
  subtitle,
  actions,
  children,
  maxWidth = 1080,
}: PageShellProps) {
  const hasHeader = Boolean(eyebrow || title || subtitle || actions);
  return (
    <div className="flex flex-col">
      {crumbs && crumbs.length > 0 && (
        <Topbar crumbs={crumbs} right={topbarRight} />
      )}
      <div
        className="px-6 md:px-8 lg:px-12 py-8 md:py-10 mx-auto w-full"
        style={{ maxWidth }}
      >
        {hasHeader && (
          <header className="mb-7 md:mb-8 flex flex-col md:flex-row md:items-start md:justify-between gap-4">
            <div className="min-w-0">
              {eyebrow && <Eyebrow>{eyebrow}</Eyebrow>}
              {title && (
                <h1
                  className={eyebrow ? "mt-3" : ""}
                  style={{
                    fontFamily: "var(--f-editorial)",
                    fontWeight: 400,
                    fontSize: "clamp(32px, 5vw, 44px)",
                    letterSpacing: "-0.012em",
                    lineHeight: 1.05,
                    color: "var(--fg)",
                    margin: 0,
                  }}
                >
                  {title}
                </h1>
              )}
              {subtitle && (
                <p
                  className="mt-2 leading-[1.6]"
                  style={{
                    fontSize: 14,
                    color: "var(--fg-muted)",
                    maxWidth: 640,
                  }}
                >
                  {subtitle}
                </p>
              )}
            </div>
            {actions && (
              <div className="flex flex-wrap items-center gap-2 md:shrink-0">
                {actions}
              </div>
            )}
          </header>
        )}
        {children}
      </div>
    </div>
  );
}
