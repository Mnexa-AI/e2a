// pending.jsx — HITL pending-review detail screen, Loft-styled.
// Shows the LIST of pending messages on the left and the SELECTED message
// with full diff/approval controls on the right.  1280×920 artboard.

function P_Sidebar() {
  // Same as dashboard sidebar but with Pending active
  return window.D_SidebarPending ? window.D_SidebarPending() : null;
}

// Tiny avatar helper
function P_Av({ ch, h }) {
  return (
    <div style={{
      width: 26, height: 26, borderRadius: 5,
      background: `var(--av-${h})`, color: '#fff',
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      fontSize: 11, fontWeight: 700, flexShrink: 0,
    }}>{ch}</div>
  );
}

function P_Chip({ kind, tone, mono }) {
  const t = {
    success: { bg: 'var(--success-bg)', fg: 'var(--success)' },
    warn: { bg: 'var(--warn-bg)', fg: 'var(--warn-strong)' },
    info: { bg: 'var(--info-bg)', fg: 'var(--info-strong)' },
    accent: { bg: 'var(--accent-soft)', fg: 'var(--accent-strong)' },
    danger: { bg: 'var(--danger-bg)', fg: 'var(--danger-strong)' },
    neutral: { bg: 'var(--bg-elev)', fg: 'var(--fg-muted)' },
  }[tone || 'neutral'];
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 5,
      padding: '2px 8px', borderRadius: 999,
      background: t.bg, color: t.fg,
      fontFamily: mono ? 'var(--f-mono)' : 'var(--f-ui)',
      fontSize: 11, fontWeight: 600, letterSpacing: mono ? '0.02em' : 0,
    }}>{kind}</span>
  );
}

function P_QueueRow({ m, active }) {
  return (
    <div style={{
      padding: '12px 14px',
      borderLeft: active ? '2px solid var(--accent)' : '2px solid transparent',
      background: active ? 'var(--bg-elev)' : 'transparent',
      cursor: 'pointer',
      borderBottom: '1px solid var(--border-sub)',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 9, marginBottom: 4 }}>
        <P_Av ch={m.av} h={m.h} />
        <div style={{ minWidth: 0, flex: 1 }}>
          <div style={{
            fontSize: 13, fontWeight: 600, color: 'var(--fg)',
            whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          }}>{m.subject}</div>
          <div style={{
            fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)',
            whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          }}>{m.agent} → {m.to}</div>
        </div>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, paddingLeft: 35 }}>
        <span style={{
          fontFamily: 'var(--f-mono)', fontSize: 10,
          color: m.exp.startsWith('in 47m') ? 'var(--warn-strong)' : 'var(--fg-subtle)',
          letterSpacing: '0.02em',
        }}>expires {m.exp}</span>
        <span style={{ color: 'var(--fg-subtle)' }}>·</span>
        <span style={{ fontFamily: 'var(--f-mono)', fontSize: 10, color: 'var(--fg-subtle)' }}>{m.ago}</span>
        {m.kind && <P_Chip kind={m.kind} tone={m.kindTone || 'neutral'} mono />}
      </div>
    </div>
  );
}

