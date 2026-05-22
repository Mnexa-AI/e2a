// domains.jsx — Domains page, Loft-styled.
// List of verified + pending domains, with DNS record drawer expanded for one.
// 1280 × 920.

function DM_DnsCheckRow({ type, name, value, ok }) {
  return (
    <div style={{
      display: 'grid',
      gridTemplateColumns: '24px 60px 1.2fr 2fr 70px',
      gap: 12, alignItems: 'center',
      padding: '10px 14px',
      borderTop: '1px solid var(--border-sub)',
      fontFamily: 'var(--f-mono)', fontSize: 12,
    }}>
      <span>
        {ok ? (
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--success)" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"><polyline points="20 6 9 17 4 12" /></svg>
        ) : (
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--warn-strong)" strokeWidth="2" strokeLinecap="round"><circle cx="12" cy="12" r="9.5" /><path d="M12 7v6M12 16h.01" /></svg>
        )}
      </span>
      <span style={{ color: 'var(--accent-strong)', fontWeight: 600 }}>{type}</span>
      <span style={{ color: 'var(--fg)' }}>{name}</span>
      <span style={{
        color: 'var(--fg-muted)',
        whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
      }}>{value}</span>
      <div style={{ textAlign: 'right' }}>
        {ok
          ? <LoftChip tone="success" mono>found</LoftChip>
          : <LoftChip tone="warn" mono>missing</LoftChip>
        }
      </div>
    </div>
  );
}

function DM_DomainCard({ d, expanded }) {
  return (
    <div style={{
      background: 'var(--bg-panel)',
      border: expanded ? '1.5px solid var(--fg)' : '1px solid var(--border)',
      borderRadius: 'var(--r-lg)',
      overflow: 'hidden',
    }}>
      {/* Row */}
      <div style={{
        display: 'flex', alignItems: 'center',
        padding: '16px 20px', gap: 18,
      }}>
        {/* globe icon */}
        <div style={{
          width: 36, height: 36, borderRadius: 8,
          background: d.verified ? 'var(--accent-soft)' : 'var(--bg-elev)',
          color: d.verified ? 'var(--accent-strong)' : 'var(--fg-muted)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          flexShrink: 0,
        }}>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="12" cy="12" r="9.5" /><path d="M3 12h18" /><path d="M12 3a16 16 0 010 18 16 16 0 010-18z" />
          </svg>
        </div>

        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 4 }}>
            <span style={{ fontFamily: 'var(--f-mono)', fontSize: 15, fontWeight: 600, color: 'var(--fg)', letterSpacing: '-0.01em' }}>
              {d.name}
            </span>
            {d.verified
              ? <LoftChip tone="success"><LoftDot tone="success" /> Verified</LoftChip>
              : <LoftChip tone="warn"><LoftDot tone="warn" /> {d.status}</LoftChip>
            }
            {d.primary && <LoftChip tone="accent">Primary</LoftChip>}
          </div>
          <div style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)', letterSpacing: '0.02em' }}>
            dom_{d.id} · added {d.added} · {d.agentCount} {d.agentCount === 1 ? 'agent' : 'agents'} · MX&nbsp;
            <span style={{ color: d.verified ? 'var(--success)' : 'var(--warn-strong)' }}>
              {d.verified ? 'reachable' : 'unreachable'}
            </span>
          </div>
        </div>

        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          {!d.verified && (
            <button style={{ ...loftBtnPrimary, padding: '6px 12px', fontSize: 12 }}>
              Re-check DNS
            </button>
          )}
          <button style={loftBtnGhost}>{expanded ? 'Hide' : 'View'} records</button>
          <button style={{ ...loftBtnGhost, padding: '7px 8px' }}>⋯</button>
        </div>
      </div>

      {/* Expanded DNS records */}
      {expanded && (
        <div style={{ borderTop: '1px solid var(--border)' }}>
          <div style={{
            display: 'flex', alignItems: 'center', justifyContent: 'space-between',
            padding: '12px 20px',
            background: 'var(--bg-elev)',
          }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              <LoftEyebrow>DNS verification</LoftEyebrow>
              <span style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)' }}>
                last checked 14s ago · re-checks every 30s
              </span>
            </div>
            <div style={{ display: 'flex', gap: 6 }}>
              <button style={loftBtnGhost}>Copy all</button>
            </div>
          </div>
          <div style={{
            display: 'grid',
            gridTemplateColumns: '24px 60px 1.2fr 2fr 70px',
            gap: 12, padding: '8px 14px',
            background: 'var(--bg-elev)',
            fontFamily: 'var(--f-mono)', fontSize: 10, fontWeight: 600,
            color: 'var(--fg-subtle)', letterSpacing: '0.08em', textTransform: 'uppercase',
            borderBottom: '1px solid var(--border-sub)',
          }}>
            <span></span><span>Type</span><span>Name</span><span>Value</span><span style={{ textAlign: 'right' }}>Status</span>
          </div>
          <DM_DnsCheckRow type="MX"  name="@"              value="10 mx.e2a.dev" ok />
          <DM_DnsCheckRow type="TXT" name="@"              value="v=spf1 include:_spf.e2a.dev ~all" ok />
          <DM_DnsCheckRow type="TXT" name="e2a._domainkey" value="v=DKIM1; k=rsa; p=MIGfMA0GCSqGSIb3DQEBAQ…" ok={false} />
        </div>
      )}
    </div>
  );
}

