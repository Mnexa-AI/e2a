import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    include: ["src/**/*.test.ts"],
    coverage: {
      provider: "v8",
      include: ["src/**/*.ts"],
      exclude: ["src/**/*.test.ts"],
      // Floors sit a few points under current coverage (lines 77.5,
      // statements 77.3, functions 77.9, branches 72.2). Ratchet up, never down.
      thresholds: {
        lines: 73,
        statements: 73,
        functions: 73,
        branches: 68,
      },
    },
  },
});
