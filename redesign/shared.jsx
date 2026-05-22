// shared.jsx — sidebar + chip + button primitives shared by the new pages.
// Existing dashboard.jsx / pending.jsx have their own local copies — these
// helpers are for the additional artboards (Get started, API keys, Domains,
// Settings). Exposed on window so any artboard can pull them in.

const NAV_ITEMS = [
  { id: 'get-started', label: 'Get started', icon: 'plus' },
  { id: 'agents',      label: 'Agents',      icon: 'grid' },
  { id: 'pending',     label: 'Pending',     icon: 'clock', badge: 2 },
  { id: 'domains',     label: 'Domains',     icon: 'globe' },
  { id: 'api-keys',    label: 'API keys',    icon: 'key'  },
];

const NAV_SVG = {
  plus:     <><circle cx="12" cy="12" r="9.5" /><path d="M12 8v8M8 12h8" /></>,
  grid:     <><rect x="3.5" y="3.5" width="7" height="7" rx="1.5" /><rect x="13.5" y="3.5" width="7" height="7" rx="1.5" /><rect x="3.5" y="13.5" width="7" height="7" rx="1.5" /><rect x="13.5" y="13.5" width="7" height="7" rx="1.5" /></>,
  clock:    <><circle cx="12" cy="12" r="9.5" /><polyline points="12 6.5 12 12 16 14" /></>,
  globe:    <><circle cx="12" cy="12" r="9.5" /><path d="M3 12h18" /><path d="M12 3a16 16 0 010 18 16 16 0 010-18z" /></>,
  key:      <><circle cx="8" cy="14" r="4" /><path d="M11 11l9-9M15 6l3 3M18 3l3 3" /></>,
  settings: <><circle cx="12" cy="12" r="2.5" /><path d="M19 12a7 7 0 00-.1-1.3l2-1.6-2-3.5-2.4 1a7 7 0 00-2.2-1.3l-.3-2.6h-4l-.4 2.6a7 7 0 00-2.2 1.3l-2.4-1-2 3.5 2 1.6A7 7 0 005 12c0 .4 0 .9.1 1.3l-2 1.6 2 3.5 2.4-1a7 7 0 002.2 1.3l.4 2.6h4l.3-2.6a7 7 0 002.2-1.3l2.4 1 2-3.5-2-1.6c.1-.4.1-.9.1-1.3z" /></>,
  msg:      <path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z" />,
};

function LoftSidebar({ active = 'agents' }) {
  return (
    <aside style={{
      width: 248, flexShrink: 0,
      background: 'var(--bg-panel)',
      borderRight: '1px solid var(--border)',
      display: 'flex', flexDirection: 'column',
      height: '100%',
    }}>
      {/* Logo */}
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
        {NAV_ITEMS.map(it => {
          const isActive = it.id === active;
          return (
            <a key={it.id} href="#" style={{
              display: 'flex', alignItems: 'center', gap: 11,
              padding: '8px 12px', borderRadius: 'var(--r-md)',
              fontSize: 13, fontFamily: 'var(--f-ui)', fontWeight: isActive ? 500 : 400,
              color: isActive ? 'var(--fg)' : 'var(--fg-muted)',
              background: isActive ? 'var(--bg-elev)' : 'transparent',
              boxShadow: isActive ? 'inset 2px 0 0 var(--accent)' : 'none',
              textDecoration: 'none', position: 'relative',
              marginBottom: 1,
            }}>
              <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                {NAV_SVG[it.icon]}
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
          );
        })}
      </nav>

      {/* Bottom */}
      <div style={{ padding: '10px 12px 14px', borderTop: '1px solid var(--border)' }}>
        <a href="#" style={{
          display: 'flex', alignItems: 'center', gap: 11,
          padding: '8px 12px', borderRadius: 'var(--r-md)',
          fontSize: 13,
          color: active === 'settings' ? 'var(--fg)' : 'var(--fg-muted)',
          background: active === 'settings' ? 'var(--bg-elev)' : 'transparent',
          boxShadow: active === 'settings' ? 'inset 2px 0 0 var(--accent)' : 'none',
          fontWeight: active === 'settings' ? 500 : 400,
          textDecoration: 'none',
        }}>
          <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden>{NAV_SVG.settings}</svg>
          Settings
        </a>
        <a href="#" style={{
          display: 'flex', alignItems: 'center', gap: 11,
          padding: '8px 12px', borderRadius: 'var(--r-md)',
          fontSize: 13, color: 'var(--fg-muted)', textDecoration: 'none',
        }}>
          <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden>{NAV_SVG.msg}</svg>
          Send feedback
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

function LoftTopbar({ crumbs = [], right = null }) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', justifyContent: 'space-between',
      padding: '14px 28px',
      borderBottom: '1px solid var(--border)',
      background: 'var(--bg-panel)',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: 12, color: 'var(--fg-muted)' }}>
        {crumbs.map((c, i) => (
          <React.Fragment key={i}>
            {i > 0 && <span>/</span>}
            <span style={{ color: i === crumbs.length - 1 ? 'var(--fg)' : 'var(--fg-muted)' }}>{c}</span>
          </React.Fragment>
        ))}
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        {right || (
          <div style={{
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
        )}
      </div>
    </div>
  );
}