function LoftDomains() {
  const domains = [
    { id: 'k3p9a2', name: 'acme.io',         verified: true,  primary: true,  added: 'Apr 02', agentCount: 3 },
    { id: 'q1m4nx', name: 'agents.acme.io',  verified: true,  primary: false, added: 'Apr 18', agentCount: 1 },
    { id: 'r0vn9k', name: 'support.acme.io', verified: false, primary: false, added: 'May 14', agentCount: 0, status: 'DKIM missing' },
  ];

  return (
    <div style={{
      width: 1280, height: 920,
      background: 'var(--bg)', color: 'var(--fg)',
      fontFamily: 'var(--f-ui)', display: 'flex', overflow: 'hidden',
    }}>
      <LoftSidebar active="domains" />

      <main style={{ flex: 1, overflow: 'auto', display: 'flex', flexDirection: 'column' }}>
        <LoftTopbar crumbs={['Acme Robotics', 'Domains']} />

        <div style={{ padding: '28px 32px 32px' }}>
          {/* Header */}
          <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 24, marginBottom: 22 }}>
            <div>
              <LoftEyebrow>Identity · SPF · DKIM</LoftEyebrow>
              <h1 style={{
                fontFamily: 'var(--f-ui)', fontSize: 26, fontWeight: 700,
                letterSpacing: '-0.012em', margin: '8px 0 6px', lineHeight: 1.15,
              }}>Domains</h1>
              <p style={{ fontSize: 13, color: 'var(--fg-muted)', margin: 0, maxWidth: 620, lineHeight: 1.6 }}>
                Domains your agents send and receive on. e2a checks SPF + DKIM on every inbound message
                and signs every outbound one.
              </p>
            </div>
            <button style={{ ...loftBtnPrimary, padding: '8px 16px' }}>
              <span style={{ fontFamily: 'var(--f-mono)' }}>+</span> Add domain
            </button>
          </div>

          {/* Stats */}
          <div style={{
            display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 12, marginBottom: 22,
          }}>
            {[
              ['Verified', '2', 'all checks passing', 'success'],
              ['Pending', '1', 'DKIM missing', 'warn'],
              ['Inbound · 7d', '4,184', '+12% w/w', 'info'],
              ['SPF/DKIM pass rate', '99.8%', 'last 7d', 'neutral'],
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
                  fontSize: 26, fontWeight: 600, color: 'var(--fg)',
                  letterSpacing: '-0.02em', lineHeight: 1, marginBottom: 6,
                }}>{val}</div>
                <div style={{
                  fontSize: 11,
                  color: tone === 'warn' ? 'var(--warn-strong)' : tone === 'success' ? 'var(--success)' : 'var(--fg-muted)',
                }}>{sub}</div>
              </div>
            ))}
          </div>

          {/* List */}
          <div style={{ display: 'grid', gridTemplateColumns: '1fr', gap: 12 }}>
            <DM_DomainCard d={domains[0]} expanded={false} />
            <DM_DomainCard d={domains[1]} expanded={false} />
            <DM_DomainCard d={domains[2]} expanded />
          </div>

          {/* Footer note */}
          <div style={{
            marginTop: 22,
            padding: '14px 18px',
            background: 'var(--bg-panel)',
            border: '1px dashed var(--border)',
            borderRadius: 'var(--r-md)',
            display: 'flex', alignItems: 'center', gap: 14,
          }}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--fg-muted)" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
              <circle cx="12" cy="12" r="9.5" /><path d="M12 8v4M12 16h.01" />
            </svg>
            <div style={{ flex: 1, fontSize: 12, color: 'var(--fg-muted)', lineHeight: 1.5 }}>
              <span style={{ color: 'var(--fg)', fontWeight: 500 }}>Don't want to set up DNS?</span>{' '}
              Use the shared domain <code style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--accent-strong)' }}>agents.e2a.dev</code>{' '}
              and skip ahead — perfect for prototypes.
            </div>
            <a href="#" style={{ fontSize: 12, color: 'var(--accent-strong)', fontWeight: 500 }}>
              Use shared domain →
            </a>
          </div>
        </div>
      </main>
    </div>
  );
}

window.LoftDomains = LoftDomains;
