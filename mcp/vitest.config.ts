import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    coverage: {
      provider: "v8",
      include: ["src/**/*.ts"],
      // Floors sit a few points under current coverage (lines 85.9,
      // statements 84.6, functions 82.1, branches 73.1). Ratchet up, never down.
      thresholds: {
        lines: 81,
        statements: 80,
        functions: 78,
        branches: 69,
      },
    },
  },
});
