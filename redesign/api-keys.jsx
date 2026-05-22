// api-keys.jsx — API keys page, Loft-styled.
// The single most "Loft" page in e2a: keys live on ink. Cyan strings,
// chartreuse prompts, mono key prefixes. 1280 × 920.

function AK_KeyRow({ name, prefix, created, lastUsed, scopes, expired }) {
  return (
    <div style={{
      display: 'grid',
      gridTemplateColumns: '1.4fr 1.6fr 1fr 1fr 1fr 100px',
      gap: 14, alignItems: 'center',
      padding: '14px 18px',
      borderTop: '1px solid var(--border-sub)',
    }}>
      <div>
        <div style={{ fontSize: 13, fontWeight: 500, color: 'var(--fg)' }}>{name}</div>
        <div style={{ fontFamily: 'var(--f-mono)', fontSize: 10, color: 'var(--fg-subtle)', marginTop: 2, letterSpacing: '0.02em' }}>
          key_{prefix.split('_')[1]?.slice(0, 6) || 'xxx'}…
        </div>
      </div>

      {/* Key prefix on ink */}
      <div style={{
        display: 'inline-flex', alignItems: 'center', gap: 8,
        padding: '5px 10px',
        background: 'var(--ink)',
        border: '1px solid var(--ink-border)',
        borderRadius: 'var(--r-sm)',
        fontFamily: 'var(--f-mono)', fontSize: 11,
        width: 'fit-content',
      }}>
        <span style={{ color: 'var(--machine)' }}>ad_live_</span>
        <span style={{ color: 'var(--ink-fg)' }}>{prefix.replace('ad_live_', '').slice(0, 8)}</span>
        <span style={{ color: 'var(--ink-fg-muted)' }}>…{prefix.slice(-4)}</span>
        <span style={{ width: 1, height: 12, background: 'var(--ink-border)' }} />
        <button style={{
          background: 'transparent', border: 'none', cursor: 'pointer',
          color: 'var(--ink-fg-muted)', padding: 0, display: 'flex',
        }}>
          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden>
            <rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V5a2 2 0 012-2h10" />
          </svg>
        </button>
      </div>

      <div style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-muted)' }}>{created}</div>
      <div style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-muted)' }}>{lastUsed}</div>
      <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
        {scopes.map(s => <LoftChip key={s} tone={s === 'admin' ? 'accent' : 'neutral'} mono>{s}</LoftChip>)}
      </div>
      <div style={{ display: 'flex', gap: 4, justifyContent: 'flex-end' }}>
        {expired
          ? <LoftChip tone="danger">Expired</LoftChip>
          : <button style={{ ...loftBtnGhost, padding: '4px 10px', fontSize: 11, color: 'var(--danger-strong)' }}>Revoke</button>
        }
      </div>
    </div>
  );
}