function LoftChip({ children, tone = 'neutral', mono = false }) {
  const t = {
    success: { bg: 'var(--success-bg)', fg: 'var(--success)' },
    warn:    { bg: 'var(--warn-bg)',    fg: 'var(--warn-strong)' },
    info:    { bg: 'var(--info-bg)',    fg: 'var(--info-strong)' },
    accent:  { bg: 'var(--accent-soft)',fg: 'var(--accent-strong)' },
    danger:  { bg: 'var(--danger-bg)',  fg: 'var(--danger-strong)' },
    neutral: { bg: 'var(--bg-elev)',    fg: 'var(--fg-muted)' },
  }[tone];
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 5,
      padding: '2px 8px', borderRadius: 999,
      background: t.bg, color: t.fg,
      fontFamily: mono ? 'var(--f-mono)' : 'var(--f-ui)',
      fontSize: 11, fontWeight: 600, letterSpacing: mono ? '0.02em' : 0,
    }}>{children}</span>
  );
}

function LoftDot({ tone = 'success' }) {
  const c = { success: 'var(--success)', warn: 'var(--warn)', accent: 'var(--accent)', danger: 'var(--danger)', neutral: 'var(--fg-subtle)' }[tone];
  return <span style={{ width: 7, height: 7, borderRadius: '50%', background: c, display: 'inline-block' }} />;
}

function LoftEyebrow({ children }) {
  return (
    <span style={{
      fontFamily: 'var(--f-mono)', fontSize: 11, fontWeight: 600,
      color: 'var(--accent-strong)', letterSpacing: '0.08em',
      textTransform: 'uppercase',
    }}>{children}</span>
  );
}

const loftBtnGhost = {
  fontFamily: 'var(--f-ui)', fontSize: 12, fontWeight: 500,
  padding: '7px 12px', borderRadius: 'var(--r-md)',
  background: 'var(--bg-panel)', color: 'var(--fg)',
  border: '1px solid var(--border)', cursor: 'pointer',
};

const loftBtnPrimary = {
  fontFamily: 'var(--f-ui)', fontSize: 13, fontWeight: 500,
  padding: '8px 16px', borderRadius: 'var(--r-md)',
  background: 'var(--accent-fill)', color: '#fff',
  border: 'none', cursor: 'pointer',
  display: 'inline-flex', alignItems: 'center', gap: 6,
};

const loftBtnMono = {
  fontFamily: 'var(--f-mono)', fontSize: 11, fontWeight: 500,
  padding: '4px 8px', borderRadius: 'var(--r-sm)',
  background: 'var(--ink-elev)', color: 'var(--ink-fg-muted)',
  border: '1px solid var(--ink-border)', cursor: 'pointer',
  display: 'inline-flex', alignItems: 'center', gap: 5,
};

// ─────────────────────────────────────────────────────────────
// Ink Console — the agent-native code block.
// Lines are an array of { c: 'comment'|'prompt'|'plain', text, fg? }
// or React fragments via `node`.
// ─────────────────────────────────────────────────────────────
function InkConsole({ title, lang, lines, copy = true, height }) {
  return (
    <div style={{
      background: 'var(--ink)',
      border: '1px solid var(--ink-border)',
      borderRadius: 'var(--r-lg)',
      overflow: 'hidden',
      fontFamily: 'var(--f-mono)',
      height,
    }}>
      {(title || lang || copy) && (
        <div style={{
          display: 'flex', alignItems: 'center',
          padding: '8px 12px 8px 14px',
          borderBottom: '1px solid var(--ink-border)',
          background: 'var(--ink-elev)',
          fontSize: 11, color: 'var(--ink-fg-muted)',
          letterSpacing: '0.02em',
        }}>
          {title && <span style={{ color: 'var(--ink-fg)', fontWeight: 500 }}>{title}</span>}
          {lang && (
            <span style={{
              marginLeft: title ? 10 : 0,
              fontFamily: 'var(--f-mono)', fontSize: 10,
              color: 'var(--spectral)',
              textTransform: 'uppercase', letterSpacing: '0.1em',
            }}>{lang}</span>
          )}
          <span style={{ flex: 1 }} />
          {copy && (
            <button style={loftBtnMono}>
              <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden>
                <rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V5a2 2 0 012-2h10" />
              </svg>
              copy
            </button>
          )}
        </div>
      )}
      <div style={{ padding: '14px 16px', fontSize: 12.5, lineHeight: 1.6 }}>
        {lines.map((l, i) => {
          if (l.node) return <div key={i}>{l.node}</div>;
          let c = 'var(--ink-fg)';
          if (l.c === 'comment') c = 'var(--ink-fg-muted)';
          if (l.c === 'prompt')  c = 'var(--machine)';
          if (l.c === 'string')  c = 'var(--spectral)';
          if (l.c === 'accent')  c = 'var(--accent)';
          if (l.fg) c = l.fg;
          return <div key={i} style={{ color: c, whiteSpace: 'pre-wrap' }}>{l.text}</div>;
        })}
      </div>
    </div>
  );
}

Object.assign(window, {
  LoftSidebar, LoftTopbar, LoftChip, LoftDot, LoftEyebrow,
  loftBtnGhost, loftBtnPrimary, loftBtnMono, InkConsole,
});
