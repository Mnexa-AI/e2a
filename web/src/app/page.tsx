"use client";

import Link from "next/link";
import { useState } from "react";
import { useAuth } from "./components/AuthProvider";
import { Eyebrow } from "@e2a/ui";
import { AGENTS_DOMAIN } from "../lib/site";

type Tab = "cli" | "claude" | "python" | "webhook";

// Intentionally NOT using AGENTS_DOMAIN_DISPLAY from lib/site — the
// in-app onboarding fallback "agents.example.com" hints at the
// shared-subdomain pattern, which makes sense once a user is signed
// in and choosing between shared / custom. The landing page shows
// this in a CLI sample where a neutral placeholder ("your-domain.com")
// reads better as a fill-in. Keep these two fallbacks distinct.
const exampleAgentDomain = AGENTS_DOMAIN || "your-domain.com";

const NAV_LINKS: { label: string; href: string; external?: boolean }[] = [
  { label: "How it works", href: "#how-it-works" },
  { label: "Human-in-the-loop", href: "#hitl" },
  { label: "Use cases", href: "#use-cases" },
  { label: "Blog", href: "/blog" },
];

const DOCS_LINKS: { label: string; href: string; external?: boolean }[] = [
  { label: "API Reference", href: "/api-docs" },
  { label: "Claude Code skill", href: "https://github.com/Mnexa-AI/e2a/blob/main/plugins/e2a/skills/e2a/SKILL.md", external: true },
  { label: "Python SDK", href: "https://pypi.org/project/e2a/", external: true },
  { label: "TypeScript SDK", href: "https://www.npmjs.com/package/@e2a/sdk", external: true },
  { label: "CLI", href: "https://www.npmjs.com/package/@e2a/cli", external: true },
];

const USE_CASES: { eyebrow: string; title: string; desc: string }[] = [
  { eyebrow: "Support", title: "Support and intake", desc: "Triage inbound requests, answer common questions, and hand off to humans without changing how customers reach you." },
  { eyebrow: "Admin", title: "Scheduling and admin", desc: "Coordinate meetings, send reminders, and follow up where most people already live — their inbox." },
  { eyebrow: "Sales", title: "Sales and follow-through", desc: "Qualify leads, reply to outreach, and keep conversations moving with a verified agent identity." },
  { eyebrow: "Auth", title: "OTP and verification", desc: "Receive verification codes, confirmation emails, and magic links — then act on them automatically." },
  { eyebrow: "Voice", title: "Voice agents", desc: "After a call ends, your voice agent sends a follow-up, receives a reply, and keeps the thread going." },
  { eyebrow: "Procurement", title: "Procurement", desc: "Coordinate with vendors, chase POs, and manage supplier threads with partners who still run on email." },
];

const FOOTER_LINKS: { label: string; href: string; external?: boolean }[] = [
  { label: "GitHub", href: "https://github.com/Mnexa-AI/e2a", external: true },
  { label: "API Docs", href: "/api-docs" },
  { label: "Blog", href: "/blog" },
  { label: "Python SDK", href: "https://pypi.org/project/e2a/", external: true },
  { label: "TypeScript SDK", href: "https://www.npmjs.com/package/@e2a/sdk", external: true },
  { label: "CLI", href: "https://www.npmjs.com/package/@e2a/cli", external: true },
  { label: "Claude Skill", href: "https://github.com/Mnexa-AI/e2a/blob/main/plugins/e2a/skills/e2a/SKILL.md", external: true },
  { label: "Feedback", href: "/feedback" },
];

