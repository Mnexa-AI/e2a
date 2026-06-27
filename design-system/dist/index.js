"use client";

// src/Button/Button.tsx
import { jsx } from "react/jsx-runtime";
function Button({
  variant = "primary",
  className = "",
  type = "button",
  children,
  ...rest
}) {
  return /* @__PURE__ */ jsx(
    "button",
    {
      type,
      className: `loft-btn loft-btn--${variant} ${className}`.trim(),
      ...rest,
      children
    }
  );
}

// src/Chip/Chip.tsx
import { jsx as jsx2 } from "react/jsx-runtime";
function Chip({
  children,
  tone = "neutral",
  mono = false,
  className = ""
}) {
  return /* @__PURE__ */ jsx2(
    "span",
    {
      className: `loft-chip loft-chip--${tone}${mono ? " loft-chip--mono" : ""} ${className}`.trim(),
      children
    }
  );
}

// src/Dot/Dot.tsx
import { jsx as jsx3 } from "react/jsx-runtime";
function Dot({ tone = "success" }) {
  return /* @__PURE__ */ jsx3("span", { "aria-hidden": true, className: `loft-dot loft-dot--${tone}` });
}

// src/Eyebrow/Eyebrow.tsx
import { jsx as jsx4 } from "react/jsx-runtime";
function Eyebrow({ children, className = "" }) {
  return /* @__PURE__ */ jsx4("span", { className: `loft-eyebrow ${className}`.trim(), children });
}

// src/ThemeToggle/ThemeToggle.tsx
import { Fragment, jsx as jsx5, jsxs } from "react/jsx-runtime";
var OPTIONS = [
  {
    value: "system",
    label: "System theme",
    icon: /* @__PURE__ */ jsxs(Fragment, { children: [
      /* @__PURE__ */ jsx5("rect", { x: "3", y: "4", width: "18", height: "12", rx: "2" }),
      /* @__PURE__ */ jsx5("path", { d: "M8 20h8M12 16v4" })
    ] })
  },
  {
    value: "light",
    label: "Light theme",
    icon: /* @__PURE__ */ jsxs(Fragment, { children: [
      /* @__PURE__ */ jsx5("circle", { cx: "12", cy: "12", r: "4" }),
      /* @__PURE__ */ jsx5("path", { d: "M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" })
    ] })
  },
  {
    value: "dark",
    label: "Dark theme",
    icon: /* @__PURE__ */ jsx5("path", { d: "M21 12.8A9 9 0 1111.2 3a7 7 0 009.8 9.8z" })
  }
];
function ThemeToggle({ value, onChange, className = "" }) {
  return /* @__PURE__ */ jsx5(
    "div",
    {
      role: "radiogroup",
      "aria-label": "Color theme",
      className: `loft-seg ${className}`.trim(),
      children: OPTIONS.map((opt) => {
        const active = value === opt.value;
        return /* @__PURE__ */ jsx5(
          "button",
          {
            type: "button",
            role: "radio",
            "aria-checked": active,
            "aria-label": opt.label,
            title: opt.label,
            onClick: () => onChange(opt.value),
            className: "loft-seg__opt",
            children: /* @__PURE__ */ jsx5(
              "svg",
              {
                width: "15",
                height: "15",
                viewBox: "0 0 24 24",
                fill: "none",
                stroke: "currentColor",
                strokeWidth: "1.6",
                strokeLinecap: "round",
                strokeLinejoin: "round",
                "aria-hidden": true,
                children: opt.icon
              }
            )
          },
          opt.value
        );
      })
    }
  );
}