function LoftPending() {
  const queue = [
    { id: 'msg_abc123', subject: 'Re: Q3 contract renewal terms', agent: 'support@acme', to: 'legal@stripe.com', exp: 'in 47m', ago: '13m ago', av: 'SP', h: 1, kind: 'reply', kindTone: 'info' },
    { id: 'msg_def456', subject: 'Follow-up — pricing for enterprise tier', agent: 'sales@acme', to: 'eng@northwind.co', exp: 'in 2h 12m', ago: '1h ago', av: 'SA', h: 4, kind: 'send', kindTone: 'accent' },
    { id: 'msg_ghi789', subject: 'Refund request — order #4128', agent: 'support@acme', to: 'customer@gmail.com', exp: 'in 5h 30m', ago: '2h ago', av: 'SP', h: 1, kind: 'reply', kindTone: 'info' },
    { id: 'msg_jkl012', subject: 'Calendar invite: kickoff Mon 10am', agent: 'admin@acme', to: '3 recipients', exp: 'in 22h', ago: '5h ago', av: 'AD', h: 6, kind: 'send', kindTone: 'accent' },
  ];
  const selected = queue[0];

  return (
    <div style={{
      width: 1280, height: 920,
      background: 'var(--bg)', color: 'var(--fg)',
      fontFamily: 'var(--f-ui)',
      display: 'flex',
      overflow: 'hidden',
    }}>
      <D_SidebarPending />

      <main style={{ flex: 1, overflow: 'auto', display: 'flex', flexDirection: 'column' }}>
        {/* Top bar */}
        <div style={{
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          padding: '14px 28px',
          borderBottom: '1px solid var(--border)',
          background: 'var(--bg-panel)',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: 12, color: 'var(--fg-muted)' }}>
            <span>Acme Robotics</span>
            <span>/</span>
            <span style={{ color: 'var(--fg)' }}>Pending</span>
            <span>/</span>
            <span style={{ fontFamily: 'var(--f-mono)', color: 'var(--fg)' }}>{selected.id}</span>
          </div>
          <div style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)' }}>
            <span style={{
              display: 'inline-flex', alignItems: 'center', gap: 6,
              color: 'var(--accent-strong)', fontWeight: 600,
            }}>
              <span style={{ width: 7, height: 7, borderRadius: '50%', background: 'var(--accent)' }} />
              4 pending · 2 expire within 1h
            </span>
          </div>
        </div>

        {/* Page header */}
        <div style={{ padding: '24px 28px 18px' }}>
          <span style={{
            fontFamily: 'var(--f-mono)', fontSize: 11, fontWeight: 600,
            color: 'var(--accent-strong)', letterSpacing: '0.08em', textTransform: 'uppercase',
          }}>Human-in-the-loop · Inbound review</span>
          <h1 style={{
            fontFamily: 'var(--f-ui)', fontSize: 26, fontWeight: 700,
            letterSpacing: '-0.012em', color: 'var(--fg)',
            margin: '6px 0 4px',
          }}>Pending approval</h1>
          <p style={{ fontSize: 13, color: 'var(--fg-muted)', margin: 0 }}>
            Outbound messages your agents want to send. Approve as-is, edit, or reject.
          </p>
        </div>

        {/* 2-column layout */}
        <div style={{
          display: 'grid', gridTemplateColumns: '320px 1fr',
          gap: 0, flex: 1, minHeight: 0,
          padding: '0 28px 28px',
        }}>
          {/* Queue */}
          <div style={{
            background: 'var(--bg-panel)',
            border: '1px solid var(--border)',
            borderTopLeftRadius: 'var(--r-lg)',
            borderBottomLeftRadius: 'var(--r-lg)',
            display: 'flex', flexDirection: 'column',
            minHeight: 0, overflow: 'hidden',
          }}>
            <div style={{
              padding: '12px 14px', borderBottom: '1px solid var(--border)',
              display: 'flex', alignItems: 'center', justifyContent: 'space-between',
              background: 'var(--bg-elev)',
            }}>
              <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg)' }}>Queue</span>
              <span style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)' }}>4</span>
            </div>
            <div style={{ overflowY: 'auto', flex: 1 }}>
              {queue.map((m, i) => <P_QueueRow key={m.id} m={m} active={i === 0} />)}
            </div>
          </div>

          {/* Detail */}
          <div style={{
            background: 'var(--bg-panel)',
            border: '1px solid var(--border)',
            borderLeft: 'none',
            borderTopRightRadius: 'var(--r-lg)',
            borderBottomRightRadius: 'var(--r-lg)',
            display: 'flex', flexDirection: 'column',
            minHeight: 0, overflow: 'hidden',
          }}>
            {/* Header */}
            <div style={{
              padding: '18px 22px',
              borderBottom: '1px solid var(--border)',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                <P_Chip kind="Reply" tone="info" mono />
                <P_Chip kind={<><span style={{ width: 6, height: 6, borderRadius: '50%', background: 'var(--warn)', display: 'inline-block', marginRight: 2 }} />Pending</>} tone="warn" />
                <span style={{
                  fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--warn-strong)',
                  letterSpacing: '0.02em', fontWeight: 600,
                }}>Expires in 47m · auto-reject on TTL</span>
                <span style={{ flex: 1 }} />
                <span style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)' }}>{selected.id}</span>
              </div>
              <div style={{
                fontFamily: 'var(--f-ui)', fontSize: 20, fontWeight: 600,
                color: 'var(--fg)', letterSpacing: '-0.01em',
              }}>{selected.subject}</div>
              <div style={{
                fontFamily: 'var(--f-mono)', fontSize: 12, color: 'var(--fg-muted)',
                marginTop: 8, display: 'flex', gap: 16, flexWrap: 'wrap',
              }}>
                <span><span style={{ color: 'var(--fg-subtle)' }}>from</span> {selected.agent}@acme.io</span>
                <span><span style={{ color: 'var(--fg-subtle)' }}>to</span> legal@stripe.com</span>
                <span><span style={{ color: 'var(--fg-subtle)' }}>conversation</span> conv_K3p9aQ</span>
                <span><span style={{ color: 'var(--fg-subtle)' }}>queued</span> 13m ago</span>
              </div>
            </div>

            {/* Body */}
            <div style={{ flex: 1, overflowY: 'auto', display: 'flex', flexDirection: 'column' }}>
              {/* Two-pane: draft + context */}
              <div style={{
                display: 'grid', gridTemplateColumns: '1.4fr 1fr',
                borderBottom: '1px solid var(--border)',
                minHeight: 0,
              }}>
                {/* Draft */}
                <div style={{ padding: '18px 22px', borderRight: '1px solid var(--border-sub)' }}>
                  <div style={{
                    fontFamily: 'var(--f-mono)', fontSize: 11, fontWeight: 600,
                    color: 'var(--accent-strong)', letterSpacing: '0.08em',
                    textTransform: 'uppercase', marginBottom: 10,
                  }}>Draft from agent</div>
                  <div style={{
                    fontSize: 14, color: 'var(--fg)', lineHeight: 1.65, whiteSpace: 'pre-wrap',
                  }}>{`Hi Maya,

Thanks for sending over the renewal draft. I've flagged two items
the team would like to revisit before signing:

  • §4.2 — the auto-renewal window is currently 90 days; we'd
    prefer 60 to align with our procurement cycle.
  • §7.1 — please confirm the data-processing addendum at v2.3.

Happy to jump on a 15-min call this week if easier — Tue or
Thu afternoon both work on our end.

Best,
Acme Support`}</div>

                  {/* Inline draft footer */}
                  <div style={{
                    marginTop: 16, paddingTop: 12,
                    borderTop: '1px solid var(--border-sub)',
                    display: 'flex', gap: 18, fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)',
                  }}>
                    <span>180 words · 1.2 KB</span>
                    <span>language: en</span>
                    <span style={{ color: 'var(--success)' }}>✓ no PII detected</span>
                  </div>
                </div>

                {/* Context */}
                <div style={{ padding: '18px 22px', background: 'var(--bg-elev)' }}>
                  <div style={{
                    fontFamily: 'var(--f-mono)', fontSize: 11, fontWeight: 600,
                    color: 'var(--accent-strong)', letterSpacing: '0.08em',
                    textTransform: 'uppercase', marginBottom: 10,
                  }}>In reply to · 12m ago</div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 9, marginBottom: 8 }}>
                    <P_Av ch="MK" h={3} />
                    <div>
                      <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)' }}>Maya K. <span style={{ color: 'var(--fg-muted)', fontWeight: 400 }}>· Stripe Legal</span></div>
                      <div style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)' }}>maya.k@stripe.com</div>
                    </div>
                  </div>
                  <div style={{
                    fontSize: 13, color: 'var(--fg-muted)', lineHeight: 1.6,
                    background: 'var(--bg-panel)',
                    border: '1px solid var(--border-sub)',
                    padding: '12px 14px', borderRadius: 'var(--r-md)',
                    fontStyle: 'italic',
                  }}>
                    "Attached is the renewal contract for Q3. Standard terms — please
                    let us know if there are any redlines on your side before EOW…"
                  </div>

                  {/* Auth provenance */}
                  <div style={{
                    marginTop: 16,
                    fontFamily: 'var(--f-mono)', fontSize: 11, lineHeight: 1.7,
                    color: 'var(--fg-muted)',
                  }}>
                    <div style={{
                      fontFamily: 'var(--f-mono)', fontSize: 11, fontWeight: 600,
                      color: 'var(--accent-strong)', letterSpacing: '0.08em',
                      textTransform: 'uppercase', marginBottom: 8, fontFamily: 'var(--f-mono)',
                    }}>Provenance</div>
                    <div>SPF&nbsp;<span style={{ color: 'var(--success)' }}>pass</span> · DKIM&nbsp;<span style={{ color: 'var(--success)' }}>pass</span> · DMARC&nbsp;<span style={{ color: 'var(--success)' }}>pass</span></div>
                    <div>sender · <span style={{ color: 'var(--fg)' }}>human</span> · stripe.com</div>
                    <div>sha256 · 9f4c…3a1b</div>
                    <div>X-E2A-Auth · <span style={{ color: 'var(--success)' }}>verified</span></div>
                  </div>
                </div>
              </div>

              {/* Outbound preview (ink block — "what will be sent") */}
              <div style={{ padding: '18px 22px' }}>
                <div style={{
                  fontFamily: 'var(--f-mono)', fontSize: 11, fontWeight: 600,
                  color: 'var(--accent-strong)', letterSpacing: '0.08em',
                  textTransform: 'uppercase', marginBottom: 10,
                }}>Headers that will be sent</div>

                <div style={{
                  background: 'var(--ink)', borderRadius: 'var(--r-lg)',
                  border: '1px solid var(--ink-border)',
                  padding: '14px 18px',
                  fontFamily: 'var(--f-mono)', fontSize: 12, lineHeight: 1.75,
                  color: 'var(--ink-fg)',
                }}>
                  <div style={{ color: 'var(--ink-fg-muted)' }}># will be signed at send-time</div>
                  <div><span style={{ color: 'var(--ink-fg-muted)' }}>From:</span> support@acme.io</div>
                  <div><span style={{ color: 'var(--ink-fg-muted)' }}>To:</span> <span style={{ color: 'var(--spectral)' }}>maya.k@stripe.com</span></div>
                  <div><span style={{ color: 'var(--ink-fg-muted)' }}>Subject:</span> Re: Q3 contract renewal terms</div>
                  <div><span style={{ color: 'var(--ink-fg-muted)' }}>In-Reply-To:</span> <span style={{ color: 'var(--spectral)' }}>&lt;b7..a1@stripe.com&gt;</span></div>
                  <div><span style={{ color: 'var(--ink-fg-muted)' }}>X-E2A-Conversation-Id:</span> conv_K3p9aQ</div>
                  <div><span style={{ color: 'var(--ink-fg-muted)' }}>X-E2A-Auth-Sender:</span> support@acme.io</div>
                  <div><span style={{ color: 'var(--ink-fg-muted)' }}>X-E2A-Auth-Body-Hash:</span> sha256:<span style={{ color: 'var(--spectral)' }}>9f4c…3a1b</span></div>
                  <div><span style={{ color: 'var(--ink-fg-muted)' }}>X-E2A-Auth-Signature:</span> <span style={{ color: 'var(--machine)' }}>hmac-sha256:7e2a…ff09</span></div>
                </div>
              </div>
            </div>

            {/* Action bar */}
            <div style={{
              borderTop: '1px solid var(--border)',
              padding: '14px 22px',
              background: 'var(--bg-elev)',
              display: 'flex', alignItems: 'center', gap: 10,
            }}>
              <button style={{
                fontFamily: 'var(--f-ui)', fontSize: 13, fontWeight: 500,
                padding: '9px 18px', borderRadius: 'var(--r-md)',
                background: 'var(--accent-fill)', color: '#fff',
                border: 'none', cursor: 'pointer',
                display: 'inline-flex', alignItems: 'center', gap: 8,
              }}>
                Approve & send
                <span style={{
                  fontFamily: 'var(--f-mono)', fontSize: 10, fontWeight: 600,
                  background: 'rgba(255,255,255,.18)', padding: '1px 5px', borderRadius: 3,
                }}>⌘↵</span>
              </button>
              <button style={{
                fontFamily: 'var(--f-ui)', fontSize: 13, fontWeight: 500,
                padding: '9px 14px', borderRadius: 'var(--r-md)',
                background: 'var(--bg-panel)', color: 'var(--fg)',
                border: '1px solid var(--border)', cursor: 'pointer',
              }}>Edit draft</button>
              <button style={{
                fontFamily: 'var(--f-ui)', fontSize: 13, fontWeight: 500,
                padding: '9px 14px', borderRadius: 'var(--r-md)',
                background: 'var(--bg-panel)', color: 'var(--danger-strong)',
                border: '1px solid var(--danger-bg)', cursor: 'pointer',
              }}>Reject</button>
              <span style={{ flex: 1 }} />
              <span style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-muted)' }}>
                or use the CLI:
              </span>
              <code style={{
                fontFamily: 'var(--f-mono)', fontSize: 12,
                background: 'var(--bg-panel)', border: '1px solid var(--border)',
                padding: '4px 9px', borderRadius: 'var(--r-sm)',
                color: 'var(--fg)',
              }}>e2a pending approve {selected.id}</code>
            </div>
          </div>
        </div>
      </main>
    </div>
  );
}

