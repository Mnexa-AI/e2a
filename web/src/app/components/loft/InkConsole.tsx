"use client";

import { useCallback, useState, type ReactNode } from "react";
import { Button } from "./Button";

export type InkLineKind = "comment" | "prompt" | "string" | "accent" | "plain";

export type InkLine =
  | { c?: InkLineKind; text: string; fg?: string; node?: undefined }
  | { node: ReactNode; c?: undefined; text?: undefined; fg?: undefined };

export type InkConsoleProps = {
  lines: InkLine[];
  title?: string;
  lang?: string;
  copy?: boolean;
  height?: number | string;
  className?: string;
};

const kindColor: Record<InkLineKind, string> = {
  comment: "var(--ink-fg-muted)",
  prompt: "var(--machine)",
  string: "var(--spectral)",
  accent: "var(--accent)",
  plain: "var(--ink-fg)",
};

function plainText(lines: InkLine[]): string {
  return lines
    .map((l) => (l.node ? "" : l.text ?? ""))
    .filter(Boolean)
    .join("\n");
}

export function InkConsole({
  lines,
  title,
  lang,
  copy = true,
  height,
  className = "",
}: InkConsoleProps) {
  const showHeader = Boolean(title || lang || copy);
  const [copied, setCopied] = useState(false);

  const onCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(plainText(lines));
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    } catch {
      // clipboard unavailable — silently ignore
    }
  }, [lines]);

  return (
    <div
      className={`overflow-hidden font-mono ${className}`}
      style={{
        background: "var(--ink)",
        border: "1px solid var(--ink-border)",
        borderRadius: "var(--r-lg)",
        height,
      }}
    >
      {showHeader && (
        <div
          className="flex items-center px-3.5 py-2 text-[11px] tracking-[0.02em]"
          style={{
            borderBottom: "1px solid var(--ink-border)",
            background: "var(--ink-elev)",
            color: "var(--ink-fg-muted)",
          }}
        >
          {title && (
            <span
              className="font-medium"
              style={{ color: "var(--ink-fg)" }}
            >
              {title}
            </span>
          )}
          {lang && (
            <span
              className={`text-[10px] uppercase tracking-[0.1em] ${title ? "ml-2.5" : ""}`}
              style={{ color: "var(--spectral)" }}
            >
              {lang}
            </span>
          )}
          <span className="flex-1" />
          {copy && (
            <Button
              variant="mono"
              onClick={onCopy}
              aria-label="Copy to clipboard"
            >
              <svg
                width="10"
                height="10"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth={2}
                aria-hidden
              >
                <rect x="9" y="9" width="11" height="11" rx="2" />
                <path d="M5 15V5a2 2 0 012-2h10" />
              </svg>
              {copied ? "copied" : "copy"}
            </Button>
          )}
        </div>
      )}
      <div className="px-4 py-3.5 text-[12.5px] leading-[1.6]">
        {lines.map((l, i) => {
          if (l.node !== undefined) {
            return <div key={i}>{l.node}</div>;
          }
          const color = l.fg ?? kindColor[l.c ?? "plain"];
          return (
            <div key={i} className="whitespace-pre-wrap" style={{ color }}>
              {l.text}
            </div>
          );
        })}
      </div>
    </div>
  );
}