// src/InkConsole/InkConsole.tsx
import { useCallback, useEffect, useRef, useState } from "react";
import { jsx as jsx6, jsxs as jsxs2 } from "react/jsx-runtime";
var kindColor = {
  comment: "var(--ink-fg-muted)",
  prompt: "var(--machine)",
  string: "var(--spectral)",
  accent: "var(--accent)",
  plain: "var(--ink-fg)"
};
function plainText(lines) {
  return lines.map((l) => l.node ? "" : l.text ?? "").filter(Boolean).join("\n");
}
function InkConsole({
  lines,
  title,
  lang,
  copy = true,
  height,
  className = ""
}) {
  const showHeader = Boolean(title || lang || copy);
  const [copied, setCopied] = useState(false);
  const copyTimer = useRef(null);
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
    }
  }, [lines]);
  return /* @__PURE__ */ jsxs2("div", { className: `loft-console ${className}`.trim(), style: { height }, children: [
    showHeader && /* @__PURE__ */ jsxs2("div", { className: "loft-console__header", children: [
      title && /* @__PURE__ */ jsx6("span", { className: "loft-console__title", children: title }),
      lang && /* @__PURE__ */ jsx6(
        "span",
        {
          className: `loft-console__lang${title ? " loft-console__lang--gap" : ""}`,
          children: lang
        }
      ),
      /* @__PURE__ */ jsx6("span", { className: "loft-console__spacer" }),
      copy && /* @__PURE__ */ jsxs2(Button, { variant: "mono", onClick: onCopy, "aria-label": "Copy to clipboard", children: [
        /* @__PURE__ */ jsxs2(
          "svg",
          {
            width: "10",
            height: "10",
            viewBox: "0 0 24 24",
            fill: "none",
            stroke: "currentColor",
            strokeWidth: 2,
            "aria-hidden": true,
            children: [
              /* @__PURE__ */ jsx6("rect", { x: "9", y: "9", width: "11", height: "11", rx: "2" }),
              /* @__PURE__ */ jsx6("path", { d: "M5 15V5a2 2 0 012-2h10" })
            ]
          }
        ),
        copied ? "copied" : "copy"
      ] })
    ] }),
    /* @__PURE__ */ jsx6("div", { className: "loft-console__body", children: lines.map((l, i) => {
      if (l.node !== void 0) {
        return /* @__PURE__ */ jsx6("div", { children: l.node }, i);
      }
      const color = l.fg ?? kindColor[l.c ?? "plain"];
      return /* @__PURE__ */ jsx6("div", { className: "loft-console__line", style: { color }, children: l.text }, i);
    }) })
  ] });
}

// src/Logo/Logo.tsx
import { jsx as jsx7, jsxs as jsxs3 } from "react/jsx-runtime";
var FONT = "var(--f-ui), 'Inter', ui-sans-serif, system-ui, -apple-system, 'Helvetica Neue', Arial, sans-serif";
var fillStyle = (value) => ({ fill: value });
function Logo({
  variant = "wordmark",
  tone = "color",
  height,
  title = "e2a",
  className,
  style
}) {
  const mono = tone === "mono";
  if (variant === "mark") {
    const h2 = height ?? 32;
    return /* @__PURE__ */ jsxs3(
      "svg",
      {
        role: "img",
        "aria-label": title,
        className,
        style,
        width: h2,
        height: h2,
        viewBox: "0 0 256 256",
        children: [
          /* @__PURE__ */ jsx7(
            "rect",
            {
              width: "256",
              height: "256",
              rx: "56",
              style: mono ? { fill: "none", stroke: "currentColor", strokeWidth: 12 } : { fill: "var(--ink)" }
            }
          ),
          /* @__PURE__ */ jsx7(
            "text",
            {
              x: "128",
              y: "178",
              textAnchor: "middle",
              fontWeight: 700,
              fontSize: 200,
              letterSpacing: -12,
              style: { ...fillStyle(mono ? "currentColor" : "var(--ink-fg)"), fontFamily: FONT },
              children: "2"
            }
          )
        ]
      }
    );
  }
  const h = height ?? 24;
  const w = h * 3.2;
  const ink = tone === "ink";
  const textFill = mono ? "currentColor" : ink ? "var(--ink-fg)" : "var(--fg)";
  const twoFill = mono ? "currentColor" : "var(--accent)";
  return /* @__PURE__ */ jsxs3(
    "svg",
    {
      role: "img",
      "aria-label": title,
      className,
      style,
      width: w,
      height: h,
      viewBox: "0 0 640 200",
      children: [
        ink && /* @__PURE__ */ jsx7("rect", { width: "640", height: "200", style: fillStyle("var(--ink)") }),
        /* @__PURE__ */ jsxs3(
          "text",
          {
            x: "320",
            y: "148",
            textAnchor: "middle",
            fontWeight: 600,
            fontSize: 176,
            letterSpacing: -12,
            style: { ...fillStyle(textFill), fontFamily: FONT },
            children: [
              /* @__PURE__ */ jsx7("tspan", { children: "e" }),
              /* @__PURE__ */ jsx7("tspan", { style: fillStyle(twoFill), children: "2" }),
              /* @__PURE__ */ jsx7("tspan", { children: "a" })
            ]
          }
        )
      ]
    }
  );
}

