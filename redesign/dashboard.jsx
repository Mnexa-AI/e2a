// dashboard.jsx — e2a Agents dashboard, Loft-styled
// Sidebar + main column. Cream shell + ink code accents. 1280×920 artboard.

function D_Sidebar() {
  const items = [
    { label: 'Get started', icon: 'plus' },
    { label: 'Agents', icon: 'grid', active: true },
    { label: 'Pending', icon: 'clock', badge: 2 },
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
      {/* Logo block */}
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

      {/* Org switcher */}
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

      {/* Nav */}
      <nav style={{ padding: '6px 12px', flex: 1 }}>
        {items.map(it => (
          <a key={it.label} href="#" style={{
            display: 'flex', alignItems: 'center', gap: 11,
            padding: '8px 12px', borderRadius: 'var(--r-md)',
            fontSize: 13, fontFamily: 'var(--f-ui)', fontWeight: it.active ? 500 : 400,
            color: it.active ? 'var(--fg)' : 'var(--fg-muted)',
            background: it.active ? 'var(--bg-elev)' : 'transparent',
            boxShadow: it.active ? 'inset 2px 0 0 var(--accent)' : 'none',
            textDecoration: 'none', position: 'relative',
            marginBottom: 1,
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

      {/* Bottom */}
      <div style={{ padding: '10px 12px 14px', borderTop: '1px solid var(--border)' }}>
        <a href="#" style={{
          display: 'flex', alignItems: 'center', gap: 11,
          padding: '8px 12px', borderRadius: 'var(--r-md)',
          fontSize: 13, color: 'var(--fg-muted)', textDecoration: 'none',
        }}>
          <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden>{SVG.settings}</svg>
          Settings
        </a>
        <a href="#" style={{
          display: 'flex', alignItems: 'center', gap: 11,
          padding: '8px 12px', borderRadius: 'var(--r-md)',
          fontSize: 13, color: 'var(--fg-muted)', textDecoration: 'none',
        }}>
          <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden>{SVG.msg}</svg>
          Send feedback
        </a>

        {/* user */}
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

function D_Chip({ kind, tone, mono }) {
  const t = {
    success: { bg: 'var(--success-bg)', fg: 'var(--success)' },
    warn: { bg: 'var(--warn-bg)', fg: 'var(--warn-strong)' },
    info: { bg: 'var(--info-bg)', fg: 'var(--info-strong)' },
    accent: { bg: 'var(--accent-soft)', fg: 'var(--accent-strong)' },
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

function D_StatusDot({ tone = 'success' }) {
  const c = { success: 'var(--success)', warn: 'var(--warn)', accent: 'var(--accent)', danger: 'var(--danger)' }[tone];
  return <span style={{ width: 7, height: 7, borderRadius: '50%', background: c, display: 'inline-block' }} />;
}

function D_AgentCard({ a }) {
  return (
    <div style={{
      background: 'var(--bg-panel)',
      border: '1px solid var(--border)',
      borderRadius: 'var(--r-lg)',
      padding: '20px 22px',
    }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 18 }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap', marginBottom: 8 }}>
            <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--fg)' }}>{a.name}</span>
            <code style={{
              fontFamily: 'var(--f-mono)', fontSize: 13,
              color: 'var(--fg)', background: 'var(--bg-elev)',
              padding: '2px 8px', borderRadius: 'var(--r-sm)',
              border: '1px solid var(--border-sub)',
            }}>{a.email}</code>
            <D_Chip kind={<><D_StatusDot tone={a.verified ? 'success' : 'warn'} />{a.verified ? 'Verified' : 'Unverified'}</>} tone={a.verified ? 'success' : 'warn'} />
            <D_Chip kind={a.shared ? 'Shared' : 'Custom'} tone={a.shared ? 'info' : 'accent'} />
            <D_Chip kind={a.mode} tone="neutral" mono />
            {a.hitl && <D_Chip kind="HITL on" tone="accent" />}
          </div>
          <div style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)', letterSpacing: '0.02em' }}>
            agent_{a.id} · created {a.created} · webhook {a.webhookOk ? 'reachable' : 'unreachable'}
          </div>

          {/* Mode switcher */}
          <div style={{
            marginTop: 14,
            display: 'inline-flex', gap: 2, padding: 3,
            background: 'var(--bg-elev)', border: '1px solid var(--border-sub)',
            borderRadius: 'var(--r-md)',
          }}>
            {['Cloud (webhook)', 'Local (WebSocket)'].map((t, i) => (
              <div key={t} style={{
                fontSize: 11, padding: '4px 12px', borderRadius: 'var(--r-sm)',
                background: (a.mode === 'Local') === (i === 1) ? 'var(--bg-panel)' : 'transparent',
                color: (a.mode === 'Local') === (i === 1) ? 'var(--fg)' : 'var(--fg-muted)',
                fontWeight: (a.mode === 'Local') === (i === 1) ? 600 : 400,
                boxShadow: (a.mode === 'Local') === (i === 1) ? 'var(--sh-1)' : 'none',
              }}>{t}</div>
            ))}
          </div>
        </div>

        <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
          <button style={btnGhost}>Test</button>
          <button style={btnGhost}>Connect</button>
          <button style={btnGhost}>⋯</button>
        </div>
      </div>

      {/* Footer row: activity */}
      <div style={{
        marginTop: 16,
        paddingTop: 14,
        borderTop: '1px solid var(--border-sub)',
        display: 'grid', gridTemplateColumns: '1fr 1fr 1fr 1fr', gap: 16,
      }}>
        {[
          ['Inbound · 7d', a.inbound, 'success'],
          ['Outbound · 7d', a.outbound, 'info'],
          ['Pending', a.pending, 'accent'],
          ['Last delivery', a.lastDelivery, 'neutral'],
        ].map(([label, val, tone]) => (
          <div key={label}>
            <div style={{
              fontFamily: 'var(--f-mono)', fontSize: 10, fontWeight: 600,
              color: 'var(--fg-subtle)', letterSpacing: '0.08em', textTransform: 'uppercase',
              marginBottom: 4,
            }}>{label}</div>
            <div style={{
              fontFamily: tone === 'neutral' ? 'var(--f-mono)' : 'var(--f-ui)',
              fontSize: tone === 'neutral' ? 12 : 18,
              fontWeight: tone === 'neutral' ? 400 : 600,
              color: 'var(--fg)',
              letterSpacing: tone === 'neutral' ? '0.02em' : '-0.01em',
            }}>{val}</div>
          </div>
        ))}
      </div>
    </div>
  );
}

const btnGhost = {
  fontFamily: 'var(--f-ui)', fontSize: 12, fontWeight: 500,
  padding: '7px 12px', borderRadius: 'var(--r-md)',
  background: 'var(--bg-panel)', color: 'var(--fg)',
  border: '1px solid var(--border)', cursor: 'pointer',
};

function LoftDashboard() {
  const agents = [
    {
      id: 'k3p9a2', name: 'Inbox triage bot', email: 'triage@acme.io',
      verified: true, shared: false, mode: 'Cloud', hitl: false,
      created: 'Apr 12', webhookOk: true,
      inbound: '1,284', outbound: '942', pending: '0', lastDelivery: '3s ago',
    },
    {
      id: 'q1m4nx', name: 'Customer support', email: 'support@agents.e2a.dev',
      verified: true, shared: true, mode: 'Cloud', hitl: true,
      created: 'Apr 18', webhookOk: true,
      inbound: '512', outbound: '397', pending: '2', lastDelivery: '14m ago',
    },
    {
      id: 'b7tz5d', name: 'Outbound sales', email: 'jenny@acme.io',
      verified: true, shared: false, mode: 'Local', hitl: true,
      created: 'May 02', webhookOk: true,
      inbound: '88', outbound: '64', pending: '0', lastDelivery: '2h ago',
    },
    {
      id: 'r0vn9k', name: 'Procurement assistant', email: 'po@acme.io',
      verified: false, shared: false, mode: 'Cloud', hitl: false,
      created: 'May 14', webhookOk: false,
      inbound: '—', outbound: '—', pending: '—', lastDelivery: 'never',
    },
  ];

  return (
    <div style={{
      width: 1280, height: 920,
      background: 'var(--bg)', color: 'var(--fg)',
      fontFamily: 'var(--f-ui)',
      display: 'flex',
      overflow: 'hidden',
    }}>
      <D_Sidebar />

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
            <span style={{ color: 'var(--fg)' }}>Agents</span>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <div style={{
              position: 'relative',
              display: 'flex', alignItems: 'center', gap: 8,
              padding: '6px 12px',
              background: 'var(--bg-elev)', borderRadius: 'var(--r-md)',
              border: '1px solid var(--border-sub)',
              fontFamily: 'var(--f-mono)', fontSize: 12, color: 'var(--fg-subtle)',
              minWidth: 280,
            }}>
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                <circle cx="11" cy="11" r="7" /><path d="M21 21l-4.3-4.3" />
              </svg>
              <span>Search agents, messages…</span>
              <span style={{
                marginLeft: 'auto',
                fontFamily: 'var(--f-mono)', fontSize: 10,
                color: 'var(--fg-subtle)',
                background: 'var(--bg-panel)',
                border: '1px solid var(--border)',
                padding: '1px 5px', borderRadius: 3,
              }}>⌘K</span>
            </div>
          </div>
        </div>

        <div style={{ padding: '28px 28px 32px', flex: 1, minHeight: 0 }}>
          {/* Page header */}
          <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 24, marginBottom: 22 }}>
            <div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 6 }}>
                <span style={{
                  fontFamily: 'var(--f-mono)', fontSize: 11, fontWeight: 600,
                  color: 'var(--accent-strong)', letterSpacing: '0.08em',
                  textTransform: 'uppercase',
                }}>Workspace · drv_acme</span>
              </div>
              <h1 style={{
                fontFamily: 'var(--f-ui)', fontSize: 26, fontWeight: 700,
                letterSpacing: '-0.012em', color: 'var(--fg)',
                margin: 0, lineHeight: 1.15,
              }}>Agents</h1>
              <p style={{ fontSize: 13, color: 'var(--fg-muted)', margin: '6px 0 0' }}>
                4 agents · 2 verified domains · indexed&nbsp;
                <span style={{ fontFamily: 'var(--f-mono)' }}>3s ago</span>
              </p>
            </div>
            <div style={{ display: 'flex', gap: 8 }}>
              <button style={{ ...btnGhost, padding: '8px 14px' }}>Import</button>
              <button style={{
                fontFamily: 'var(--f-ui)', fontSize: 13, fontWeight: 500,
                padding: '8px 16px', borderRadius: 'var(--r-md)',
                background: 'var(--accent-fill)', color: '#fff',
                border: 'none', cursor: 'pointer',
                display: 'inline-flex', alignItems: 'center', gap: 6,
              }}>
                <span style={{ fontFamily: 'var(--f-mono)' }}>+</span> Create agent
              </button>
            </div>
          </div>

          {/* Stats strip */}
          <div style={{
            display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 12,
            marginBottom: 22,
          }}>
            {[
              ['Inbound · today', '184', '+12%', 'success'],
              ['Outbound · today', '139', '+4%', 'info'],
              ['Pending review', '2', 'oldest in 47m', 'accent'],
              ['Delivery success', '99.6%', 'last 7d', 'neutral'],
            ].map(([label, val, sub, tone]) => (
              <div key={label} style={{
                background: 'var(--bg-panel)',
                border: '1px solid var(--border)',
                borderRadius: 'var(--r-lg)',
                padding: '14px 16px',
              }}>
                <div style={{
                  fontFamily: 'var(--f-mono)', fontSize: 10, fontWeight: 600,
                  color: 'var(--fg-subtle)', letterSpacing: '0.08em', textTransform: 'uppercase',
                  marginBottom: 8,
                }}>{label}</div>
                <div style={{
                  fontSize: 28, fontWeight: 600, color: 'var(--fg)',
                  letterSpacing: '-0.02em', lineHeight: 1, marginBottom: 6,
                }}>{val}</div>
                <div style={{ fontSize: 11, color: tone === 'accent' ? 'var(--accent-strong)' : tone === 'success' ? 'var(--success)' : 'var(--fg-muted)' }}>
                  {sub}
                </div>
              </div>
            ))}
          </div>

          {/* Filter bar */}
          <div style={{
            display: 'flex', alignItems: 'center', gap: 8, marginBottom: 14,
          }}>
            {['All 4', 'Cloud 3', 'Local 1', 'HITL on 2', 'Unverified 1'].map((p, i) => (
              <button key={p} style={{
                fontFamily: 'var(--f-ui)', fontSize: 12, fontWeight: 500,
                padding: '5px 12px', borderRadius: 999,
                background: i === 0 ? 'var(--fg)' : 'var(--bg-panel)',
                color: i === 0 ? 'var(--bg)' : 'var(--fg-muted)',
                border: i === 0 ? '1px solid var(--fg)' : '1px solid var(--border)',
                cursor: 'pointer',
              }}>{p}</button>
            ))}
            <span style={{ flex: 1 }} />
            <span style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)', letterSpacing: '0.02em' }}>
              Sort: <span style={{ color: 'var(--fg-muted)' }}>last activity ▾</span>
            </span>
          </div>

          {/* Agent cards */}
          <div style={{ display: 'grid', gridTemplateColumns: '1fr', gap: 12 }}>
            {agents.map(a => <D_AgentCard key={a.id} a={a} />)}
          </div>
        </div>
      </main>
    </div>
  );
}

window.LoftDashboard = LoftDashboard;
