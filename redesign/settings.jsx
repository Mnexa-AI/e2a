// settings.jsx — Settings page, Loft-styled.
// Billing is intentionally out. Sections shown: Profile (read), Organization
// (read), Usage (no plan caps — just the numbers we already track),
// Webhook signing secrets (full CRUD exists in backend), Notifications
// (stub), Danger zone (DELETE /api/v1/users/me). 1280 × 980.

function ST_SectionNav() {
  const items = [
    { id: 'profile',       label: 'Profile' },
    { id: 'org',           label: 'Organization' },
    { id: 'usage',         label: 'Usage', active: true },
    { id: 'webhooks',      label: 'Webhook signing' },
    { id: 'notifications', label: 'Notifications' },
    { id: 'danger',        label: 'Danger zone', tone: 'danger' },
  ];
  return (
    <nav style={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      {items.map(it => (
        <a key={it.id} href="#" style={{
          padding: '8px 12px',
          fontSize: 13, textDecoration: 'none',
          color: it.active ? 'var(--fg)' : (it.tone === 'danger' ? 'var(--danger-strong)' : 'var(--fg-muted)'),
          background: it.active ? 'var(--bg-elev)' : 'transparent',
          borderRadius: 'var(--r-md)',
          boxShadow: it.active ? 'inset 2px 0 0 var(--accent)' : 'none',
          fontWeight: it.active ? 500 : 400,
        }}>{it.label}</a>
      ))}
    </nav>
  );
}

function ST_KV({ k, v, mono = false, kid = false }) {
  return (
    <div style={{
      display: 'grid', gridTemplateColumns: '180px 1fr',
      padding: '12px 0',
      borderTop: '1px solid var(--border-sub)',
      alignItems: 'center', gap: 16,
    }}>
      <div style={{
        fontFamily: 'var(--f-mono)', fontSize: 11,
        color: 'var(--fg-subtle)', letterSpacing: '0.04em',
      }}>{k}</div>
      <div style={{
        fontFamily: mono ? 'var(--f-mono)' : 'var(--f-ui)',
        fontSize: kid ? 11 : 13,
        color: 'var(--fg)',
      }}>{v}</div>
    </div>
  );
}

function ST_UsageBar({ label, used, unit, sub }) {
  // No plan caps — just show the count with a sparkline-ish bar from
  // peak-this-month. Keeps the visual rhythm without lying about limits.
  return (
    <div>
      <div style={{
        display: 'flex', alignItems: 'baseline', justifyContent: 'space-between',
        marginBottom: 6,
      }}>
        <span style={{ fontSize: 12, fontWeight: 500, color: 'var(--fg)' }}>{label}</span>
        <span style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)' }}>{sub}</span>
      </div>
      <div style={{
        fontFamily: "'Instrument Serif', Georgia, serif",
        fontSize: 32, fontWeight: 400, color: 'var(--fg)',
        letterSpacing: '-0.012em', lineHeight: 1, marginBottom: 8,
      }}>
        {used.toLocaleString()}
        <span style={{
          fontFamily: 'var(--f-mono)', fontSize: 11,
          color: 'var(--fg-subtle)', marginLeft: 6,
          letterSpacing: '0.04em',
        }}>{unit}</span>
      </div>
      {/* Mini bar: 30 days, latest on right */}
      <div style={{ display: 'flex', alignItems: 'flex-end', gap: 2, height: 18 }}>
        {Array.from({ length: 30 }).map((_, i) => {
          const h = 4 + Math.abs(Math.sin(i * 0.7 + label.length)) * 14;
          return (
            <div key={i} style={{
              flex: 1, height: h,
              background: i === 29 ? 'var(--accent-fill)' : 'var(--bg-sunken)',
              borderRadius: 1.5,
            }} />
          );
        })}
      </div>
      <div style={{ marginTop: 5, fontFamily: 'var(--f-mono)', fontSize: 10, color: 'var(--fg-subtle)' }}>
        last 30 days · resets monthly
      </div>
    </div>
  );
}

function ST_SecretRow({ name, prefix, created, lastSigned, current }) {
  return (
    <div style={{
      display: 'grid',
      gridTemplateColumns: '1.4fr 1.6fr 1fr 1fr 90px',
      gap: 14, alignItems: 'center',
      padding: '12px 18px',
      borderTop: '1px solid var(--border-sub)',
    }}>
      <div>
        <div style={{ fontSize: 13, fontWeight: 500, color: 'var(--fg)', display: 'flex', alignItems: 'center', gap: 8 }}>
          {name}
          {current && <LoftChip tone="accent" mono>current</LoftChip>}
        </div>
      </div>
      <div style={{
        display: 'inline-flex', alignItems: 'center', gap: 8,
        padding: '4px 10px',
        background: 'var(--ink)',
        border: '1px solid var(--ink-border)',
        borderRadius: 'var(--r-sm)',
        fontFamily: 'var(--f-mono)', fontSize: 11,
        width: 'fit-content',
      }}>
        <span style={{ color: 'var(--machine)' }}>whsec_</span>
        <span style={{ color: 'var(--ink-fg)' }}>{prefix.slice(0, 8)}</span>
        <span style={{ color: 'var(--ink-fg-muted)' }}>…{prefix.slice(-4)}</span>
      </div>
      <div style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-muted)' }}>{created}</div>
      <div style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-muted)' }}>{lastSigned}</div>
      <div style={{ textAlign: 'right' }}>
        <button style={{ ...loftBtnGhost, padding: '4px 10px', fontSize: 11, color: 'var(--danger-strong)' }}>Revoke</button>
      </div>
    </div>
  );
}

