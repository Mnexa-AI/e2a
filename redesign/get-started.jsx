// get-started.jsx — Onboarding screen, Loft-styled.
// Three vertical steps: choose an address, configure DNS, point the agent.
// 1280 × 1080 artboard.

function GS_StepHeader({ n, label, status, children }) {
  // status: 'done' | 'active' | 'todo'
  const statusColor = {
    done:   { ring: 'var(--success)',  fill: 'var(--success)',  fg: '#fff' },
    active: { ring: 'var(--accent)',   fill: 'var(--accent)',   fg: '#fff' },
    todo:   { ring: 'var(--border)',   fill: 'var(--bg-panel)', fg: 'var(--fg-subtle)' },
  }[status];
  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', gap: 16 }}>
      <div style={{
        width: 28, height: 28, borderRadius: '50%',
        background: statusColor.fill, color: statusColor.fg,
        border: `1px solid ${statusColor.ring}`,
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        fontFamily: 'var(--f-mono)', fontSize: 12, fontWeight: 700,
        flexShrink: 0, marginTop: 2,
      }}>
        {status === 'done'
          ? <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"><polyline points="20 6 9 17 4 12" /></svg>
          : n}
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{
          fontFamily: 'var(--f-ui)', fontSize: 16, fontWeight: 600,
          color: status === 'todo' ? 'var(--fg-muted)' : 'var(--fg)',
          letterSpacing: '-0.005em',
        }}>{label}</div>
        {children && <div style={{ marginTop: 14 }}>{children}</div>}
      </div>
    </div>
  );
}

function GS_AddressChoice() {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
      {/* Shared option */}
      <div style={{
        background: 'var(--bg-panel)',
        border: '2px solid var(--accent)',
        borderRadius: 'var(--r-lg)',
        padding: 18,
        position: 'relative',
      }}>
        <div style={{
          position: 'absolute', top: 14, right: 14,
        }}>
          <LoftChip tone="accent">Recommended</LoftChip>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
          <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--fg)' }}>Shared address</span>
          <LoftChip tone="success" mono>1 min</LoftChip>
        </div>
        <div style={{ fontFamily: 'var(--f-mono)', fontSize: 12, color: 'var(--accent-strong)', marginBottom: 12 }}>
          you@agents.e2a.dev
        </div>
        <ul style={{
          margin: 0, padding: 0, listStyle: 'none',
          fontSize: 12, color: 'var(--fg-muted)', lineHeight: 1.7,
        }}>
          <li>· Skip DNS setup entirely</li>
          <li>· Inherits e2a's verified domain</li>
          <li>· Best for prototypes and testing</li>
        </ul>
      </div>

      {/* Custom option */}
      <div style={{
        background: 'var(--bg-panel)',
        border: '1px solid var(--border)',
        borderRadius: 'var(--r-lg)',
        padding: 18,
        position: 'relative',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
          <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--fg)' }}>Custom domain</span>
          <LoftChip tone="neutral" mono>~10 min</LoftChip>
        </div>
        <div style={{ fontFamily: 'var(--f-mono)', fontSize: 12, color: 'var(--fg-muted)', marginBottom: 12 }}>
          you@<span style={{ color: 'var(--fg)' }}>yourcompany.com</span>
        </div>
        <ul style={{
          margin: 0, padding: 0, listStyle: 'none',
          fontSize: 12, color: 'var(--fg-muted)', lineHeight: 1.7,
        }}>
          <li>· Use your own domain (e.g. acme.io)</li>
          <li>· Add 3 DNS records · verify in &lt;5min</li>
          <li>· Production-ready, brand-safe</li>
        </ul>
      </div>
    </div>
  );
}

function GS_DnsRow({ type, name, value }) {
  return (
    <div style={{
      display: 'grid',
      gridTemplateColumns: '60px 1fr 2fr 28px',
      gap: 12, alignItems: 'center',
      padding: '10px 14px',
      borderTop: '1px solid var(--border-sub)',
      fontFamily: 'var(--f-mono)', fontSize: 12,
    }}>
      <span style={{ color: 'var(--accent-strong)', fontWeight: 600 }}>{type}</span>
      <span style={{ color: 'var(--fg)' }}>{name}</span>
      <span style={{
        color: 'var(--fg-muted)',
        whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
      }}>{value}</span>
      <button style={{ ...loftBtnGhost, padding: '4px 6px', fontSize: 10 }}>
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden>
          <rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V5a2 2 0 012-2h10" />
        </svg>
      </button>
    </div>
  );
}