export default function Home() {
  const { user, loading: checkingAuth } = useAuth();
  const [activeTab, setActiveTab] = useState<Tab>("cli");
  const [docsOpen, setDocsOpen] = useState(false);

  return (
    <div
      className="min-h-screen flex flex-col overflow-x-hidden w-full"
      style={{
        background: "var(--bg)",
        color: "var(--fg)",
        fontFamily: "var(--f-ui)",
      }}
    >
      {/* Nav */}
      <nav
        className="sticky top-0 z-50 backdrop-blur-md backdrop-saturate-150"
        style={{
          background: "color-mix(in srgb, var(--bg) 85%, transparent)",
          borderBottom: "1px solid var(--border)",
        }}
      >
        <div className="max-w-[1080px] mx-auto flex items-center justify-between gap-4 px-8 py-3.5">
          <div className="flex items-center gap-2.5">
            <Link
              href="/"
              className="font-mono font-bold text-[17px]"
              style={{ color: "var(--fg)", letterSpacing: "-0.02em" }}
            >
              e2a
            </Link>
            <span
              className="hidden md:inline font-mono text-[11px]"
              style={{ color: "var(--fg-subtle)", letterSpacing: "0.04em" }}
            >
              v1.0
            </span>
          </div>

          <div className="flex items-center gap-1 text-[13px]">
            <div className="hidden md:flex items-center gap-1">
              {NAV_LINKS.map((l) =>
                l.href.startsWith("#") ? (
                  <a
                    key={l.label}
                    href={l.href}
                    className="px-3 py-1.5 rounded-md transition hover:bg-[var(--bg-elev)]"
                    style={{ color: "var(--fg-muted)" }}
                  >
                    {l.label}
                  </a>
                ) : (
                  <Link
                    key={l.label}
                    href={l.href}
                    className="px-3 py-1.5 rounded-md transition hover:bg-[var(--bg-elev)]"
                    style={{ color: "var(--fg-muted)" }}
                  >
                    {l.label}
                  </Link>
                ),
              )}
              <div
                className="relative"
                onMouseEnter={() => setDocsOpen(true)}
                onMouseLeave={() => setDocsOpen(false)}
              >
                <button
                  type="button"
                  className="px-3 py-1.5 rounded-md transition hover:bg-[var(--bg-elev)]"
                  style={{ color: "var(--fg-muted)" }}
                >
                  Docs <span className="text-[10px]">▾</span>
                </button>
                {docsOpen && (
                  <div
                    className="absolute top-full left-0 min-w-[180px] py-1.5 z-50"
                    style={{
                      background: "var(--bg-panel)",
                      border: "1px solid var(--border)",
                      borderRadius: "var(--r-md)",
                      boxShadow: "var(--sh-2)",
                    }}
                  >
                    {DOCS_LINKS.map((d) =>
                      d.external ? (
                        <a
                          key={d.label}
                          href={d.href}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="block px-4 py-1.5 text-[13px] transition hover:bg-[var(--bg-elev)]"
                          style={{ color: "var(--fg-muted)" }}
                        >
                          {d.label}
                        </a>
                      ) : (
                        <Link
                          key={d.label}
                          href={d.href}
                          className="block px-4 py-1.5 text-[13px] transition hover:bg-[var(--bg-elev)]"
                          style={{ color: "var(--fg-muted)" }}
                        >
                          {d.label}
                        </Link>
                      ),
                    )}
                  </div>
                )}
              </div>
              <a
                href="https://github.com/Mnexa-AI/e2a"
                target="_blank"
                rel="noopener noreferrer"
                aria-label="View source on GitHub"
                className="inline-flex items-center px-2.5 py-1.5 rounded-md transition hover:bg-[var(--bg-elev)]"
                style={{ color: "var(--fg-muted)" }}
              >
                <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden>
                  <path d="M12 .5C5.65.5.5 5.65.5 12c0 5.08 3.29 9.39 7.86 10.91.58.1.79-.25.79-.56 0-.27-.01-1.16-.02-2.1-3.2.7-3.87-1.36-3.87-1.36-.52-1.32-1.27-1.67-1.27-1.67-1.04-.71.08-.7.08-.7 1.15.08 1.76 1.18 1.76 1.18 1.02 1.76 2.69 1.25 3.34.96.1-.74.4-1.25.72-1.54-2.55-.29-5.24-1.28-5.24-5.69 0-1.26.45-2.29 1.18-3.1-.12-.29-.51-1.46.11-3.04 0 0 .96-.31 3.15 1.18a10.95 10.95 0 0 1 5.74 0c2.19-1.49 3.15-1.18 3.15-1.18.62 1.58.23 2.75.11 3.04.74.81 1.18 1.84 1.18 3.1 0 4.42-2.69 5.4-5.25 5.68.41.36.78 1.06.78 2.13 0 1.54-.01 2.78-.01 3.16 0 .31.21.67.8.56C20.21 21.39 23.5 17.08 23.5 12 23.5 5.65 18.35.5 12 .5z" />
                </svg>
              </a>
              <span
                className="mx-1.5"
                style={{ width: 1, height: 18, background: "var(--border)" }}
              />
              {checkingAuth ? (
                <span className="px-3 text-[12px]" style={{ color: "var(--fg-subtle)" }}>
                  ...
                </span>
              ) : user ? (
                <Link
                  href="/inboxes"
                  className="px-3 py-1.5 rounded-md transition hover:bg-[var(--bg-elev)]"
                  style={{ color: "var(--fg-muted)" }}
                >
                  Go to Dashboard
                </Link>
              ) : (
                <a
                  href="/api/auth/login"
                  className="px-3 py-1.5 rounded-md transition hover:bg-[var(--bg-elev)]"
                  style={{ color: "var(--fg-muted)" }}
                >
                  Sign in
                </a>
              )}
            </div>
            <Link
              href="/get-started"
              className="ml-1 inline-flex items-center gap-1.5 px-3.5 py-1.5 font-medium transition"
              style={{
                background: "var(--fg)",
                color: "var(--bg)",
                borderRadius: "var(--r-md)",
              }}
            >
              Start building
              <span className="font-mono">→</span>
            </Link>
          </div>
        </div>
      </nav>

      {/* Hero */}
      <section className="relative overflow-hidden">
        <div
          aria-hidden
          className="absolute pointer-events-none"
          style={{
            top: -160,
            right: -180,
            width: 560,
            height: 560,
            background:
              "radial-gradient(circle at center, var(--accent-soft) 0%, rgba(0,0,0,0) 60%)",
          }}
        />
        <div
          aria-hidden
          className="absolute pointer-events-none"
          style={{
            bottom: -200,
            left: -200,
            width: 520,
            height: 520,
            background:
              "radial-gradient(circle at center, rgba(111,221,229,.18) 0%, rgba(0,0,0,0) 60%)",
          }}
        />
        <div className="relative max-w-[1080px] mx-auto px-6 md:px-8 pt-16 md:pt-[88px] pb-12 md:pb-16 text-center">
          <p
            className="inline-flex items-center gap-2 mb-6 font-mono text-[11px] font-semibold uppercase"
            style={{ color: "var(--success)", letterSpacing: "0.08em" }}
          >
            <span className="relative inline-flex w-2 h-2">
              <span
                className="absolute inline-flex w-full h-full rounded-full opacity-40 animate-ping"
                style={{ background: "var(--success)" }}
              />
              <span
                className="relative inline-flex w-2 h-2 rounded-full"
                style={{ background: "var(--success)" }}
              />
            </span>
            Now generally available · free to start
          </p>
          <h1
            className="mx-auto mb-6 leading-[1.05]"
            style={{
              fontFamily: "var(--f-editorial)",
              fontWeight: 400,
              fontSize: "clamp(40px, 6vw, 72px)",
              letterSpacing: "-0.012em",
              maxWidth: 880,
              color: "var(--fg)",
            }}
          >
            Give your agent an email address.{" "}
            <em
              style={{
                fontStyle: "italic",
                color: "var(--accent-strong)",
              }}
            >
              In under two minutes.
            </em>
          </h1>
          <p
            className="mx-auto mb-9 leading-[1.55]"
            style={{
              fontSize: 17,
              color: "var(--fg-muted)",
              maxWidth: 540,
            }}
          >
            Anyone can send an email — so your agent should have one. Signed identity, conversation threading, and a human-in-the-loop gate. No mail server. No public URL.
          </p>
          <div className="inline-flex flex-wrap items-center justify-center gap-2.5">
            <Link
              href="/get-started"
              className="inline-flex items-center gap-2 px-4 py-2.5 text-[14px] font-medium transition"
              style={{
                background: "var(--accent-fill)",
                color: "var(--accent-fg)",
                borderRadius: "var(--r-md)",
              }}
            >
              Get started free
              <span className="font-mono">→</span>
            </Link>
            <Link
              href="/api-docs"
              className="inline-flex items-center gap-2 px-4 py-2.5 text-[14px] font-medium transition"
              style={{
                background: "var(--bg-panel)",
                color: "var(--fg)",
                border: "1px solid var(--border)",
                borderRadius: "var(--r-md)",
              }}
            >
              Read the docs
            </Link>
          </div>

          <div
            className="mt-11 flex flex-wrap justify-center gap-x-6 gap-y-2 font-mono text-[12px]"
            style={{ color: "var(--fg-subtle)", letterSpacing: "0.02em" }}
          >
            <span>
              <span style={{ color: "var(--fg-muted)" }}>$</span>&nbsp;npm i -g @e2a/cli
            </span>
            <span style={{ color: "var(--border-strong)" }}>·</span>
            <span>
              <span style={{ color: "var(--fg-muted)" }}>$</span>&nbsp;pip install e2a
            </span>
            <span style={{ color: "var(--border-strong)" }}>·</span>
            <span>Apache 2.0</span>
          </div>

          <div className="mt-7 flex justify-center">
            <a
              href="https://www.producthunt.com/products/e2a-open-source-email-api-for-agents?embed=true&utm_source=badge-featured&utm_medium=badge&utm_campaign=badge-e2a-open-source-email-api-for-agents"
              target="_blank"
              rel="noopener noreferrer"
            >
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img
                src="https://api.producthunt.com/widgets/embed-image/v1/featured.svg?post_id=1145559&theme=light&t=1778615217650"
                alt="e2a – open-source email API for agents - Give your AI agents a real, authenticated email address. | Product Hunt"
                width={250}
                height={54}
                style={{ display: "block" }}
              />
            </a>
          </div>
        </div>
      </section>

      {/* Divider */}
      <div style={{ height: 1, background: "var(--border)" }} />

      {/* How it works */}
      <section id="how-it-works" className="px-6 md:px-8 py-12 md:py-16">
        <div className="max-w-[1080px] mx-auto">
          <div className="text-center mb-11">
            <Eyebrow>01 · How it works</Eyebrow>
            <h2
              className="mx-auto mt-3 mb-2"
              style={{
                fontFamily: "var(--f-editorial)",
                fontWeight: 400,
                fontSize: "clamp(28px, 4vw, 38px)",
                letterSpacing: "-0.01em",
                color: "var(--fg)",
              }}
            >
              Up and running in three steps.
            </h2>
            <p
              className="mx-auto max-w-[460px] text-[14px]"
              style={{ color: "var(--fg-muted)" }}
            >
              No mail server to configure. No custom inbox to build.
            </p>
          </div>
          <div
            className="grid grid-cols-1 md:grid-cols-3 overflow-hidden"
            style={{
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-lg)",
            }}
          >
            {[
              {
                num: "01",
                title: "Register your agent",
                desc: AGENTS_DOMAIN
                  ? `Sign in at e2a.dev/get-started, pick a slug, and you've got my-agent@${AGENTS_DOMAIN}. Or BYO domain — verify by DNS TXT. (Also creatable via MCP or the SDK.)`
                  : "Sign in at e2a.dev/get-started and bring your own domain — register it once and verify the DNS records. (Also creatable via MCP or the SDK.)",
                tag: "e2a.dev/get-started",
              },
              {
                num: "02",
                title: "Connect your agent",
                desc: "CLI, Python or TypeScript SDK, or the Claude Code skill. Local agents use WebSocket, cloud agents use webhooks — same delivery contract.",
                tag: "pip install e2a",
              },
              {
                num: "03",
                title: "Receive, reply, stay in thread",
                desc: "Inbound mail arrives signed: sender identity, SPF/DKIM verdict, and a conversation_id that survives the email ↔ structured-data boundary.",
                tag: "on_message(msg)",
              },
            ].map((step, i) => (
              <div
                key={step.num}
                className="px-7 py-8"
                style={{
                  borderLeft: i > 0 ? "1px solid var(--border)" : "none",
                  borderTop:
                    i > 0
                      ? "1px solid var(--border)"
                      : "none",
                }}
              >
                <div
                  className="font-mono text-[11px] font-semibold uppercase mb-4"
                  style={{
                    color: "var(--accent-strong)",
                    letterSpacing: "0.08em",
                  }}
                >
                  STEP {step.num}
                </div>
                <div
                  className="text-[18px] font-semibold mb-2"
                  style={{ color: "var(--fg)", letterSpacing: "-0.01em" }}
                >
                  {step.title}
                </div>
                <div
                  className="text-[13px] leading-[1.6] mb-4"
                  style={{ color: "var(--fg-muted)" }}
                >
                  {step.desc}
                </div>
                <span
                  className="inline-flex font-mono text-[12px] px-2.5 py-1"
                  style={{
                    color: "var(--fg)",
                    background: "var(--bg-elev)",
                    border: "1px solid var(--border-sub)",
                    borderRadius: "var(--r-sm)",
                  }}
                >
                  {step.tag}
                </span>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* Quick start */}
      <section
        className="px-6 md:px-8 py-12 md:py-16"
        style={{
          background: "var(--bg-elev)",
          borderTop: "1px solid var(--border)",
          borderBottom: "1px solid var(--border)",
        }}
      >
        <div className="max-w-[1080px] mx-auto">
          <div className="text-center mb-7">
            <Eyebrow>02 · Quick start</Eyebrow>
            <h2
              className="mt-3 mb-2"
              style={{
                fontFamily: "var(--f-editorial)",
                fontWeight: 400,
                fontSize: "clamp(28px, 4vw, 38px)",
                letterSpacing: "-0.01em",
                color: "var(--fg)",
              }}
            >
              A few lines of code.
            </h2>
            <p
              className="mx-auto max-w-[460px] text-[14px]"
              style={{ color: "var(--fg-muted)" }}
            >
              Pick your interface. Everything else is already wired up.
            </p>
          </div>

          <div className="flex justify-center mb-4">
            <div
              className="inline-flex gap-0.5 p-1"
              style={{
                background: "var(--bg-panel)",
                border: "1px solid var(--border)",
                borderRadius: "var(--r-md)",
              }}
            >
              {(["cli", "claude", "python", "webhook"] as Tab[]).map((tab) => (
                <button
                  key={tab}
                  type="button"
                  onClick={() => setActiveTab(tab)}
                  className="px-3.5 py-1.5 text-[12px] font-medium transition"
                  style={{
                    background: activeTab === tab ? "var(--fg)" : "transparent",
                    color:
                      activeTab === tab ? "var(--bg)" : "var(--fg-muted)",
                    borderRadius: "var(--r-sm)",
                  }}
                >
                  {tab === "cli"
                    ? "CLI"
                    : tab === "claude"
                      ? "Claude Code"
                      : tab === "python"
                        ? "Python"
                        : "Webhook"}
                </button>
              ))}
            </div>
          </div>

          <CodeBlock>
            {activeTab === "cli" && (
              <>
                <Line c="comment"># install</Line>
                <Line>
                  <Tok c="accent">npm install</Tok> -g @e2a/cli
                </Line>
                <Line>&nbsp;</Line>
                <Line c="comment"># sign in (register your agent at e2a.dev/get-started)</Line>
                <Line>e2a login</Line>
                <Line>&nbsp;</Line>
                <Line c="comment"># listen for inbound email, forward to your local server</Line>
                <Line>
                  e2a listen <Tok c="flag">--agent</Tok> my-agent@{exampleAgentDomain} <Tok c="flag">--forward</Tok> http://localhost:3000
                </Line>
              </>
            )}
            {activeTab === "claude" && (
              <>
                <Line c="comment"># connect Claude Code to e2a over MCP (OAuth in the browser)</Line>
                <Line>claude mcp add <Tok c="flag">--transport</Tok> http <Tok c="flag">--scope</Tok> user \</Line>
                <Line>&nbsp;&nbsp;e2a https://api.e2a.dev/mcp</Line>
                <Line>&nbsp;</Line>
                <Line c="comment"># then just ask Claude in plain language:</Line>
                <Line c="comment">#   &quot;Create an email agent and listen for new mail.&quot;</Line>
                <Line>&nbsp;</Line>
                <Line c="comment"># works with Cursor, Codex, Windsurf — any MCP-aware agent</Line>
              </>
            )}
            {activeTab === "python" && (
              <>
                <Line>
                  <Tok c="keyword">from</Tok> e2a.v1 <Tok c="keyword">import</Tok> AsyncE2AClient
                </Line>
                <Line>&nbsp;</Line>
                <Line c="comment"># conversation_id threads multi-turn replies</Line>
                <Line>
                  <Tok c="keyword">async with</Tok> <Tok c="fn">AsyncE2AClient</Tok>(api_key=<Tok c="string">&quot;e2a_…&quot;</Tok>) <Tok c="keyword">as</Tok> client:
                </Line>
                <Line>
                  &nbsp;&nbsp;<Tok c="keyword">async for</Tok> n <Tok c="keyword">in</Tok> client.<Tok c="fn">listen</Tok>(<Tok c="string">{`"my-agent@${exampleAgentDomain}"`}</Tok>):
                </Line>
                <Line>
                  &nbsp;&nbsp;&nbsp;&nbsp;msg = <Tok c="keyword">await</Tok> client.messages.<Tok c="fn">get</Tok>(n.delivered_to, n.message_id)
                </Line>
                <Line>
                  &nbsp;&nbsp;&nbsp;&nbsp;<Tok c="fn">print</Tok>(msg.subject, n.conversation_id)
                </Line>
                <Line>
                  &nbsp;&nbsp;&nbsp;&nbsp;<Tok c="keyword">await</Tok> client.messages.<Tok c="fn">reply</Tok>(n.delivered_to, n.message_id, {`{`}<Tok c="string">&quot;text&quot;</Tok>: <Tok c="string">&quot;Got it, on it.&quot;</Tok>{`}`})
                </Line>
              </>
            )}
            {activeTab === "webhook" && (
              <>
                <Line c="comment"># 1. create the agent</Line>
                <Line>curl -X POST https://api.e2a.dev/v1/agents \</Line>
                <Line>&nbsp;&nbsp;-H <Tok c="string">{`"Authorization: Bearer $E2A_API_KEY"`}</Tok> \</Line>
                <Line>&nbsp;&nbsp;-d <Tok c="string">{`'{"email":"my-agent@agents.e2a.dev"}'`}</Tok></Line>
                <Line>&nbsp;</Line>
                <Line c="comment"># 2. subscribe a webhook to receive inbound mail</Line>
                <Line>curl -X POST https://api.e2a.dev/v1/webhooks \</Line>
                <Line>&nbsp;&nbsp;-H <Tok c="string">{`"Authorization: Bearer $E2A_API_KEY"`}</Tok> \</Line>
                <Line>&nbsp;&nbsp;-d <Tok c="string">{`'{"url":"https://your-app.com/inbox","events":["email.received"]}'`}</Tok></Line>
                <Line>&nbsp;</Line>
                <Line c="comment"># e2a POSTs verified payloads to your endpoint with HMAC signature</Line>
              </>
            )}
          </CodeBlock>
        </div>
      </section>

      {/* Human-in-the-loop */}
      <section id="hitl" className="px-6 md:px-8 py-14 md:py-[72px]">
        <div className="max-w-[1080px] mx-auto grid grid-cols-1 md:grid-cols-2 gap-10 md:gap-14 items-center">
          <div>
            <Eyebrow>03 · Human-in-the-loop</Eyebrow>
            <h2
              className="mt-3.5 mb-4 leading-[1.1]"
              style={{
                fontFamily: "var(--f-editorial)",
                fontWeight: 400,
                fontSize: "clamp(30px, 4.5vw, 44px)",
                letterSpacing: "-0.01em",
                color: "var(--fg)",
              }}
            >
              Approve{" "}
              <em style={{ color: "var(--accent-strong)" }}>before</em> your agent hits send.
            </h2>
            <p
              className="mb-3.5 leading-[1.6]"
              style={{ fontSize: 15, color: "var(--fg-muted)" }}
            >
              Flip one switch and outbound messages pause for your review instead of going straight out. You get a notification — click to see recipients, subject, and body on a secure confirmation page. Approve, edit, or reject.
            </p>
            <p
              className="mb-5 leading-[1.65]"
              style={{ fontSize: 13, color: "var(--fg-muted)" }}
            >
              Per-agent, opt-in, off by default. Configurable TTL with auto-approve or auto-reject on expiry. Reviewable from the dashboard, SDK, or one-click magic links in your inbox.
            </p>
            <div className="flex flex-wrap items-center gap-2.5">
              <Link
                href="/blog/human-in-the-loop-for-agent-email"
                className="inline-flex items-center gap-2 px-4 py-2.5 text-[14px] font-medium"
                style={{
                  background: "var(--accent-fill)",
                  color: "var(--accent-fg)",
                  borderRadius: "var(--r-md)",
                }}
              >
                Read the announcement
                <span className="font-mono">→</span>
              </Link>
              <Link
                href="/inboxes"
                className="inline-flex items-center px-4 py-2.5 text-[14px] font-medium"
                style={{
                  background: "var(--bg-panel)",
                  color: "var(--fg)",
                  border: "1px solid var(--border)",
                  borderRadius: "var(--r-md)",
                }}
              >
                Enable on an agent
              </Link>
            </div>
          </div>

          <CodeBlock title="hitl.ts · sdk">
            <Line c="comment">{"// Turn on HITL in the dashboard, or hold outbound for"}</Line>
            <Line c="comment">{"// review via the agent's protection config."}</Line>
            <Line>&nbsp;</Line>
            <Line c="comment">{"// review held messages with an account-scoped key"}</Line>
            <Line>
              <Tok c="keyword">const</Tok> held = <Tok c="keyword">await</Tok> client.reviews.<Tok c="fn">list</Tok>().<Tok c="fn">toArray</Tok>();
            </Line>
            <Line c="dim">&nbsp;&nbsp;msg_abc123  customer@acme.io   <Tok c="warn">in 47m</Tok></Line>
            <Line c="dim">&nbsp;&nbsp;msg_def456  legal@stripe.com   <Tok c="warn">in 2h 12m</Tok></Line>
            <Line>&nbsp;</Line>
            <Line c="comment">{"// approve (sends it) or reject with a reason"}</Line>
            <Line>
              <Tok c="keyword">await</Tok> client.reviews.<Tok c="fn">approve</Tok>(<Tok c="string">&quot;msg_abc123&quot;</Tok>);
            </Line>
            <Line c="success">&nbsp;&nbsp;→ approved · delivering now</Line>
          </CodeBlock>
        </div>
      </section>

      {/* Use cases */}
      <section
        id="use-cases"
        className="px-6 md:px-8 py-12 md:py-16"
        style={{
          background: "var(--bg-elev)",
          borderTop: "1px solid var(--border)",
          borderBottom: "1px solid var(--border)",
        }}
      >
        <div className="max-w-[1080px] mx-auto">
          <div className="text-center mb-10">
            <Eyebrow>04 · Use cases</Eyebrow>
            <h2
              className="mt-3 mb-2"
              style={{
                fontFamily: "var(--f-editorial)",
                fontWeight: 400,
                fontSize: "clamp(28px, 4vw, 38px)",
                letterSpacing: "-0.01em",
                color: "var(--fg)",
              }}
            >
              What you can build.
            </h2>
            <p
              className="mx-auto max-w-[460px] text-[14px]"
              style={{ color: "var(--fg-muted)" }}
            >
              If it can receive email and take action, e2a can power it.
            </p>
          </div>

          <div
            className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 overflow-hidden"
            style={{
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-lg)",
            }}
          >
            {USE_CASES.map((u, i) => {
              const col3 = i % 3;
              const lastRow = i >= USE_CASES.length - 3;
              return (
                <div
                  key={u.title}
                  className="p-6 md:p-7"
                  style={{
                    borderRight:
                      col3 < 2 ? "1px solid var(--border)" : "none",
                    borderBottom: lastRow
                      ? "none"
                      : "1px solid var(--border)",
                  }}
                >
                  <div
                    className="font-mono text-[11px] font-semibold uppercase mb-2.5"
                    style={{
                      color: "var(--accent-strong)",
                      letterSpacing: "0.08em",
                    }}
                  >
                    {u.eyebrow}
                  </div>
                  <div
                    className="text-[15px] font-semibold mb-1.5"
                    style={{ color: "var(--fg)" }}
                  >
                    {u.title}
                  </div>
                  <div
                    className="text-[13px] leading-[1.6]"
                    style={{ color: "var(--fg-muted)" }}
                  >
                    {u.desc}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      </section>

      {/* CTA */}
      <section className="px-6 md:px-8 py-14 md:py-[72px] text-center">
        <div className="max-w-[720px] mx-auto">
          <h2
            className="mb-3.5 leading-[1.05]"
            style={{
              fontFamily: "var(--f-editorial)",
              fontWeight: 400,
              fontSize: "clamp(32px, 4.5vw, 46px)",
              letterSpacing: "-0.012em",
              color: "var(--fg)",
            }}
          >
            Your agent&apos;s inbox is{" "}
            <em style={{ color: "var(--accent-strong)" }}>one sign-in</em> away.
          </h2>
          <p
            className="mb-7 leading-[1.55]"
            style={{ fontSize: 15, color: "var(--fg-muted)" }}
          >
            Free to start. No credit card. Up and running in under two minutes.
          </p>
          <div className="inline-flex flex-wrap items-center justify-center gap-2.5">
            <Link
              href="/get-started"
              className="inline-flex items-center gap-2 px-4 py-2.5 text-[14px] font-medium"
              style={{
                background: "var(--accent-fill)",
                color: "var(--accent-fg)",
                borderRadius: "var(--r-md)",
              }}
            >
              Get started free
              <span className="font-mono">→</span>
            </Link>
            <Link
              href="/api-docs"
              className="inline-flex items-center px-4 py-2.5 text-[14px] font-medium"
              style={{
                background: "var(--bg-panel)",
                color: "var(--fg)",
                border: "1px solid var(--border)",
                borderRadius: "var(--r-md)",
              }}
            >
              Read the docs
            </Link>
          </div>
          <p
            className="mt-4 text-[12px]"
            style={{ color: "var(--fg-subtle)" }}
          >
            Have feedback?{" "}
            <Link
              href="/feedback"
              style={{ color: "var(--accent-strong)" }}
            >
              We&apos;d love to hear from you.
            </Link>
          </p>
        </div>
      </section>

      {/* Footer */}
      <footer
        className="px-6 md:px-8 py-6"
        style={{ borderTop: "1px solid var(--border)" }}
      >
        <div className="max-w-[1080px] mx-auto flex flex-col md:flex-row md:items-center md:justify-between gap-4">
          <div className="flex items-center gap-3.5">
            <span
              className="font-mono font-bold text-[15px]"
              style={{ color: "var(--fg)", letterSpacing: "-0.02em" }}
            >
              e2a
            </span>
            <span
              className="font-mono text-[12px]"
              style={{ color: "var(--fg-muted)" }}
            >
              · Email for agents
            </span>
          </div>
          <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-[12px]">
            {FOOTER_LINKS.map((l) =>
              l.external ? (
                <a
                  key={l.label}
                  href={l.href}
                  target="_blank"
                  rel="noopener noreferrer"
                  style={{ color: "var(--fg-muted)" }}
                >
                  {l.label}
                </a>
              ) : (
                <Link
                  key={l.label}
                  href={l.href}
                  style={{ color: "var(--fg-muted)" }}
                >
                  {l.label}
                </Link>
              ),
            )}
          </div>
          <span
            className="font-mono text-[12px]"
            style={{ color: "var(--fg-subtle)" }}
          >
            Apache 2.0
          </span>
        </div>
      </footer>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────
// Local helpers — small enough to live next to the page rather than become
// reusable primitives. The shared InkConsole primitive takes a `lines`
// array; here we want JSX children so syntax highlighting reads inline.
// ─────────────────────────────────────────────────────────────────────────

function CodeBlock({
  children,
  title,
}: {
  children: React.ReactNode;
  title?: string;
}) {
  return (
    <div
      className="overflow-hidden font-mono"
      style={{
        background: "var(--ink)",
        border: "1px solid var(--ink-border)",
        borderRadius: "var(--r-lg)",
        boxShadow:
          "0 1px 0 rgba(255,255,255,.03) inset, 0 6px 24px rgba(20,15,8,.08)",
      }}
    >
      <div
        className="flex items-center gap-2.5 px-4 py-2.5"
        style={{
          borderBottom: "1px solid var(--ink-border)",
          background: "var(--ink-elev)",
        }}
      >
        <span className="w-2.5 h-2.5 rounded-full" style={{ background: "#3a342e" }} />
        <span className="w-2.5 h-2.5 rounded-full" style={{ background: "#3a342e" }} />
        <span className="w-2.5 h-2.5 rounded-full" style={{ background: "#3a342e" }} />
        <span
          className="ml-2 text-[11px]"
          style={{ color: "var(--ink-fg-muted)", letterSpacing: "0.04em" }}
        >
          {title ?? "~/my-agent"}
        </span>
      </div>
      <div
        className="px-5 py-4 text-[12.5px] leading-[1.75] overflow-x-auto"
        style={{ color: "var(--ink-fg)" }}
      >
        {children}
      </div>
    </div>
  );
}

type LineKind = "plain" | "comment" | "dim" | "success";

function Line({
  children,
  c = "plain",
}: {
  children: React.ReactNode;
  c?: LineKind;
}) {
  const color =
    c === "comment"
      ? "var(--ink-fg-muted)"
      : c === "dim"
        ? "var(--ink-fg-muted)"
        : c === "success"
          ? "var(--machine)"
          : "var(--ink-fg)";
  return <div style={{ color }}>{children}</div>;
}

function Tok({
  children,
  c,
}: {
  children: React.ReactNode;
  c: "prompt" | "string" | "keyword" | "fn" | "flag" | "accent" | "warn";
}) {
  const color =
    c === "prompt"
      ? "var(--machine)"
      : c === "string"
        ? "var(--spectral)"
        : c === "keyword"
          ? "#C8A8FF"
          : c === "fn"
            ? "var(--spectral)"
            : c === "flag"
              ? "var(--machine)"
              : c === "accent"
                ? "var(--accent)"
                : "var(--warn)";
  const extra =
    c === "prompt" ? { userSelect: "none" as const, marginRight: 8 } : {};
  return <span style={{ color, ...extra }}>{children}</span>;
}
