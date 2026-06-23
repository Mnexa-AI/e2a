import type { MDXComponents } from "mdx/types";

// Map MDX elements to styled HTML for the blog.
// Keep this list minimal — post-specific overrides can be passed in per-page.
export function useMDXComponents(components: MDXComponents): MDXComponents {
  return {
    h1: ({ children }) => (
      <h1
        style={{
          fontFamily: "var(--f-editorial)",
          fontSize: "clamp(32px, 3.5vw, 44px)",
          lineHeight: 1.15,
          fontWeight: 400,
          color: "#1C1A17",
          margin: "0 0 18px",
          letterSpacing: "-0.01em",
        }}
      >
        {children}
      </h1>
    ),
    h2: ({ children }) => (
      <h2
        style={{
          fontFamily: "var(--f-editorial)",
          fontSize: 26,
          fontWeight: 400,
          color: "#1C1A17",
          margin: "36px 0 12px",
          letterSpacing: "-0.005em",
        }}
      >
        {children}
      </h2>
    ),
    h3: ({ children }) => (
      <h3 style={{ fontSize: 18, fontWeight: 600, color: "#1C1A17", margin: "28px 0 10px" }}>
        {children}
      </h3>
    ),
    p: ({ children }) => (
      <p style={{ fontSize: 16, lineHeight: 1.7, color: "#3A342D", margin: "0 0 16px" }}>
        {children}
      </p>
    ),
    a: ({ href, children }) => (
      <a
        href={href}
        style={{ color: "#8B5E3C", textDecoration: "underline", textUnderlineOffset: "3px" }}
      >
        {children}
      </a>
    ),
    ul: ({ children }) => (
      <ul style={{ fontSize: 16, lineHeight: 1.7, color: "#3A342D", margin: "0 0 16px", paddingLeft: 22 }}>
        {children}
      </ul>
    ),
    ol: ({ children }) => (
      <ol style={{ fontSize: 16, lineHeight: 1.7, color: "#3A342D", margin: "0 0 16px", paddingLeft: 22 }}>
        {children}
      </ol>
    ),
    li: ({ children }) => <li style={{ margin: "0 0 6px" }}>{children}</li>,
    code: ({ children, ...props }) => {
      // Inline code when no className (block code gets className="language-*")
      if (!("className" in (props as Record<string, unknown>))) {
        return (
          <code
            style={{
              fontFamily: "var(--f-mono)",
              fontSize: "0.92em",
              background: "#F0EAE0",
              padding: "1px 6px",
              borderRadius: 4,
              border: "0.5px solid #E8E0D4",
              color: "#1C1A17",
            }}
          >
            {children}
          </code>
        );
      }
      return <code {...props}>{children}</code>;
    },
    pre: ({ children }) => (
      <pre
        style={{
          background: "#1C1A17",
          color: "#E8E0D4",
          padding: "20px 24px",
          borderRadius: 10,
          fontFamily: "var(--f-mono)",
          fontSize: 13,
          lineHeight: 1.7,
          overflowX: "auto",
          margin: "20px 0",
        }}
      >
        {children}
      </pre>
    ),
    blockquote: ({ children }) => (
      <blockquote
        style={{
          borderLeft: "3px solid #D4C9BC",
          paddingLeft: 18,
          margin: "20px 0",
          color: "#7A6F63",
          fontStyle: "italic",
        }}
      >
        {children}
      </blockquote>
    ),
    hr: () => <hr style={{ border: "none", borderTop: "0.5px solid #E8E0D4", margin: "36px 0" }} />,
    ...components,
  };
}