function GS_DnsTable() {
  return (
    <div style={{
      background: 'var(--bg-panel)',
      border: '1px solid var(--border)',
      borderRadius: 'var(--r-lg)',
      overflow: 'hidden',
    }}>
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '12px 14px',
        background: 'var(--bg-elev)',
      }}>
        <div>
          <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)' }}>
            Add these 3 records to <span style={{ fontFamily: 'var(--f-mono)' }}>acme.io</span>
          </div>
          <div style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)', marginTop: 2 }}>
            DNS provider · Cloudflare · propagation ~2 min
          </div>
        </div>
        <div style={{ display: 'flex', gap: 6 }}>
          <button style={loftBtnGhost}>Copy all</button>
          <button style={{
            ...loftBtnPrimary, padding: '6px 12px', fontSize: 12,
          }}>
            <LoftDot tone="warn" /> Check DNS
          </button>
        </div>
      </div>
      <div style={{
        display: 'grid',
        gridTemplateColumns: '60px 1fr 2fr 28px',
        gap: 12, padding: '8px 14px',
        background: 'var(--bg-elev)',
        fontFamily: 'var(--f-mono)', fontSize: 10, fontWeight: 600,
        color: 'var(--fg-subtle)', letterSpacing: '0.08em', textTransform: 'uppercase',
        borderBottom: '1px solid var(--border-sub)',
      }}>
        <span>Type</span><span>Name</span><span>Value</span><span></span>
      </div>
      <GS_DnsRow type="MX"  name="@"               value="10 mx.e2a.dev" />
      <GS_DnsRow type="TXT" name="@"               value="v=spf1 include:_spf.e2a.dev ~all" />
      <GS_DnsRow type="TXT" name="e2a._domainkey"  value="v=DKIM1; k=rsa; p=MIGfMA0GCSqGSIb3DQEBAQUAA4…" />
    </div>
  );
}

