import { defineConfig } from "vitest/config";

// Separate config for the live binary-spawn parity harness (test/**), kept out of
// the default `vitest run` (src/** unit tests). Skips itself when staging creds
// are absent, so it's safe to invoke unconditionally in the conformance gate.
export default defineConfig({
  test: {
    include: ["test/**/*.test.ts"],
    testTimeout: 45_000,
  },
});
