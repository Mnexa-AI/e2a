"use client";

import Link from "next/link";
import { useState } from "react";
import { useAuth } from "./components/AuthProvider";
import { SITE_URL, AGENTS_DOMAIN } from "../lib/site";

type Tab = "cli" | "claude" | "python" | "webhook";

// Display string used in marketing-copy code samples. Falls back to a generic
// placeholder when the deployment doesn't expose a shared domain so the
// landing page still renders sensibly on a self-host that hasn't set
// NEXT_PUBLIC_AGENTS_DOMAIN.
const exampleAgentDomain = AGENTS_DOMAIN || "your-domain.com";

export default function Home() {
  const { user, loading: checkingAuth } = useAuth();
  const [activeTab, setActiveTab] = useState<Tab>("cli");
  const [docsOpen, setDocsOpen] = useState(false);

  return (
    <div className="min-h-screen flex flex-col overflow-x-hidden w-full" style={{ background: "#FDFAF6", color: "#1C1A17", fontFamily: "var(--font-sans, system-ui)" }}>

      {/* Nav */}
      <nav style={{ borderBottom: "0.5px solid #E8E0D4", background: "#FDFAF6", position: "sticky", top: 0, zIndex: 50 }}>
        <div style={{ maxWidth: 960, margin: "0 auto", display: "flex", alignItems: "center", justifyContent: "space-between", padding: "14px 32px" }}>
          <Link href="/" style={{ fontFamily: "'IBM Plex Mono', monospace", fontWeight: 600, fontSize: 15, color: "#1C1A17", textDecoration: "none" }}>
            e2a
          </Link>
          <div style={{ display: "flex", alignItems: "center", gap: 2 }}>
            <a href="#how-it-works" className="nav-secondary-link" style={{ fontSize: 13, color: "#7A6F63", padding: "6px 10px", borderRadius: 6, textDecoration: "none" }}>How it works</a>
            <a href="#hitl" className="nav-secondary-link" style={{ fontSize: 13, color: "#7A6F63", padding: "6px 10px", borderRadius: 6, textDecoration: "none" }}>Human-in-the-loop</a>
            <a href="#use-cases" className="nav-secondary-link" style={{ fontSize: 13, color: "#7A6F63", padding: "6px 10px", borderRadius: 6, textDecoration: "none" }}>Use cases</a>
            <Link href="/blog" className="nav-secondary-link" style={{ fontSize: 13, color: "#7A6F63", padding: "6px 10px", borderRadius: 6, textDecoration: "none" }}>Blog</Link>
            <div className="nav-secondary-link" style={{ position: "relative" }} onMouseEnter={() => setDocsOpen(true)} onMouseLeave={() => setDocsOpen(false)}>
              <button style={{ fontSize: 13, color: "#7A6F63", padding: "6px 10px", borderRadius: 6, background: "none", border: "none", cursor: "pointer", fontFamily: "inherit" }}>
                Docs <span style={{ fontSize: 10 }}>▾</span>
              </button>
              {docsOpen && (
                <div style={{ position: "absolute", top: "100%", left: 0, background: "#FDFAF6", border: "0.5px solid #E8E0D4", borderRadius: 8, padding: "6px 0", minWidth: 160, boxShadow: "0 4px 12px rgba(0,0,0,0.08)", zIndex: 100 }}>
                  <Link href="/api-docs" style={{ display: "block", fontSize: 13, color: "#7A6F63", padding: "7px 16px", textDecoration: "none" }} onMouseEnter={e => (e.currentTarget.style.background = "#F5EDE3")} onMouseLeave={e => (e.currentTarget.style.background = "transparent")}>API Reference</Link>
                  <a href="https://github.com/Mnexa-AI/e2a-claude-code-skill" target="_blank" rel="noopener noreferrer" style={{ display: "block", fontSize: 13, color: "#7A6F63", padding: "7px 16px", textDecoration: "none" }} onMouseEnter={e => (e.currentTarget.style.background = "#F5EDE3")} onMouseLeave={e => (e.currentTarget.style.background = "transparent")}>Claude Code skill</a>
                  <a href="https://pypi.org/project/e2a/" target="_blank" rel="noopener noreferrer" style={{ display: "block", fontSize: 13, color: "#7A6F63", padding: "7px 16px", textDecoration: "none" }} onMouseEnter={e => (e.currentTarget.style.background = "#F5EDE3")} onMouseLeave={e => (e.currentTarget.style.background = "transparent")}>Python SDK</a>
                  <a href="https://www.npmjs.com/package/@e2a/sdk" target="_blank" rel="noopener noreferrer" style={{ display: "block", fontSize: 13, color: "#7A6F63", padding: "7px 16px", textDecoration: "none" }} onMouseEnter={e => (e.currentTarget.style.background = "#F5EDE3")} onMouseLeave={e => (e.currentTarget.style.background = "transparent")}>TypeScript SDK</a>
                  <a href="https://www.npmjs.com/package/@e2a/cli" target="_blank" rel="noopener noreferrer" style={{ display: "block", fontSize: 13, color: "#7A6F63", padding: "7px 16px", textDecoration: "none" }} onMouseEnter={e => (e.currentTarget.style.background = "#F5EDE3")} onMouseLeave={e => (e.currentTarget.style.background = "transparent")}>CLI</a>
                </div>
              )}
            </div>
            <a
              href="https://github.com/Mnexa-AI/e2a"
              target="_blank"
              rel="noopener noreferrer"
              aria-label="View source on GitHub"
              className="nav-secondary-link"
              style={{ display: "inline-flex", alignItems: "center", color: "#7A6F63", padding: "6px 10px", borderRadius: 6, textDecoration: "none" }}
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                <path d="M12 .5C5.65.5.5 5.65.5 12c0 5.08 3.29 9.39 7.86 10.91.58.1.79-.25.79-.56 0-.27-.01-1.16-.02-2.1-3.2.7-3.87-1.36-3.87-1.36-.52-1.32-1.27-1.67-1.27-1.67-1.04-.71.08-.7.08-.7 1.15.08 1.76 1.18 1.76 1.18 1.02 1.76 2.69 1.25 3.34.96.1-.74.4-1.25.72-1.54-2.55-.29-5.24-1.28-5.24-5.69 0-1.26.45-2.29 1.18-3.1-.12-.29-.51-1.46.11-3.04 0 0 .96-.31 3.15 1.18a10.95 10.95 0 0 1 5.74 0c2.19-1.49 3.15-1.18 3.15-1.18.62 1.58.23 2.75.11 3.04.74.81 1.18 1.84 1.18 3.1 0 4.42-2.69 5.4-5.25 5.68.41.36.78 1.06.78 2.13 0 1.54-.01 2.78-.01 3.16 0 .31.21.67.8.56C20.21 21.39 23.5 17.08 23.5 12 23.5 5.65 18.35.5 12 .5z"/>
              </svg>
            </a>
            <div style={{ marginLeft: 8, paddingLeft: 18, borderLeft: "0.5px solid #E8E0D4" }}>
              {checkingAuth ? (
                <span style={{ fontSize: 12, color: "#A89A8A" }}>...</span>
              ) : user ? (
                <Link href="/dashboard" style={{ fontSize: 13, color: "#7A6F63", padding: "6px 10px", textDecoration: "none" }}>Go to Dashboard</Link>
              ) : (
                <a href="/api/auth/login" style={{ fontSize: 13, color: "#7A6F63", padding: "6px 10px", textDecoration: "none" }}>Sign in</a>
              )}
            </div>
            <Link href="/get-started" style={{ fontSize: 13, fontWeight: 500, background: "#1C1A17", color: "#FDFAF6", padding: "7px 14px", borderRadius: 7, textDecoration: "none", marginLeft: 6 }}>
              Start building →
            </Link>
          </div>
        </div>
      </nav>

      {/* Hero */}
      <section className="overflow-x-hidden hero-section" style={{ padding: "80px 32px 68px", textAlign: "center" }}>
        <p style={{ display: "inline-flex", alignItems: "center", gap: 7, fontSize: 12, fontWeight: 500, letterSpacing: "0.06em", textTransform: "uppercase", color: "#3A7A3A", marginBottom: 24 }}>
          <span style={{ position: "relative", display: "inline-flex", width: 7, height: 7 }}>
            <span style={{ position: "absolute", inset: -3, borderRadius: "50%", background: "#22c55e", opacity: 0.3, animation: "ping 2s ease-out infinite" }} />
            <span style={{ width: 7, height: 7, borderRadius: "50%", background: "#22c55e", display: "inline-block" }} />
          </span>
          Now in public beta
        </p>
        <h1 style={{ fontFamily: "'Instrument Serif', Georgia, serif", fontSize: "clamp(40px, 4vw, 56px)", lineHeight: 1.1, fontWeight: 400, color: "#1C1A17", maxWidth: 580, margin: "0 auto 22px", letterSpacing: "-0.01em", textAlign: "center" }}>
          Give your agent an email address.{" "}
          <em style={{ fontStyle: "italic", color: "#8B5E3C" }}>In under two minutes.</em>
        </h1>
        <p style={{ fontSize: 15, color: "#7A6F63", maxWidth: 400, margin: "0 auto 32px", lineHeight: 1.6, textAlign: "center" }}>
          Anyone can send an email, so your agent should have one. No mail server. No public URL. Free.
        </p>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "center", gap: 10, flexWrap: "wrap" }}>
          <Link href="/get-started" style={{ fontSize: 14, fontWeight: 500, background: "#1C1A17", color: "#FDFAF6", padding: "11px 22px", borderRadius: 8, textDecoration: "none", display: "inline-flex", alignItems: "center", gap: 6 }}>
            Get started free →
          </Link>
          <Link href="/api-docs" style={{ fontSize: 14, color: "#7A6F63", padding: "11px 18px", borderRadius: 8, border: "0.5px solid #D4C9BC", textDecoration: "none" }}>
            Read the docs
          </Link>
        </div>
      </section>

      <div style={{ height: "0.5px", background: "#E8E0D4" }} />

      {/* How it works */}
      <section id="how-it-works" style={{ padding: "56px 32px" }}>
        <div style={{ maxWidth: 960, margin: "0 auto" }}>
          <p style={{ fontSize: 11, fontWeight: 500, letterSpacing: "0.07em", textTransform: "uppercase", color: "#A89A8A", marginBottom: 10, textAlign: "center" }}>How it works</p>
          <h2 style={{ fontFamily: "'Instrument Serif', Georgia, serif", fontSize: 30, fontWeight: 400, color: "#1C1A17", textAlign: "center", marginBottom: 8 }}>Up and running in three steps</h2>
          <p style={{ fontSize: 14, color: "#7A6F63", textAlign: "center", maxWidth: 380, margin: "0 auto 40px", lineHeight: 1.6 }}>No mail server to configure. No custom inbox to build.</p>
          <div className="steps-grid" style={{ display: "grid", gridTemplateColumns: "repeat(3, 1fr)" }}>
            {[
              { num: "1", title: "Register your agent", desc: AGENTS_DOMAIN ? `Sign in and choose a shared address on ${AGENTS_DOMAIN}, or bring your own domain for more control.` : "Sign in and bring your own domain — register it once and verify the DNS records.", tag: "e2a agents register my-agent" },
              { num: "2", title: "Connect your agent", desc: "CLI, Python SDK, or the /e2a skill in Claude Code. Local agents use WebSocket, cloud agents use webhooks.", tag: "pip install e2a" },
              { num: "3", title: "Receive, reply, stay in thread", desc: "Emails arrive as signed events with sender identity, domain auth, and a conversation_id that tracks multi-turn threads across humans and other agents — no manual threading logic.", tag: "on_message(msg)" },
            ].map((step, i) => (
              <div key={i} style={{ padding: "28px 24px", borderLeft: i > 0 ? "0.5px solid #E8E0D4" : "none" }}>
                <div style={{ fontFamily: "'Instrument Serif', Georgia, serif", fontSize: 36, color: "#D4C9BC", fontWeight: 400, marginBottom: 12, lineHeight: 1 }}>{step.num}</div>
                <div style={{ fontSize: 14, fontWeight: 500, color: "#1C1A17", marginBottom: 6 }}>{step.title}</div>
                <div style={{ fontSize: 13, color: "#7A6F63", lineHeight: 1.6 }}>{step.desc}</div>
                <span style={{ display: "inline-flex", marginTop: 12, fontFamily: "'IBM Plex Mono', monospace", fontSize: 11, color: "#8B7B6B", background: "#F0EAE0", padding: "3px 8px", borderRadius: 5, border: "0.5px solid #E8E0D4" }}>{step.tag}</span>
              </div>
            ))}
          </div>
        </div>
      </section>

      <div style={{ height: "0.5px", background: "#E8E0D4" }} />

      {/* Quick start */}
      <section style={{ background: "#F5EFE6", padding: "48px 32px" }}>
        <div style={{ maxWidth: 960, margin: "0 auto" }}>
          <p style={{ fontSize: 11, fontWeight: 500, letterSpacing: "0.07em", textTransform: "uppercase", color: "#A89A8A", marginBottom: 10, textAlign: "center" }}>Quick start</p>
          <h2 style={{ fontFamily: "'Instrument Serif', Georgia, serif", fontSize: 30, fontWeight: 400, color: "#1C1A17", textAlign: "center", marginBottom: 8 }}>A few lines of code</h2>
          <p style={{ fontSize: 14, color: "#7A6F63", textAlign: "center", maxWidth: 380, margin: "0 auto 32px", lineHeight: 1.6 }}>Pick your interface. Everything else is already wired up.</p>
          <div style={{ display: "flex", gap: 4, marginBottom: 16, justifyContent: "center" }}>
            {(["cli", "claude", "python", "webhook"] as Tab[]).map((tab) => (
              <button key={tab} onClick={() => setActiveTab(tab)} style={{ fontSize: 12, fontWeight: 500, padding: "5px 12px", borderRadius: 6, cursor: "pointer", border: activeTab === tab ? "0.5px solid #E8E0D4" : "0.5px solid transparent", background: activeTab === tab ? "#FDFAF6" : "transparent", color: activeTab === tab ? "#1C1A17" : "#7A6F63" }}>
                {tab === "cli" ? "CLI" : tab === "claude" ? "Claude Code" : tab === "python" ? "Python" : "Webhook"}
              </button>
            ))}
          </div>
          <div className="code-block" style={{ background: "#1C1A17", borderRadius: 10, padding: "24px 28px", fontFamily: "'IBM Plex Mono', monospace", fontSize: 12.5, lineHeight: 1.8, overflowX: "auto" }}>
            {activeTab === "cli" && (
              <div>
                <div style={{ color: "#5A5248" }}># install</div>
                <div style={{ color: "#E8E0D4" }}><span style={{ color: "#C9956C" }}>npm install</span> -g @e2a/cli</div>
                <div style={{ color: "#2A2620" }}>&nbsp;</div>
                <div style={{ color: "#5A5248" }}># register your agent</div>
                <div style={{ color: "#E8E0D4" }}>e2a agents register my-agent</div>
                <div style={{ color: "#2A2620" }}>&nbsp;</div>
                <div style={{ color: "#5A5248" }}># listen for inbound email, forward to your local server</div>
                <div style={{ color: "#E8E0D4" }}>e2a listen <span style={{ color: "#8BAF6E" }}>--agent</span> my-agent@{exampleAgentDomain} <span style={{ color: "#8BAF6E" }}>--forward</span> http://localhost:3000</div>
              </div>
            )}
            {activeTab === "claude" && (
              <div>
                <div style={{ color: "#5A5248" }}># drop the e2a skill into your Claude Code project</div>
                <div style={{ color: "#E8E0D4" }}>mkdir -p .claude/skills/e2a</div>
                <div style={{ color: "#E8E0D4" }}>curl -o .claude/skills/e2a/SKILL.md \</div>
                <div style={{ color: "#E8E0D4" }}>&nbsp;&nbsp;https://raw.githubusercontent.com/Mnexa-AI/e2a-claude-code-skill/main/SKILL.md</div>
                <div style={{ color: "#2A2620" }}>&nbsp;</div>
                <div style={{ color: "#5A5248" }}># then tell Claude Code:</div>
                <div style={{ color: "#E8E0D4" }}>/e2a register my-agent</div>
                <div style={{ color: "#E8E0D4" }}>/e2a listen</div>
                <div style={{ color: "#2A2620" }}>&nbsp;</div>
                <div style={{ color: "#5A5248" }}># works with OpenClaw, Gemini CLI, any skill-aware agent</div>
              </div>
            )}
            {activeTab === "python" && (
              <div>
                <div style={{ color: "#E8E0D4" }}><span style={{ color: "#C9956C" }}>from</span> e2a.v1 <span style={{ color: "#C9956C" }}>import</span> AsyncE2AClient</div>
                <div style={{ color: "#2A2620" }}>&nbsp;</div>
                <div style={{ color: "#E8E0D4" }}>client = <span style={{ color: "#7EAACF" }}>AsyncE2AClient</span>(<span style={{ color: "#8BAF6E" }}>{'"your-api-key"'}</span>)</div>
                <div style={{ color: "#2A2620" }}>&nbsp;</div>
                <div style={{ color: "#5A5248" }}># sender identity verified; conversation_id threads multi-turn replies</div>
                <div style={{ color: "#E8E0D4" }}><span style={{ color: "#C9956C" }}>async for</span> msg <span style={{ color: "#C9956C" }}>in</span> client.<span style={{ color: "#7EAACF" }}>listen</span>(<span style={{ color: "#8BAF6E" }}>{`"my-agent@${exampleAgentDomain}"`}</span>):</div>
                <div style={{ color: "#E8E0D4" }}>&nbsp;&nbsp;&nbsp;&nbsp;<span style={{ color: "#7EAACF" }}>print</span>(msg.is_verified, msg.subject, msg.conversation_id)</div>
                <div style={{ color: "#E8E0D4" }}>&nbsp;&nbsp;&nbsp;&nbsp;<span style={{ color: "#C9956C" }}>await</span> msg.<span style={{ color: "#7EAACF" }}>reply</span>(<span style={{ color: "#8BAF6E" }}>{'"Got it, on it."'}</span>, conversation_id=msg.conversation_id)</div>
              </div>
            )}
            {activeTab === "webhook" && (
              <div>
                <div style={{ color: "#5A5248" }}># register a cloud agent with a webhook URL</div>
                <div style={{ color: "#E8E0D4" }}>curl -X POST {SITE_URL}/api/v1/agents \</div>
                <div style={{ color: "#E8E0D4" }}>&nbsp;&nbsp;-H <span style={{ color: "#8BAF6E" }}>{'"Authorization: Bearer $E2A_API_KEY"'}</span> \</div>
                <div style={{ color: "#E8E0D4" }}>&nbsp;&nbsp;-d <span style={{ color: "#8BAF6E" }}>{'"\'{"slug":"my-agent","webhook_url":"https://your-app.com/inbox"}\''}</span></div>
                <div style={{ color: "#2A2620" }}>&nbsp;</div>
                <div style={{ color: "#5A5248" }}># e2a POSTs verified payloads to your endpoint</div>
                <div style={{ color: "#5A5248" }}># includes HMAC signature, SPF/DKIM results,</div>
                <div style={{ color: "#5A5248" }}># and OAuth sender identity when available</div>
              </div>
            )}
          </div>
        </div>
      </section>

      <div style={{ height: "0.5px", background: "#E8E0D4" }} />

      {/* Human-in-the-loop */}
      <section id="hitl" style={{ padding: "64px 32px" }}>
        <div style={{ maxWidth: 960, margin: "0 auto" }}>
          <div className="hitl-grid" style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 48, alignItems: "center" }}>
            <div>
              <p style={{ fontSize: 11, fontWeight: 500, letterSpacing: "0.07em", textTransform: "uppercase", color: "#A89A8A", marginBottom: 10 }}>New · Human-in-the-loop</p>
              <h2 style={{ fontFamily: "'Instrument Serif', Georgia, serif", fontSize: 30, fontWeight: 400, color: "#1C1A17", marginBottom: 14, lineHeight: 1.2 }}>
                Approve before your agent hits send.
              </h2>
              <p style={{ fontSize: 14, color: "#7A6F63", lineHeight: 1.65, marginBottom: 14 }}>
                Flip one switch and outbound messages pause for your review instead of going straight out. You get an email notification — click to see recipients, subject, and body on a secure confirmation page. Approve as-is, edit, or reject.
              </p>
              <p style={{ fontSize: 13, color: "#7A6F63", lineHeight: 1.65, marginBottom: 20 }}>
                Per-agent, opt-in, off by default. Configurable TTL with auto-approve or auto-reject on expiry. Reviewable from the dashboard, CLI, or one-click magic links in your inbox.
              </p>
              <div style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
                <Link href="/blog/human-in-the-loop-for-agent-email" style={{ fontSize: 13, fontWeight: 500, background: "#1C1A17", color: "#FDFAF6", padding: "9px 16px", borderRadius: 7, textDecoration: "none" }}>
                  Read the announcement →
                </Link>
                <Link href="/dashboard" style={{ fontSize: 13, color: "#7A6F63", padding: "9px 14px", borderRadius: 7, border: "0.5px solid #D4C9BC", textDecoration: "none" }}>
                  Enable on an agent
                </Link>
              </div>
            </div>
            <div className="code-block" style={{ background: "#1C1A17", borderRadius: 10, padding: "22px 24px", fontFamily: "'IBM Plex Mono', monospace", fontSize: 12.5, lineHeight: 1.8, overflowX: "auto" }}>
              <div style={{ color: "#5A5248" }}># hold outbound from this agent for review</div>
              <div style={{ color: "#E8E0D4" }}>e2a agents update my-agent <span style={{ color: "#8BAF6E" }}>--hitl</span> \</div>
              <div style={{ color: "#E8E0D4" }}>&nbsp;&nbsp;<span style={{ color: "#8BAF6E" }}>--hitl-ttl</span> 3600 <span style={{ color: "#8BAF6E" }}>--hitl-expiration-action</span> reject</div>
              <div style={{ color: "#2A2620" }}>&nbsp;</div>
              <div style={{ color: "#5A5248" }}># review held messages from the terminal</div>
              <div style={{ color: "#E8E0D4" }}>e2a pending list</div>
              <div style={{ color: "#E8E0D4" }}>e2a pending approve msg_abc123 <span style={{ color: "#8BAF6E" }}>--edit</span></div>
              <div style={{ color: "#E8E0D4" }}>e2a pending reject msg_def456</div>
              <div style={{ color: "#2A2620" }}>&nbsp;</div>
              <div style={{ color: "#5A5248" }}># or do it from the dashboard, or straight from the</div>
              <div style={{ color: "#5A5248" }}># notification email&apos;s approve / reject buttons</div>
            </div>
          </div>
        </div>
      </section>

      <div style={{ height: "0.5px", background: "#E8E0D4" }} />

      {/* Use cases */}
      <section id="use-cases" style={{ background: "#F5EFE6", padding: "56px 32px 0" }}>
        <div style={{ maxWidth: 960, margin: "0 auto" }}>
          <p style={{ fontSize: 11, fontWeight: 500, letterSpacing: "0.07em", textTransform: "uppercase", color: "#A89A8A", marginBottom: 10, textAlign: "center" }}>Use cases</p>
          <h2 style={{ fontFamily: "'Instrument Serif', Georgia, serif", fontSize: 30, fontWeight: 400, color: "#1C1A17", textAlign: "center", marginBottom: 8 }}>What you can build</h2>
          <p style={{ fontSize: 14, color: "#7A6F63", textAlign: "center", maxWidth: 380, margin: "0 auto 40px", lineHeight: 1.6 }}>If it can receive email and take action, e2a can power it.</p>
        </div>
        <div className="use-cases-grid" style={{ maxWidth: 960, margin: "0 auto", display: "grid", gridTemplateColumns: "repeat(3, 1fr)" }}>
          {[
            {
              title: "Support and intake",
              desc: "Triage inbound requests, answer common questions, and hand off to humans without changing how customers reach you.",
              icon: <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />,
            },
            {
              title: "Scheduling and admin",
              desc: "Handle meeting coordination, reminders, summaries, and follow-ups in the channel most people already live in.",
              icon: <><rect x="3" y="4" width="18" height="18" rx="2" ry="2" /><line x1="16" y1="2" x2="16" y2="6" /><line x1="8" y1="2" x2="8" y2="6" /><line x1="3" y1="10" x2="21" y2="10" /></>,
            },
            {
              title: "Sales and follow-through",
              desc: "Qualify leads, reply to outreach, and keep conversations moving with a verified agent identity and real email threads.",
              icon: <><line x1="12" y1="1" x2="12" y2="23" /><path d="M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6" /></>,
            },
            {
              title: "OTP and verification flows",
              desc: "Give your agent an address that receives verification codes, confirmation emails, and magic links, then acts on them automatically.",
              icon: <><rect x="2" y="3" width="20" height="14" rx="2" /><path d="M8 21h8M12 17v4" /><line x1="7" y1="8" x2="7" y2="8" /><line x1="11" y1="8" x2="17" y2="8" /><line x1="7" y1="12" x2="7" y2="12" /><line x1="11" y1="12" x2="17" y2="12" /></>,
            },
            {
              title: "Voice agents",
              desc: "After a call ends, your voice agent sends a follow-up, receives a reply, and keeps the thread going without human involvement.",
              icon: <><path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z" /><path d="M19 10v2a7 7 0 0 1-14 0v-2" /><line x1="12" y1="19" x2="12" y2="23" /><line x1="8" y1="23" x2="16" y2="23" /></>,
            },
            {
              title: "Procurement",
              desc: "Coordinate with vendors, chase purchase orders, and manage supplier threads with partners who still run on email.",
              icon: <><path d="M6 2L3 6v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V6l-3-4z" /><line x1="3" y1="6" x2="21" y2="6" /><path d="M16 10a4 4 0 0 1-8 0" /></>,
            },
          ].map((uc, i) => {
            const isLastRow = i >= 3;
            const col = i % 3;
            const borderRight = col < 2 ? "0.5px solid #E8E0D4" : "none";
            const borderBottom = isLastRow ? "none" : "0.5px solid #E8E0D4";
            return (
              <div key={i} style={{ padding: 28, borderRight, borderBottom }}>
                <div style={{ width: 30, height: 30, borderRadius: 7, background: "#F0EAE0", display: "flex", alignItems: "center", justifyContent: "center", marginBottom: 14 }}>
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="#8B5E3C" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">{uc.icon}</svg>
                </div>
                <div style={{ fontSize: 13, fontWeight: 500, color: "#1C1A17", marginBottom: 5 }}>{uc.title}</div>
                <div style={{ fontSize: 12, color: "#7A6F63", lineHeight: 1.65 }}>{uc.desc}</div>
              </div>
            );
          })}
        </div>
        <div style={{ height: 48 }} />
      </section>

      <div style={{ height: "0.5px", background: "#E8E0D4" }} />

      {/* CTA */}
      <section style={{ padding: "64px 32px", textAlign: "center" }}>
        <h2 style={{ fontFamily: "'Instrument Serif', Georgia, serif", fontSize: 32, fontWeight: 400, color: "#1C1A17", marginBottom: 10 }}>Your agent&apos;s inbox is one sign-in away.</h2>
        <p style={{ fontSize: 14, color: "#7A6F63", margin: "0 auto 28px", maxWidth: 320, lineHeight: 1.6 }}>Free during beta. No credit card. Up and running in under two minutes.</p>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "center", gap: 10, flexWrap: "wrap" }}>
          <Link href="/get-started" style={{ fontSize: 14, fontWeight: 500, background: "#1C1A17", color: "#FDFAF6", padding: "11px 22px", borderRadius: 8, textDecoration: "none", display: "inline-flex", alignItems: "center", gap: 6 }}>
            Get started free →
          </Link>
          <Link href="/api-docs" style={{ fontSize: 14, color: "#7A6F63", padding: "11px 18px", borderRadius: 8, border: "0.5px solid #D4C9BC", textDecoration: "none" }}>
            Read the docs
          </Link>
        </div>
        <p style={{ marginTop: 16, fontSize: 12, color: "#A89A8A" }}>
          Have feedback?{" "}
          <Link href="/feedback" style={{ color: "#8B5E3C", textDecoration: "none" }}>We&apos;d love to hear from you.</Link>
        </p>
      </section>

      {/* Footer */}
      <footer className="site-footer" style={{ padding: "20px 32px", borderTop: "0.5px solid #E8E0D4", display: "flex", alignItems: "center", justifyContent: "space-between" }}>
        <span style={{ fontFamily: "'IBM Plex Mono', monospace", fontWeight: 600, fontSize: 13, color: "#1C1A17" }}>e2a</span>
        <div className="footer-links" style={{ display: "flex", gap: 20 }}>
          <a href="https://github.com/Mnexa-AI/e2a" target="_blank" rel="noopener noreferrer" style={{ fontSize: 12, color: "#A89A8A", textDecoration: "none" }}>GitHub</a>
          <Link href="/api-docs" style={{ fontSize: 12, color: "#A89A8A", textDecoration: "none" }}>API Docs</Link>
          <Link href="/blog" style={{ fontSize: 12, color: "#A89A8A", textDecoration: "none" }}>Blog</Link>
          <a href="https://pypi.org/project/e2a/" target="_blank" rel="noopener noreferrer" style={{ fontSize: 12, color: "#A89A8A", textDecoration: "none" }}>Python SDK</a>
          <a href="https://www.npmjs.com/package/@e2a/sdk" target="_blank" rel="noopener noreferrer" style={{ fontSize: 12, color: "#A89A8A", textDecoration: "none" }}>TypeScript SDK</a>
          <a href="https://www.npmjs.com/package/@e2a/cli" target="_blank" rel="noopener noreferrer" style={{ fontSize: 12, color: "#A89A8A", textDecoration: "none" }}>CLI</a>
          <a href="https://github.com/Mnexa-AI/e2a-claude-code-skill" target="_blank" rel="noopener noreferrer" style={{ fontSize: 12, color: "#A89A8A", textDecoration: "none" }}>Claude Skill</a>
          <Link href="/feedback" style={{ fontSize: 12, color: "#A89A8A", textDecoration: "none" }}>Feedback</Link>
        </div>
        <span style={{ fontSize: 12, color: "#A89A8A" }}>Apache 2.0</span>
      </footer>

      <style>{`
        @import url('https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;600&family=Instrument+Serif:ital@0;1&display=swap');
        @keyframes ping {
          0%, 100% { transform: scale(1); opacity: 0.3; }
          50% { transform: scale(1.8); opacity: 0; }
        }
        @media (max-width: 640px) {
          .nav-secondary-link { display: none; }

          .hero-section { padding: 52px 24px 44px !important; }

          .steps-grid { grid-template-columns: 1fr !important; }
          .steps-grid > div { border-left: none !important; border-bottom: 0.5px solid #E8E0D4; padding: 24px 0; }
          .steps-grid > div:last-child { border-bottom: none; }

          .code-block { padding: 20px 16px !important; }

          .hitl-grid { grid-template-columns: 1fr !important; gap: 24px !important; }

          .use-cases-grid { grid-template-columns: 1fr !important; }
          .use-cases-grid > div { border-right: none !important; border-bottom: 0.5px solid #E8E0D4 !important; }
          .use-cases-grid > div:last-child { border-bottom: none !important; }

          .site-footer { flex-direction: column !important; align-items: center !important; gap: 16px !important; padding: 24px 20px !important; }
          .footer-links { flex-wrap: wrap; justify-content: center; gap: 12px !important; }
        }
      `}</style>
    </div>
  );
}
