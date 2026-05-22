// landing.jsx — e2a landing, restyled with Loft tokens
// Cream + ember + ink, with Loft's eyebrow / code-block / chip patterns.
// Instrument Serif italic stays as e2a's brand DNA for editorial headlines.

function L_Eyebrow({ children, color }) {
  return (
    <span style={{
      fontFamily: "var(--f-mono)", fontSize: 11, fontWeight: 600,
      letterSpacing: '0.08em', textTransform: 'uppercase',
      color: color || 'var(--accent-strong)',
    }}>{children}</span>
  );
}

function L_BetaPill() {
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 8,
      fontFamily: "var(--f-mono)", fontSize: 11, fontWeight: 600,
      letterSpacing: '0.08em', textTransform: 'uppercase',
      color: 'var(--success)',
    }}>
      <span style={{ position: 'relative', display: 'inline-flex', width: 8, height: 8 }}>
        <span style={{
          position: 'absolute', inset: -4, borderRadius: '50%',
          background: 'var(--success)', opacity: 0.25,
          animation: 'lr-ping 1.8s ease-out infinite',
        }} />
        <span style={{ width: 8, height: 8, borderRadius: '50%', background: 'var(--success)' }} />
      </span>
      Now in public beta · free
    </span>
  );
}

function L_Wordmark({ size = 16 }) {
  return (
    <span style={{
      fontFamily: "var(--f-mono)", fontWeight: 700, fontSize: size,
      letterSpacing: '-0.02em', color: 'var(--fg)',
    }}>e2a</span>
  );
}

function L_BtnPrimary({ children, kbd }) {
  return (
    <a href="#" style={{
      display: 'inline-flex', alignItems: 'center', gap: 8,
      background: 'var(--accent-fill)', color: '#fff',
      padding: '11px 18px', borderRadius: 'var(--r-md)',
      fontFamily: 'var(--f-ui)', fontSize: 14, fontWeight: 500,
      textDecoration: 'none', letterSpacing: '-0.005em',
    }}>
      {children}
      <span style={{ fontFamily: 'var(--f-mono)', fontSize: 12, opacity: 0.9 }}>→</span>
    </a>
  );
}

function L_BtnSecondary({ children }) {
  return (
    <a href="#" style={{
      display: 'inline-flex', alignItems: 'center', gap: 8,
      background: 'var(--bg-panel)', color: 'var(--fg)',
      padding: '10px 17px', borderRadius: 'var(--r-md)',
      fontFamily: 'var(--f-ui)', fontSize: 14, fontWeight: 500,
      textDecoration: 'none',
      border: '1px solid var(--border)',
    }}>{children}</a>
  );
}

// Ink code block — the Loft "machine speaking" motif
function L_Ink({ children, title, prompt, width = '100%', noChrome = false }) {
  return (
    <div style={{
      background: 'var(--ink)', borderRadius: 'var(--r-lg)',
      border: '1px solid var(--ink-border)',
      padding: noChrome ? '14px 18px' : 0,
      width,
      overflow: 'hidden',
      boxShadow: '0 1px 0 rgba(255,255,255,.03) inset, 0 6px 24px rgba(20,15,8,.08)',
    }}>
      {!noChrome && (
        <div style={{
          display: 'flex', alignItems: 'center', gap: 10,
          padding: '10px 16px', borderBottom: '1px solid var(--ink-border)',
          background: 'var(--ink-elev)',
        }}>
          <span style={{ width: 9, height: 9, borderRadius: '50%', background: '#3a342e' }} />
          <span style={{ width: 9, height: 9, borderRadius: '50%', background: '#3a342e' }} />
          <span style={{ width: 9, height: 9, borderRadius: '50%', background: '#3a342e' }} />
          <span style={{
            marginLeft: 8, fontFamily: 'var(--f-mono)', fontSize: 11,
            color: 'var(--ink-fg-muted)', letterSpacing: '0.04em',
          }}>{title || '~/my-agent'}</span>
        </div>
      )}
      <div style={{ padding: noChrome ? 0 : '16px 18px', fontFamily: 'var(--f-mono)', fontSize: 13, lineHeight: 1.75, color: 'var(--ink-fg)' }}>
        {children}
      </div>
    </div>
  );
}