function LoftAPIKeys() {
  const keys = [
    { name: 'CI · contract tests',  prefix: 'ad_live_x8nz4q1rk2a8p3vb',  created: '2026-04-12', lastUsed: '3s ago',  scopes: ['send', 'inbox'] },
    { name: 'Production · main',    prefix: 'ad_live_9k2mn4f7tqv1z8wb',  created: '2026-04-02', lastUsed: '47s ago', scopes: ['admin'] },
    { name: 'Local dev · jamie',    prefix: 'ad_live_b3rt8h2nq9pa1xkv',  created: '2026-04-18', lastUsed: '4m ago',  scopes: ['send', 'inbox'] },
    { name: 'Old triage runner',    prefix: 'ad_live_n7p1v4kz3qf8mt2c',  created: '2026-02-04', lastUsed: 'never',   scopes: ['send'], expired: true },
  ];

  return (
    <div style={{
      width: 1280, height: 920,
      background: 'var(--bg)', color: 'var(--fg)',
      fontFamily: 'var(--f-ui)', display: 'flex', overflow: 'hidden',
    }}>
      <LoftSidebar active="api-keys" />

      <main style={{ flex: 1, overflow: 'auto', display: 'flex', flexDirection: 'column' }}>
        <LoftTopbar crumbs={['Acme Robotics', 'API keys']} />

        <div style={{ padding: '28px 32px 32px' }}>
          {/* Header */}
          <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 24, marginBottom: 24 }}>
            <div>
              <LoftEyebrow>Developer · ad_live_</LoftEyebrow>
              <h1 style={{
                fontFamily: 'var(--f-ui)', fontSize: 26, fontWeight: 700,
                letterSpacing: '-0.012em', margin: '8px 0 6px', lineHeight: 1.15,
              }}>API keys</h1>
              <p style={{ fontSize: 13, color: 'var(--fg-muted)', margin: 0, maxWidth: 620, lineHeight: 1.6 }}>
                Authenticate the agents you build when they send mail, reply, or subscribe over WebSocket.
                One key works across all your agents. Keys never appear twice — copy at creation.
              </p>
            </div>
            <button style={{ ...loftBtnPrimary, padding: '8px 16px' }}>
              <span style={{ fontFamily: 'var(--f-mono)' }}>+</span> Create key
            </button>
          </div>

          {/* Just-created banner */}
          <div style={{
            background: 'var(--ink)',
            border: '1px solid var(--ink-border)',
            borderRadius: 'var(--r-lg)',
            padding: 18,
            marginBottom: 24,
            position: 'relative',
          }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                <span style={{
                  width: 8, height: 8, borderRadius: '50%',
                  background: 'var(--machine)',
                  boxShadow: '0 0 0 4px rgba(182,243,110,0.18)',
                }} />
                <span style={{ color: 'var(--ink-fg)', fontSize: 13, fontWeight: 500 }}>
                  Key created · copy it now, it won't be shown again
                </span>
              </div>
              <button style={{
                background: 'transparent', border: 'none',
                color: 'var(--ink-fg-muted)', fontSize: 11, cursor: 'pointer',
                fontFamily: 'var(--f-mono)',
              }}>dismiss</button>
            </div>
            <div style={{
              background: 'var(--ink-elev)',
              border: '1px solid var(--ink-border)',
              borderRadius: 'var(--r-md)',
              padding: '12px 14px',
              display: 'flex', alignItems: 'center', gap: 10,
              fontFamily: 'var(--f-mono)', fontSize: 13,
            }}>
              <span style={{ color: 'var(--machine)' }}>ad_live_</span>
              <span style={{ color: 'var(--ink-fg)', flex: 1, letterSpacing: '0.02em' }}>
                9f4cN3a1bM7zK2pQwLxVcRhBjT5sUyFa
              </span>
              <button style={loftBtnMono}>
                <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden>
                  <rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V5a2 2 0 012-2h10" />
                </svg>
                copy key
              </button>
            </div>
            <div style={{ marginTop: 10, fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--ink-fg-muted)' }}>
              // sha256:9f4c…3a1b · created 4s ago · scopes: send, inbox · expires never
            </div>
          </div>

          {/* Keys table */}
          <div style={{
            background: 'var(--bg-panel)',
            border: '1px solid var(--border)',
            borderRadius: 'var(--r-lg)',
            overflow: 'hidden',
            marginBottom: 24,
          }}>
            <div style={{
              display: 'flex', alignItems: 'center', justifyContent: 'space-between',
              padding: '14px 18px',
              background: 'var(--bg-elev)',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--fg)' }}>4 keys</span>
                <span style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)' }}>· 1 expired · 3 active</span>
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span style={{ fontFamily: 'var(--f-mono)', fontSize: 11, color: 'var(--fg-subtle)' }}>
                  Sort: <span style={{ color: 'var(--fg-muted)' }}>last used ▾</span>
                </span>
              </div>
            </div>

            <div style={{
              display: 'grid',
              gridTemplateColumns: '1.4fr 1.6fr 1fr 1fr 1fr 100px',
              gap: 14, padding: '10px 18px',
              background: 'var(--bg-elev)',
              fontFamily: 'var(--f-mono)', fontSize: 10, fontWeight: 600,
              color: 'var(--fg-subtle)', letterSpacing: '0.08em', textTransform: 'uppercase',
              borderBottom: '1px solid var(--border-sub)',
            }}>
              <span>Name</span><span>Prefix</span><span>Created</span><span>Last used</span><span>Scopes</span><span></span>
            </div>

            {keys.map(k => <AK_KeyRow key={k.prefix} {...k} />)}
          </div>

          {/* Two-up: usage example + security */}
          <div style={{ display: 'grid', gridTemplateColumns: '1.4fr 1fr', gap: 16 }}>
            <InkConsole
              title="Send mail via API"
              lang="curl"
              lines={[
                { c: 'comment', text: '# POST a message to an agent' },
                { node: (
                  <div>
                    <span style={{ color: 'var(--machine)' }}>$ </span>
                    <span style={{ color: 'var(--ink-fg)' }}>curl https://api.e2a.dev/v1/messages \</span>
                  </div>
                )},
                { node: (
                  <div style={{ paddingLeft: 16 }}>
                    <span style={{ color: 'var(--ink-fg)' }}>-H </span>
                    <span style={{ color: 'var(--spectral)' }}>"Authorization: Bearer </span>
                    <span style={{ color: 'var(--machine)' }}>ad_live_</span>
                    <span style={{ color: 'var(--spectral)' }}>9f4c…3a1b"</span>
                    <span style={{ color: 'var(--ink-fg)' }}> \</span>
                  </div>
                )},
                { node: (
                  <div style={{ paddingLeft: 16 }}>
                    <span style={{ color: 'var(--ink-fg)' }}>-H </span>
                    <span style={{ color: 'var(--spectral)' }}>"Content-Type: application/json"</span>
                    <span style={{ color: 'var(--ink-fg)' }}> \</span>
                  </div>
                )},
                { node: (
                  <div style={{ paddingLeft: 16 }}>
                    <span style={{ color: 'var(--ink-fg)' }}>-d </span>
                    <span style={{ color: 'var(--spectral)' }}>{'\'{"to":"triage@acme.io","subject":"hi","body":"hello"}\''}</span>
                  </div>
                )},
                { c: 'comment', text: '// 201 Created · msg_8nlqv · 412ms · sha256:c7b1…9f3a' },
              ]}
            />

            <div style={{
              background: 'var(--bg-panel)',
              border: '1px solid var(--border)',
              borderRadius: 'var(--r-lg)',
              padding: 18,
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--accent-strong)" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                  <rect x="4" y="10" width="16" height="11" rx="2" /><path d="M8 10V7a4 4 0 018 0v3" />
                </svg>
                <span style={{ fontSize: 13, fontWeight: 600 }}>Key hygiene</span>
              </div>
              <ul style={{
                margin: 0, padding: 0, listStyle: 'none',
                fontSize: 12, color: 'var(--fg-muted)', lineHeight: 1.75,
              }}>
                <li style={{ display: 'flex', gap: 8 }}>
                  <LoftDot tone="success" />
                  <span>Keys are hashed at rest — we can never recover one for you.</span>
                </li>
                <li style={{ display: 'flex', gap: 8 }}>
                  <LoftDot tone="success" />
                  <span>Rotate quarterly · keep one key per environment.</span>
                </li>
                <li style={{ display: 'flex', gap: 8 }}>
                  <LoftDot tone="warn" />
                  <span>Avoid committing keys to git. Use a secret manager.</span>
                </li>
                <li style={{ display: 'flex', gap: 8 }}>
                  <LoftDot tone="accent" />
                  <span>Scope: <code style={{ fontFamily: 'var(--f-mono)', fontSize: 11 }}>send</code> + <code style={{ fontFamily: 'var(--f-mono)', fontSize: 11 }}>inbox</code> for runtime; <code style={{ fontFamily: 'var(--f-mono)', fontSize: 11 }}>admin</code> for tooling only.</span>
                </li>
              </ul>
            </div>
          </div>
        </div>
      </main>
    </div>
  );
}

window.LoftAPIKeys = LoftAPIKeys;
