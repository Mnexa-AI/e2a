// Runs before any modules are imported (via jest.config setupFiles).
// Tests assume the deployment exposes a shared agent domain — set the
// public env var here so lib/site picks it up at module evaluation time.
process.env.NEXT_PUBLIC_AGENTS_DOMAIN = "agents.e2a.dev";