const C_PROMPT = { color: 'var(--machine)', userSelect: 'none', marginRight: 8 };
const C_COMMENT = { color: 'var(--ink-fg-muted)' };
const C_STRING = { color: 'var(--spectral)' };
const C_FLAG = { color: 'var(--machine)' };

// ────────────────────────────────────────────────────────
// The landing artboard itself — 1280px wide, free height
// ────────────────────────────────────────────────────────

function LoftLanding() {
  return (
    <div style={{
      width: 1280, background: 'var(--bg)', color: 'var(--fg)',
      fontFamily: 'var(--f-ui)',
    }}>
      <Nav />
      <Hero />
      <RuleEm />
      <HowItWorks />
      <QuickStart />
      <Hitl />
      <UseCases />
      <Cta />
      <Footer />
    </div>
  );
}

function Nav() {
  return (
    <nav style={{
      position: 'sticky', top: 0, zIndex: 5,
      background: 'rgba(250,247,242,.85)',
      backdropFilter: 'saturate(120%) blur(8px)',
      WebkitBackdropFilter: 'saturate(120%) blur(8px)',
      borderBottom: '1px solid var(--border)',
    }}>
      <div style={{
        maxWidth: 1080, margin: '0 auto',
        padding: '14px 32px',
        display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 24,
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <L_Wordmark size={17} />
          <span style={{
            fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)',
            letterSpacing: '0.04em',
          }}>v0.4 · beta</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 4, fontFamily: 'var(--f-ui)', fontSize: 13 }}>
          {[
            ['How it works', '#'],
            ['Human-in-the-loop', '#'],
            ['Use cases', '#'],
            ['Blog', '#'],
            ['Docs', '#'],
          ].map(([label, href]) => (
            <a key={label} href={href} style={{
              padding: '7px 12px', borderRadius: 'var(--r-md)',
              color: 'var(--fg-muted)', textDecoration: 'none',
            }}>{label}</a>
          ))}
          <span style={{ width: 1, height: 18, background: 'var(--border)', margin: '0 6px' }} />
          <a href="#" style={{ padding: '7px 12px', color: 'var(--fg-muted)', textDecoration: 'none' }}>Sign in</a>
          <a href="#" style={{
            marginLeft: 4, display: 'inline-flex', alignItems: 'center', gap: 6,
            padding: '7px 14px', borderRadius: 'var(--r-md)',
            background: 'var(--fg)', color: 'var(--bg)',
            fontWeight: 500, textDecoration: 'none',
          }}>
            Start building
            <span style={{ fontFamily: 'var(--f-mono)' }}>→</span>
          </a>
        </div>
      </div>
    </nav>
  );
}