function LoftGetStarted() {
  return (
    <div style={{
      width: 1280, height: 1080,
      background: 'var(--bg)', color: 'var(--fg)',
      fontFamily: 'var(--f-ui)', display: 'flex', overflow: 'hidden',
    }}>
      <LoftSidebar active="get-started" />

      <main style={{ flex: 1, overflow: 'auto', display: 'flex', flexDirection: 'column' }}>
        <LoftTopbar crumbs={['Acme Robotics', 'Get started']} />

        <div style={{ padding: '32px 48px 48px', maxWidth: 880, width: '100%' }}>
          {/* Page header */}
          <div style={{ marginBottom: 28 }}>
            <LoftEyebrow>Onboarding · est. 3 minutes</LoftEyebrow>
            <h1 style={{
              fontFamily: "'Instrument Serif', Georgia, serif",
              fontSize: 44, fontWeight: 400,
              letterSpacing: '-0.012em', color: 'var(--fg)',
              margin: '12px 0 8px', lineHeight: 1.05,
            }}>
              Wire up your first <em style={{ color: 'var(--accent-strong)' }}>agent inbox.</em>
            </h1>
            <p style={{ fontSize: 14, color: 'var(--fg-muted)', margin: 0, lineHeight: 1.6, maxWidth: 640 }}>
              Pick how your agent gets mail, then point e2a at the place your code is running.
              You can change all of this later from the dashboard.
            </p>
          </div>

          {/* Step 1 — done */}
          <div style={{ marginBottom: 24 }}>
            <GS_StepHeader n={1} label="Pick an address" status="done">
              <div style={{
                background: 'var(--bg-panel)',
                border: '1px solid var(--border)',
                borderRadius: 'var(--r-md)',
                padding: '10px 14px',
                display: 'flex', alignItems: 'center', gap: 10,
              }}>
                <LoftDot tone="success" />
                <span style={{ fontSize: 13, color: 'var(--fg)' }}>Custom domain selected:</span>
                <code style={{
                  fontFamily: 'var(--f-mono)', fontSize: 12, color: 'var(--fg)',
                  background: 'var(--bg-elev)', padding: '2px 8px',
                  border: '1px solid var(--border-sub)', borderRadius: 'var(--r-sm)',
                }}>acme.io</code>
                <span style={{ flex: 1 }} />
                <a href="#" style={{ fontSize: 12, color: 'var(--fg-muted)', textDecoration: 'underline' }}>change</a>
              </div>
            </GS_StepHeader>
          </div>

          {/* Step 2 — active */}
          <div style={{
            position: 'relative',
            paddingLeft: 0,
            marginBottom: 24,
          }}>
            {/* vertical guide */}
            <div style={{
              position: 'absolute', left: 13.5, top: -22, height: 22,
              width: 1, background: 'var(--border)',
            }} />
            <GS_StepHeader n={2} label="Add DNS records to your domain" status="active">
              <GS_DnsTable />
              <div style={{ marginTop: 10, fontSize: 12, color: 'var(--fg-muted)' }}>
                We'll re-check every 30 seconds. Most domains verify in under 2 minutes ·{' '}
                <a href="#" style={{ color: 'var(--accent-strong)' }}>full DNS guide →</a>
              </div>
            </GS_StepHeader>
          </div>

          {/* Step 3 — todo */}
          <div style={{ position: 'relative', marginBottom: 28 }}>
            <div style={{
              position: 'absolute', left: 13.5, top: -22, height: 22,
              width: 1, background: 'var(--border)',
            }} />
            <GS_StepHeader n={3} label="Point e2a at your agent" status="todo">
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 14 }}>
                {[
                  { mode: 'Webhook', desc: 'POST to your HTTPS endpoint', mono: 'POST https://…' },
                  { mode: 'WebSocket', desc: 'Push to a long-lived connection', mono: 'wss://api.e2a.dev/v1/ws' },
                ].map((m, i) => (
                  <div key={m.mode} style={{
                    background: 'var(--bg-panel)',
                    border: i === 0 ? '1.5px solid var(--fg)' : '1px solid var(--border)',
                    borderRadius: 'var(--r-md)', padding: 14,
                  }}>
                    <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)', marginBottom: 2 }}>{m.mode}</div>
                    <div style={{ fontSize: 12, color: 'var(--fg-muted)', marginBottom: 8 }}>{m.desc}</div>
                    <div style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)' }}>{m.mono}</div>
                  </div>
                ))}
              </div>
              <InkConsole
                title="terminal · agent.dev"
                lang="bash"
                lines={[
                  { c: 'comment', text: '# install the CLI' },
                  { c: 'prompt',  text: '$ npm i -g @e2a/cli' },
                  { c: 'comment', text: '# subscribe to your agent, forward to localhost:3000' },
                  { node: (
                    <div>
                      <span style={{ color: 'var(--machine)' }}>$ </span>
                      <span style={{ color: 'var(--ink-fg)' }}>e2a listen </span>
                      <span style={{ color: 'var(--spectral)' }}>--forward </span>
                      <span style={{ color: 'var(--spectral)' }}>http://localhost:3000/inbox</span>
                    </div>
                  )},
                  { c: 'comment', text: '// 200 OK · subscribed to agent_k3p9a2 · waiting for mail…' },
                ]}
              />
            </GS_StepHeader>
          </div>

          {/* CTA */}
          <div style={{
            display: 'flex', alignItems: 'center', justifyContent: 'space-between',
            paddingTop: 18, borderTop: '1px solid var(--border)',
          }}>
            <div style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)', letterSpacing: '0.02em' }}>
              We'll email you the moment DNS verifies · jamie@acme.io
            </div>
            <div style={{ display: 'flex', gap: 8 }}>
              <button style={loftBtnGhost}>Save & finish later</button>
              <button style={{ ...loftBtnPrimary, opacity: 0.55, cursor: 'not-allowed' }}>
                Continue <span style={{ fontFamily: 'var(--f-mono)' }}>→</span>
              </button>
            </div>
          </div>
        </div>
      </main>
    </div>
  );
}

window.LoftGetStarted = LoftGetStarted;