function LoftSettings() {
  return (
    <div style={{
      width: 1280, height: 980,
      background: 'var(--bg)', color: 'var(--fg)',
      fontFamily: 'var(--f-ui)', display: 'flex', overflow: 'hidden',
    }}>
      <LoftSidebar active="settings" />

      <main style={{ flex: 1, overflow: 'auto', display: 'flex', flexDirection: 'column' }}>
        <LoftTopbar crumbs={['Acme Robotics', 'Settings', 'Usage']} />

        <div style={{
          display: 'grid',
          gridTemplateColumns: '220px 1fr',
          gap: 32, padding: '28px 32px 32px', flex: 1, minHeight: 0,
        }}>
          {/* Section nav */}
          <div>
            <LoftEyebrow>Settings</LoftEyebrow>
            <div style={{ marginTop: 14 }}>
              <ST_SectionNav />
            </div>
          </div>

          {/* Right column */}
          <div style={{ maxWidth: 760 }}>
            {/* Header */}
            <div style={{ marginBottom: 22 }}>
              <h1 style={{
                fontFamily: 'var(--f-ui)', fontSize: 22, fontWeight: 700,
                letterSpacing: '-0.012em', margin: '0 0 6px', lineHeight: 1.2,
              }}>Usage</h1>
              <p style={{ fontSize: 13, color: 'var(--fg-muted)', margin: 0, lineHeight: 1.6 }}>
                What your agents have moved through e2a this month. Counters reset on the 1st at 00:00 UTC.
              </p>
            </div>

            {/* Usage card */}
            <div style={{
              background: 'var(--bg-panel)',
              border: '1px solid var(--border)',
              borderRadius: 'var(--r-lg)',
              padding: 22,
              marginBottom: 18,
            }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 18 }}>
                <LoftEyebrow>This month · May 2026</LoftEyebrow>
                <a href="#" style={{ fontSize: 12, color: 'var(--accent-strong)' }}>Export CSV →</a>
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 32 }}>
                <ST_UsageBar label="Inbound mail"    used={18432} unit="msgs"     sub="+12% vs Apr" />
                <ST_UsageBar label="Outbound mail"   used={9847}  unit="msgs"     sub="+4% vs Apr"  />
                <ST_UsageBar label="Stored messages" used={7800}  unit="archived" sub="30-day TTL"  />
              </div>
            </div>

            {/* Organization card */}
            <div style={{
              background: 'var(--bg-panel)',
              border: '1px solid var(--border)',
              borderRadius: 'var(--r-lg)',
              padding: '6px 22px 16px',
              marginBottom: 18,
            }}>
              <div style={{ padding: '14px 0 4px', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--fg)' }}>Workspace</span>
                <button style={{ ...loftBtnGhost, padding: '6px 12px' }}>Edit name</button>
              </div>
              <ST_KV k="NAME"    v="Acme Robotics" />
              <ST_KV k="USER ID" v="usr_8nlqv6kp2qx9" mono kid />
              <ST_KV k="EMAIL"   v="jamie@acme.io" mono kid />
              <ST_KV k="CREATED" v="2026-04-02 14:32:11 UTC" mono kid />
            </div>

            {/* Webhook signing card */}
            <div style={{
              background: 'var(--bg-panel)',
              border: '1px solid var(--border)',
              borderRadius: 'var(--r-lg)',
              overflow: 'hidden',
              marginBottom: 18,
            }}>
              <div style={{
                display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                padding: '16px 22px',
              }}>
                <div>
                  <div style={{ fontSize: 14, fontWeight: 600, color: 'var(--fg)' }}>Webhook signing</div>
                  <div style={{ fontSize: 12, color: 'var(--fg-muted)', marginTop: 2, maxWidth: 480, lineHeight: 1.55 }}>
                    Secrets we use to HMAC-sign every webhook your agents receive.{' '}
                    Rotate by creating a new secret — both keep signing until you revoke the old one.
                  </div>
                </div>
                <button style={{ ...loftBtnPrimary, padding: '7px 14px', fontSize: 12 }}>
                  <span style={{ fontFamily: 'var(--f-mono)' }}>+</span> New secret
                </button>
              </div>
              <div style={{
                display: 'grid',
                gridTemplateColumns: '1.4fr 1.6fr 1fr 1fr 90px',
                gap: 14, padding: '10px 18px',
                background: 'var(--bg-elev)',
                fontFamily: 'var(--f-mono)', fontSize: 10, fontWeight: 600,
                color: 'var(--fg-subtle)', letterSpacing: '0.08em', textTransform: 'uppercase',
                borderBottom: '1px solid var(--border-sub)',
                borderTop: '1px solid var(--border-sub)',
              }}>
                <span>Name</span><span>Prefix</span><span>Created</span><span>Last signed</span><span></span>
              </div>
              <ST_SecretRow name="default" prefix="3kQpVnL9bH4rT8mZxJaWcRf2NyBgUdCe" created="2026-04-02" lastSigned="14s ago"  current />
              <ST_SecretRow name="rotation · Apr-23" prefix="9fT4aB7nP1qZ8kHcMxWvLrJ3yEgUdNaB" created="2026-04-23" lastSigned="2d ago" />
            </div>

            {/* Notifications card (stub — no backend yet) */}
            <div style={{
              background: 'var(--bg-panel)',
              border: '1px solid var(--border)',
              borderRadius: 'var(--r-lg)',
              padding: '6px 22px 16px',
              marginBottom: 18,
            }}>
              <div style={{
                padding: '14px 0 4px',
                display: 'flex', alignItems: 'center', justifyContent: 'space-between',
              }}>
                <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--fg)' }}>Notifications</span>
                <LoftChip tone="neutral">Coming soon</LoftChip>
              </div>
              {[
                ['DNS verified', 'When a domain you added finishes verifying'],
                ['HITL pending', 'When an agent has a message awaiting review'],
                ['Webhook failing', 'When your webhook returns ≥ 3 errors in a row'],
              ].map(([n, d]) => (
                <div key={n} style={{
                  display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                  padding: '12px 0',
                  borderTop: '1px solid var(--border-sub)',
                  opacity: 0.6,
                }}>
                  <div>
                    <div style={{ fontSize: 13, fontWeight: 500, color: 'var(--fg)' }}>{n}</div>
                    <div style={{ fontSize: 11, color: 'var(--fg-muted)', marginTop: 2 }}>{d}</div>
                  </div>
                  <div style={{
                    width: 32, height: 18, borderRadius: 999,
                    background: 'var(--bg-sunken)', position: 'relative',
                  }}>
                    <div style={{
                      position: 'absolute', top: 2, left: 2,
                      width: 14, height: 14, borderRadius: '50%',
                      background: 'var(--bg-panel)',
                      boxShadow: 'var(--sh-1)',
                    }} />
                  </div>
                </div>
              ))}
            </div>

            {/* Danger zone */}
            <div style={{
              background: 'var(--bg-panel)',
              border: '1px solid var(--danger)',
              borderRadius: 'var(--r-lg)',
              padding: 22,
              marginBottom: 18,
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                <span style={{
                  fontFamily: 'var(--f-mono)', fontSize: 11, fontWeight: 600,
                  color: 'var(--danger-strong)', letterSpacing: '0.08em',
                  textTransform: 'uppercase',
                }}>Danger zone</span>
              </div>
              <div style={{
                display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                padding: '14px 0 4px',
              }}>
                <div>
                  <div style={{ fontSize: 13, fontWeight: 500, color: 'var(--fg)' }}>Export all data</div>
                  <div style={{ fontSize: 12, color: 'var(--fg-muted)', marginTop: 2 }}>
                    Agents, domains, messages, API keys — JSON archive.
                  </div>
                </div>
                <button style={loftBtnGhost}>Request export</button>
              </div>
              <div style={{
                display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                padding: '14px 0 4px',
                borderTop: '1px solid var(--border-sub)',
              }}>
                <div>
                  <div style={{ fontSize: 13, fontWeight: 500, color: 'var(--fg)' }}>Delete account</div>
                  <div style={{ fontSize: 12, color: 'var(--fg-muted)', marginTop: 2 }}>
                    Removes all agents, domains, keys, and message history. Irreversible.
                  </div>
                </div>
                <button style={{
                  ...loftBtnGhost,
                  borderColor: 'var(--danger)',
                  color: 'var(--danger-strong)',
                  fontWeight: 500,
                }}>Delete account</button>
              </div>
            </div>

            {/* Footnote */}
            <div style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)', letterSpacing: '0.02em' }}>
              // questions? mail{' '}
              <a href="#" style={{ color: 'var(--accent-strong)' }}>support@e2a.dev</a>
              {' '}— we usually reply in &lt; 4h on weekdays.
            </div>
          </div>
        </div>
      </main>
    </div>
  );
}

window.LoftSettings = LoftSettings;