function Hero() {
  return (
    <section style={{ position: 'relative', overflow: 'hidden' }}>
      {/* Loft's signature: subtle radial glows */}
      <div style={{
        position: 'absolute', top: -160, right: -180, width: 560, height: 560,
        background: 'radial-gradient(circle at center, var(--accent-soft) 0%, rgba(0,0,0,0) 60%)',
        pointerEvents: 'none',
      }} />
      <div style={{
        position: 'absolute', bottom: -200, left: -200, width: 520, height: 520,
        background: 'radial-gradient(circle at center, rgba(111,221,229,.18) 0%, rgba(0,0,0,0) 60%)',
        pointerEvents: 'none',
      }} />

      <div style={{
        maxWidth: 1080, margin: '0 auto',
        padding: '88px 32px 64px',
        textAlign: 'center', position: 'relative',
      }}>
        <div style={{ marginBottom: 28 }}>
          <L_BetaPill />
        </div>
        <h1 style={{
          fontFamily: "'Instrument Serif', Georgia, serif",
          fontSize: 72, lineHeight: 1.04, fontWeight: 400,
          letterSpacing: '-0.012em',
          maxWidth: 880, margin: '0 auto 22px',
          color: 'var(--fg)',
        }}>
          Give your agent an email address.{' '}
          <em style={{ fontStyle: 'italic', color: 'var(--accent-strong)' }}>
            In under two minutes.
          </em>
        </h1>
        <p style={{
          fontSize: 17, color: 'var(--fg-muted)',
          maxWidth: 540, margin: '0 auto 36px',
          lineHeight: 1.55,
        }}>
          Anyone can send an email — so your agent should have one. Signed identity,
          conversation threading, and a human-in-the-loop gate. No mail server. No public URL.
        </p>
        <div style={{ display: 'inline-flex', gap: 10, alignItems: 'center', flexWrap: 'wrap', justifyContent: 'center' }}>
          <L_BtnPrimary>Get started free</L_BtnPrimary>
          <L_BtnSecondary>Read the docs</L_BtnSecondary>
        </div>

        <div style={{
          marginTop: 44,
          display: 'flex', justifyContent: 'center', gap: 26,
          fontFamily: 'var(--f-mono)', fontSize: 12, color: 'var(--fg-subtle)',
          letterSpacing: '0.02em',
        }}>
          <span><span style={{ color: 'var(--fg-muted)' }}>$</span>&nbsp;npm i -g @e2a/cli</span>
          <span style={{ color: 'var(--border-strong)' }}>·</span>
          <span><span style={{ color: 'var(--fg-muted)' }}>$</span>&nbsp;pip install e2a</span>
          <span style={{ color: 'var(--border-strong)' }}>·</span>
          <span>Apache 2.0</span>
        </div>
      </div>
    </section>
  );
}

function RuleEm() {
  return <div style={{ height: 1, background: 'var(--border)' }} />;
}