// Sidebar with Pending active — small variant so we can share the layout
function D_SidebarPending() {
  const items = [
    { label: 'Get started', icon: 'plus' },
    { label: 'Agents', icon: 'grid' },
    { label: 'Pending', icon: 'clock', active: true, badge: 4 },
    { label: 'Domains', icon: 'globe' },
    { label: 'API keys', icon: 'key' },
  ];

  const SVG = {
    plus: <><circle cx="12" cy="12" r="9.5" /><path d="M12 8v8M8 12h8" /></>,
    grid: <><rect x="3.5" y="3.5" width="7" height="7" rx="1.5" /><rect x="13.5" y="3.5" width="7" height="7" rx="1.5" /><rect x="3.5" y="13.5" width="7" height="7" rx="1.5" /><rect x="13.5" y="13.5" width="7" height="7" rx="1.5" /></>,
    clock: <><circle cx="12" cy="12" r="9.5" /><polyline points="12 6.5 12 12 16 14" /></>,
    globe: <><circle cx="12" cy="12" r="9.5" /><path d="M3 12h18" /><path d="M12 3a16 16 0 010 18 16 16 0 010-18z" /></>,
    key: <><circle cx="8" cy="14" r="4" /><path d="M11 11l9-9M15 6l3 3M18 3l3 3" /></>,
    settings: <><circle cx="12" cy="12" r="2.5" /><path d="M19 12a7 7 0 00-.1-1.3l2-1.6-2-3.5-2.4 1a7 7 0 00-2.2-1.3l-.3-2.6h-4l-.4 2.6a7 7 0 00-2.2 1.3l-2.4-1-2 3.5 2 1.6A7 7 0 005 12c0 .4 0 .9.1 1.3l-2 1.6 2 3.5 2.4-1a7 7 0 002.2 1.3l.4 2.6h4l.3-2.6a7 7 0 002.2-1.3l2.4 1 2-3.5-2-1.6c.1-.4.1-.9.1-1.3z" /></>,
    msg: <path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z" />,
  };

  return (
    <aside style={{
      width: 248, flexShrink: 0,
      background: 'var(--bg-panel)',
      borderRight: '1px solid var(--border)',
      display: 'flex', flexDirection: 'column',
      height: '100%',
    }}>
      <div style={{
        padding: '18px 20px 16px',
        borderBottom: '1px solid var(--border)',
        display: 'flex', alignItems: 'center', gap: 11,
      }}>
        <div style={{
          width: 32, height: 32, borderRadius: 7,
          background: 'var(--fg)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          color: 'var(--bg)', fontFamily: 'var(--f-mono)', fontWeight: 700, fontSize: 12,
          letterSpacing: '-0.04em',
        }}>e2a</div>
        <div>
          <div style={{ fontFamily: 'var(--f-mono)', fontWeight: 700, fontSize: 14, color: 'var(--fg)', lineHeight: 1.1, letterSpacing: '-0.02em' }}>e2a</div>
          <div style={{ fontSize: 11, color: 'var(--fg-muted)' }}>Email for AI agents</div>
        </div>
      </div>

      <div style={{ padding: '12px 14px 6px' }}>
        <div style={{
          display: 'flex', alignItems: 'center', gap: 10,
          padding: '8px 10px',
          background: 'var(--bg-elev)', borderRadius: 'var(--r-md)',
          border: '1px solid var(--border-sub)',
        }}>
          <div style={{
            width: 22, height: 22, borderRadius: 5,
            background: 'var(--av-1)', color: '#fff',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            fontSize: 10, fontWeight: 700,
          }}>AC</div>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--fg)' }}>Acme Robotics</div>
            <div style={{ fontFamily: 'var(--f-mono)', fontSize: 10, color: 'var(--fg-subtle)' }}>org_8nlqv</div>
          </div>
          <span style={{ color: 'var(--fg-subtle)', fontSize: 10 }}>▾</span>
        </div>
      </div>

      <nav style={{ padding: '6px 12px', flex: 1 }}>
        {items.map(it => (
          <a key={it.label} href="#" style={{
            display: 'flex', alignItems: 'center', gap: 11,
            padding: '8px 12px', borderRadius: 'var(--r-md)',
            fontSize: 13, fontFamily: 'var(--f-ui)', fontWeight: it.active ? 500 : 400,
            color: it.active ? 'var(--fg)' : 'var(--fg-muted)',
            background: it.active ? 'var(--bg-elev)' : 'transparent',
            boxShadow: it.active ? 'inset 2px 0 0 var(--accent)' : 'none',
            textDecoration: 'none', marginBottom: 1,
          }}>
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
              {SVG[it.icon]}
            </svg>
            <span style={{ flex: 1 }}>{it.label}</span>
            {it.badge && (
              <span style={{
                display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
                minWidth: 18, height: 18, padding: '0 6px',
                background: 'var(--accent)', color: '#fff',
                borderRadius: 999, fontSize: 10, fontWeight: 700,
                fontFamily: 'var(--f-mono)',
              }}>{it.badge}</span>
            )}
          </a>
        ))}
      </nav>

      <div style={{ padding: '10px 12px 14px', borderTop: '1px solid var(--border)' }}>
        <a href="#" style={{
          display: 'flex', alignItems: 'center', gap: 11,
          padding: '8px 12px', borderRadius: 'var(--r-md)',
          fontSize: 13, color: 'var(--fg-muted)', textDecoration: 'none',
        }}>
          <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden>{SVG.settings}</svg>
          Settings
        </a>

        <div style={{
          marginTop: 10,
          display: 'flex', alignItems: 'center', gap: 10,
          padding: '8px 10px',
          borderRadius: 'var(--r-md)',
          border: '1px solid var(--border-sub)',
        }}>
          <div style={{
            width: 28, height: 28, borderRadius: '50%',
            background: 'var(--av-4)', color: '#fff',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            fontSize: 11, fontWeight: 700,
          }}>JM</div>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontSize: 12, fontWeight: 500, color: 'var(--fg)' }}>Jamie M.</div>
            <div style={{ fontFamily: 'var(--f-mono)', fontSize: 10, color: 'var(--fg-subtle)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>jamie@acme.io</div>
          </div>
        </div>
      </div>
    </aside>
  );
}

window.D_SidebarPending = D_SidebarPending;
window.LoftPending = LoftPending;
