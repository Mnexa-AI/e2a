"use client";

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Button } from "../Button/Button";

export type InkLineKind = "comment" | "prompt" | "string" | "accent" | "plain";

export type InkLine =
  | { c?: InkLineKind; text: string; fg?: string; node?: undefined }
  | { node: ReactNode; c?: undefined; text?: undefined; fg?: undefined };

export type InkConsoleProps = {
  /** Lines to render. Each is either tokenized text (`{ text, c }`) or a raw `{ node }`. */
  lines: InkLine[];
  title?: string;
  lang?: string;
  /** Show the copy button (copies all text lines). Defaults to true. */
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

/**
 * Agent-native console surface. Renders on the dark "ink" palette regardless
 * of theme, with syntax-tinted lines and an optional copy button.
 */
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
  // Clear the "copied" pulse timer on unmount so a fast unmount within the
  // 1.2s window doesn't setState on an unmounted component.
  const copyTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => {
      if (copyTimer.current !== null) clearTimeout(copyTimer.current);
    };
  }, []);

  const onCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(plainText(lines));
      setCopied(true);
      if (copyTimer.current !== null) clearTimeout(copyTimer.current);
      copyTimer.current = setTimeout(() => {
        setCopied(false);
        copyTimer.current = null;
      }, 1200);
    } catch {
      // clipboard unavailable — silently ignore
    }
  }, [lines]);

  return (
    <div className={`loft-console ${className}`.trim()} style={{ height }}>
      {showHeader && (
        <div className="loft-console__header">
          {title && <span className="loft-console__title">{title}</span>}
          {lang && (
            <span
              className={`loft-console__lang${title ? " loft-console__lang--gap" : ""}`}
            >
              {lang}
            </span>
          )}
          <span className="loft-console__spacer" />
          {copy && (
            <Button variant="mono" onClick={onCopy} aria-label="Copy to clipboard">
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
      <div className="loft-console__body">
        {lines.map((l, i) => {
          if (l.node !== undefined) {
            return <div key={i}>{l.node}</div>;
          }
          const color = l.fg ?? kindColor[l.c ?? "plain"];
          return (
            <div key={i} className="loft-console__line" style={{ color }}>
              {l.text}
            </div>
          );
        })}
      </div>
    </div>
  );
}