function HowItWorks() {
  const steps = [
    {
      n: '01',
      title: 'Register your agent',
      desc: "Sign in, pick a slug, and you've got my-agent@agents.e2a.dev. Or BYO domain — verify by DNS TXT.",
      tag: 'e2a agents register my-agent',
    },
    {
      n: '02',
      title: 'Connect',
      desc: 'CLI, Python or TypeScript SDK, or the Claude Code skill. Local agents use WebSocket, cloud agents use webhooks — same delivery contract.',
      tag: 'pip install e2a',
    },
    {
      n: '03',
      title: 'Receive, reply, stay in thread',
      desc: 'Inbound mail arrives signed: sender identity, SPF/DKIM verdict, and a conversation_id that survives the email ↔ structured-data boundary.',
      tag: 'on_message(msg)',
    },
  ];
  return (
    <section style={{ padding: '64px 32px' }}>
      <div style={{ maxWidth: 1080, margin: '0 auto' }}>
        <div style={{ textAlign: 'center', marginBottom: 44 }}>
          <L_Eyebrow>01 · How it works</L_Eyebrow>
          <h2 style={{
            fontFamily: "'Instrument Serif', Georgia, serif",
            fontSize: 38, fontWeight: 400, color: 'var(--fg)',
            margin: '12px auto 8px', letterSpacing: '-0.01em',
          }}>Up and running in three steps.</h2>
          <p style={{ fontSize: 14, color: 'var(--fg-muted)', maxWidth: 460, margin: '0 auto' }}>
            No mail server to configure. No custom inbox to build.
          </p>
        </div>
        <div style={{
          display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)',
          background: 'var(--bg-panel)', border: '1px solid var(--border)',
          borderRadius: 'var(--r-lg)', overflow: 'hidden',
        }}>
          {steps.map((s, i) => (
            <div key={s.n} style={{
              padding: '32px 28px',
              borderLeft: i > 0 ? '1px solid var(--border)' : 'none',
            }}>
              <div style={{
                fontFamily: 'var(--f-mono)', fontSize: 11, fontWeight: 600,
                color: 'var(--accent-strong)', letterSpacing: '0.08em',
                marginBottom: 18,
              }}>STEP {s.n}</div>
              <div style={{
                fontFamily: 'var(--f-ui)', fontSize: 18, fontWeight: 600,
                color: 'var(--fg)', marginBottom: 8, letterSpacing: '-0.01em',
              }}>{s.title}</div>
              <div style={{ fontSize: 13, color: 'var(--fg-muted)', lineHeight: 1.6, marginBottom: 18 }}>
                {s.desc}
              </div>
              <span style={{
                display: 'inline-flex',
                fontFamily: 'var(--f-mono)', fontSize: 12,
                color: 'var(--fg)',
                background: 'var(--bg-elev)',
                padding: '4px 10px', borderRadius: 'var(--r-sm)',
                border: '1px solid var(--border-sub)',
              }}>{s.tag}</span>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function QuickStart() {
  const tabs = ['CLI', 'Python', 'TypeScript', 'Webhook'];
  return (
    <section style={{ background: 'var(--bg-elev)', padding: '64px 32px', borderTop: '1px solid var(--border)', borderBottom: '1px solid var(--border)' }}>
      <div style={{ maxWidth: 1080, margin: '0 auto' }}>
        <div style={{ textAlign: 'center', marginBottom: 28 }}>
          <L_Eyebrow>02 · Quick start</L_Eyebrow>
          <h2 style={{
            fontFamily: "'Instrument Serif', Georgia, serif",
            fontSize: 38, fontWeight: 400, color: 'var(--fg)',
            margin: '12px auto 8px', letterSpacing: '-0.01em',
          }}>A few lines of code.</h2>
          <p style={{ fontSize: 14, color: 'var(--fg-muted)', maxWidth: 460, margin: '0 auto' }}>
            Pick your interface. Everything else is already wired up.
          </p>
        </div>

        {/* Loft segmented control */}
        <div style={{ display: 'flex', justifyContent: 'center', marginBottom: 18 }}>
          <div style={{
            display: 'inline-flex', gap: 2, padding: 3,
            background: 'var(--bg-panel)', border: '1px solid var(--border)',
            borderRadius: 'var(--r-md)',
          }}>
            {tabs.map((t, i) => (
              <button key={t} style={{
                fontFamily: 'var(--f-ui)', fontSize: 12, fontWeight: 500,
                padding: '6px 14px', borderRadius: 'var(--r-sm)',
                border: 'none', cursor: 'pointer',
                background: i === 1 ? 'var(--fg)' : 'transparent',
                color: i === 1 ? 'var(--bg)' : 'var(--fg-muted)',
              }}>{t}</button>
            ))}
          </div>
        </div>

        <L_Ink title="agent.py · python">
          <div><span style={C_COMMENT}># register · listen · reply — three lines, signed identity included</span></div>
          <div><span style={{ color: '#C8A8FF' }}>from</span> e2a.v1 <span style={{ color: '#C8A8FF' }}>import</span> AsyncE2AClient</div>
          <div>&nbsp;</div>
          <div>client = <span style={{ color: 'var(--spectral)' }}>AsyncE2AClient</span>(api_key=<span style={C_STRING}>"e2a_…"</span>)</div>
          <div>&nbsp;</div>
          <div><span style={{ color: '#C8A8FF' }}>async for</span> msg <span style={{ color: '#C8A8FF' }}>in</span> client.<span style={{ color: 'var(--spectral)' }}>listen</span>(<span style={C_STRING}>"my-agent@agents.e2a.dev"</span>):</div>
          <div>{'\u00A0\u00A0'}<span style={{ color: 'var(--spectral)' }}>print</span>(msg.is_verified, msg.subject, msg.conversation_id)</div>
          <div>{'\u00A0\u00A0'}<span style={{ color: '#C8A8FF' }}>await</span> msg.<span style={{ color: 'var(--spectral)' }}>reply</span>(<span style={C_STRING}>"Got it, on it."</span>, conversation_id=msg.conversation_id)</div>
          <div>&nbsp;</div>
          <div><span style={C_COMMENT}># 200 OK · sender verified · sha256:9f4c…3a1b</span></div>
        </L_Ink>
      </div>
    </section>
  );
}

function Hitl() {
  return (
    <section style={{ padding: '72px 32px' }}>
      <div style={{
        maxWidth: 1080, margin: '0 auto',
        display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 56, alignItems: 'center',
      }}>
        <div>
          <L_Eyebrow>03 · Human-in-the-loop</L_Eyebrow>
          <h2 style={{
            fontFamily: "'Instrument Serif', Georgia, serif",
            fontSize: 44, fontWeight: 400, color: 'var(--fg)',
            margin: '14px 0 16px', lineHeight: 1.1, letterSpacing: '-0.01em',
          }}>
            Approve <em style={{ color: 'var(--accent-strong)' }}>before</em> your agent hits send.
          </h2>
          <p style={{ fontSize: 15, color: 'var(--fg-muted)', lineHeight: 1.6, marginBottom: 14 }}>
            Flip one switch and outbound messages pause for your review instead of going
            straight out. You get a notification — click to see recipients, subject, and
            body on a secure confirmation page. Approve, edit, or reject.
          </p>
          <p style={{ fontSize: 13, color: 'var(--fg-muted)', lineHeight: 1.65, marginBottom: 22 }}>
            Per-agent, opt-in, off by default. Configurable TTL with auto-approve or
            auto-reject on expiry. Reviewable from the dashboard, CLI, or one-click magic
            links in your inbox.
          </p>
          <div style={{ display: 'flex', gap: 10 }}>
            <L_BtnPrimary>Read the announcement</L_BtnPrimary>
            <L_BtnSecondary>Enable on an agent</L_BtnSecondary>
          </div>
        </div>

        <L_Ink title="hitl.sh · cli">
          <div><span style={C_COMMENT}># hold outbound for review</span></div>
          <div><span style={C_PROMPT}>$</span>e2a agents update my-agent <span style={C_FLAG}>--hitl</span> \</div>
          <div>{'\u00A0\u00A0'}<span style={C_FLAG}>--hitl-ttl</span> 3600 <span style={C_FLAG}>--hitl-expiration-action</span> reject</div>
          <div>&nbsp;</div>
          <div><span style={C_COMMENT}># review held messages from your terminal</span></div>
          <div><span style={C_PROMPT}>$</span>e2a pending list</div>
          <div style={{ color: 'var(--ink-fg-muted)' }}>{'\u00A0\u00A0'}msg_abc123  customer@acme.io   <span style={{ color: 'var(--warn)' }}>in 47m</span></div>
          <div style={{ color: 'var(--ink-fg-muted)' }}>{'\u00A0\u00A0'}msg_def456  legal@stripe.com   <span style={{ color: 'var(--warn)' }}>in 2h 12m</span></div>
          <div>&nbsp;</div>
          <div><span style={C_PROMPT}>$</span>e2a pending approve msg_abc123 <span style={C_FLAG}>--edit</span></div>
          <div style={{ color: 'var(--machine)' }}>{'\u00A0\u00A0'}→ approved · delivering now</div>
        </L_Ink>
      </div>
    </section>
  );
}

function UseCases() {
  const items = [
    { eye: 'support', title: 'Support & intake', desc: 'Triage inbound requests, answer common questions, and hand off to humans without changing how customers reach you.' },
    { eye: 'admin', title: 'Scheduling & admin', desc: 'Coordinate meetings, send reminders, and follow up where most people already live — their inbox.' },
    { eye: 'sales', title: 'Sales & follow-through', desc: 'Qualify leads, reply to outreach, and keep conversations moving with a verified agent identity.' },
    { eye: 'auth', title: 'OTP & verification flows', desc: 'Receive verification codes, confirmation emails, and magic links — then act on them automatically.' },
    { eye: 'voice', title: 'Voice agents', desc: 'After a call ends, your voice agent sends a follow-up, receives a reply, and keeps the thread going.' },
    { eye: 'procurement', title: 'Procurement', desc: 'Coordinate with vendors, chase POs, and manage supplier threads with partners who still run on email.' },
  ];
  return (
    <section style={{ background: 'var(--bg-elev)', padding: '64px 32px', borderTop: '1px solid var(--border)', borderBottom: '1px solid var(--border)' }}>
      <div style={{ maxWidth: 1080, margin: '0 auto' }}>
        <div style={{ textAlign: 'center', marginBottom: 40 }}>
          <L_Eyebrow>04 · Use cases</L_Eyebrow>
          <h2 style={{
            fontFamily: "'Instrument Serif', Georgia, serif",
            fontSize: 38, fontWeight: 400, color: 'var(--fg)',
            margin: '12px auto 8px', letterSpacing: '-0.01em',
          }}>What you can build.</h2>
          <p style={{ fontSize: 14, color: 'var(--fg-muted)', maxWidth: 460, margin: '0 auto' }}>
            If it can receive email and take action, e2a can power it.
          </p>
        </div>

        <div style={{
          display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 0,
          background: 'var(--bg-panel)', border: '1px solid var(--border)',
          borderRadius: 'var(--r-lg)', overflow: 'hidden',
        }}>
          {items.map((u, i) => (
            <div key={u.title} style={{
              padding: '24px 24px 26px',
              borderRight: (i % 3) < 2 ? '1px solid var(--border)' : 'none',
              borderBottom: i < 3 ? '1px solid var(--border)' : 'none',
            }}>
              <div style={{
                fontFamily: 'var(--f-mono)', fontSize: 11, fontWeight: 600,
                color: 'var(--accent-strong)', letterSpacing: '0.08em',
                marginBottom: 10, textTransform: 'uppercase',
              }}>{u.eye}</div>
              <div style={{ fontFamily: 'var(--f-ui)', fontSize: 15, fontWeight: 600, color: 'var(--fg)', marginBottom: 6 }}>
                {u.title}
              </div>
              <div style={{ fontSize: 13, color: 'var(--fg-muted)', lineHeight: 1.6 }}>
                {u.desc}
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function Cta() {
  return (
    <section style={{ padding: '72px 32px', textAlign: 'center' }}>
      <div style={{ maxWidth: 720, margin: '0 auto' }}>
        <h2 style={{
          fontFamily: "'Instrument Serif', Georgia, serif",
          fontSize: 46, fontWeight: 400, color: 'var(--fg)',
          letterSpacing: '-0.012em', lineHeight: 1.05, marginBottom: 14,
        }}>
          Your agent's inbox is{' '}
          <em style={{ color: 'var(--accent-strong)' }}>one sign-in</em> away.
        </h2>
        <p style={{ fontSize: 15, color: 'var(--fg-muted)', lineHeight: 1.55, marginBottom: 28 }}>
          Free during beta. No credit card. Up and running in under two minutes.
        </p>
        <div style={{ display: 'inline-flex', gap: 10, alignItems: 'center', flexWrap: 'wrap', justifyContent: 'center' }}>
          <L_BtnPrimary>Get started free</L_BtnPrimary>
          <L_BtnSecondary>Read the docs</L_BtnSecondary>
        </div>
      </div>
    </section>
  );
}

function Footer() {
  return (
    <footer style={{
      borderTop: '1px solid var(--border)',
      padding: '24px 32px',
    }}>
      <div style={{
        maxWidth: 1080, margin: '0 auto',
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        gap: 32, fontSize: 12, color: 'var(--fg-subtle)', fontFamily: 'var(--f-ui)',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          <L_Wordmark size={15} />
          <span style={{ fontFamily: 'var(--f-mono)', color: 'var(--fg-muted)' }}>· Email for agents</span>
        </div>
        <div style={{ display: 'flex', gap: 18 }}>
          {['GitHub', 'API Docs', 'Blog', 'Python SDK', 'TypeScript SDK', 'CLI', 'Claude Skill', 'Feedback'].map(x =>
            <a key={x} href="#" style={{ color: 'var(--fg-muted)', textDecoration: 'none' }}>{x}</a>
          )}
        </div>
        <span style={{ fontFamily: 'var(--f-mono)' }}>Apache 2.0</span>
      </div>
    </footer>
  );
}

window.LoftLanding = LoftLanding;