// src/Field/Field.tsx
import { jsx as jsx8, jsxs as jsxs4 } from "react/jsx-runtime";
function Field({
  label,
  hint,
  value,
  onChange,
  className = "",
  type = "text",
  ...props
}) {
  return /* @__PURE__ */ jsxs4("label", { className: `loft-field ${className}`.trim(), children: [
    /* @__PURE__ */ jsx8("span", { className: "loft-field__label", children: label }),
    /* @__PURE__ */ jsx8(
      "input",
      {
        ...props,
        type,
        className: "loft-field__input",
        value,
        onChange: (e) => onChange(e.target.value)
      }
    ),
    hint && /* @__PURE__ */ jsx8("span", { className: "loft-field__hint", children: hint })
  ] });
}

// src/Avatar/Avatar.tsx
import { jsx as jsx9 } from "react/jsx-runtime";
function hashTo8(input) {
  let h = 0;
  for (let i = 0; i < input.length; i++) {
    h = h * 31 + input.charCodeAt(i) | 0;
  }
  return (h % 8 + 8) % 8 + 1;
}
function initials(name, email) {
  const trimmed = name?.trim();
  if (trimmed) {
    const parts = trimmed.split(/\s+/);
    if (parts.length >= 2) {
      return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
    }
    return parts[0].slice(0, 2).toUpperCase();
  }
  const local = (email ?? "").split("@")[0] || email || "?";
  return local.slice(0, 2).toUpperCase();
}
function Avatar({ name, email, size = 24 }) {
  const seed = (email || name || "").toLowerCase();
  const bucket = hashTo8(seed);
  return /* @__PURE__ */ jsx9(
    "span",
    {
      "aria-hidden": true,
      style: {
        width: size,
        height: size,
        borderRadius: 4,
        background: `var(--av-${bucket})`,
        color: "#fff",
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        fontSize: Math.round(size * 0.42),
        fontWeight: 700,
        flexShrink: 0,
        letterSpacing: "0.02em"
      },
      children: initials(name, email)
    }
  );
}

// src/Collapsible/Collapsible.tsx
import { useState as useState2, useSyncExternalStore } from "react";
import { jsx as jsx10, jsxs as jsxs5 } from "react/jsx-runtime";
function usePrefersReducedMotion() {
  return useSyncExternalStore(
    subscribeReducedMotion,
    getReducedMotionSnapshot,
    getReducedMotionServerSnapshot
  );
}
function subscribeReducedMotion(onChange) {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return () => {
    };
  }
  const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
  mq.addEventListener("change", onChange);
  return () => mq.removeEventListener("change", onChange);
}
function getReducedMotionSnapshot() {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return false;
  }
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}
function getReducedMotionServerSnapshot() {
  return false;
}
function Collapsible({
  label,
  meta,
  defaultOpen = false,
  open: controlledOpen,
  onOpenChange,
  children
}) {
  const [uncontrolled, setUncontrolled] = useState2(defaultOpen);
  const open = controlledOpen ?? uncontrolled;
  const reduced = usePrefersReducedMotion();
  const setOpen = (next) => {
    if (controlledOpen === void 0) setUncontrolled(next);
    onOpenChange?.(next);
  };
  return /* @__PURE__ */ jsxs5("section", { className: "loft-collapsible", children: [
    /* @__PURE__ */ jsxs5(
      "button",
      {
        type: "button",
        onClick: () => setOpen(!open),
        "aria-expanded": open,
        className: `loft-collapsible__trigger${open ? " loft-collapsible__trigger--open" : ""}`,
        children: [
          /* @__PURE__ */ jsx10(
            "span",
            {
              "aria-hidden": true,
              className: "loft-collapsible__chevron",
              style: {
                transform: open ? "rotate(90deg)" : "rotate(0deg)",
                transition: reduced ? "none" : "transform 120ms ease"
              },
              children: "\u25B6"
            }
          ),
          /* @__PURE__ */ jsx10(Eyebrow, { children: label }),
          /* @__PURE__ */ jsx10("span", { className: "loft-collapsible__spacer" }),
          meta && /* @__PURE__ */ jsx10("span", { className: "loft-collapsible__meta", children: meta })
        ]
      }
    ),
    open && children
  ] });
}

// src/Card/Card.tsx
import { jsx as jsx11 } from "react/jsx-runtime";
function Card({ className = "", children, ...props }) {
  return /* @__PURE__ */ jsx11("div", { className: `loft-card ${className}`.trim(), ...props, children });
}
export {
  Avatar,
  Button,
  Card,
  Chip,
  Collapsible,
  Dot,
  Eyebrow,
  Field,
  InkConsole,
  Logo,
  ThemeToggle
};
