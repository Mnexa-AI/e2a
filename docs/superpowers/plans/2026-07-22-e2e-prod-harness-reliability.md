# Production E2E Harness Reliability Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Remove false production failures caused by host selection, account capacity, retired MCP names, suppressed sink recipients, and destructive stress probes.

**Architecture:** Keep the black-box suites behavior-focused. Centralize target derivation in the harness environment, allow `ApiClient` to address the hosted web origin, make resource cleanup test-scoped where capacity matters, and gate cooldown-inducing probes behind the existing stress flag.

**Tech Stack:** TypeScript, Node test runner, deployed HTTP API/MCP endpoints.

---

### Task 1: Add target and sink configuration regressions

- [ ] Add unit tests for API-to-site origin derivation and sink fallback.
- [ ] Run the tests and observe the expected missing-export failures.
- [ ] Add `siteUrl` and `sinkEmail` to `ProdEnv`, plus pure resolver functions.
- [ ] Allow `ApiClient` to accept a base URL override.
- [ ] Point billing web-route probes at `siteUrl` while keeping `/v1/account` on `apiUrl`.
- [ ] Run the focused unit tests.

### Task 2: Make production suites capacity-safe and current

- [ ] Add regression tests for agent-capacity calculation.
- [ ] Track every successful concurrent create before assertions and skip probes that exceed current account headroom.
- [ ] Clean created agents between capacity-sensitive tests.
- [ ] Gate the active registration-rate probe behind `E2E_PROD_STRESS=1`.
- [ ] Exercise canonical MCP tools (`send_message`, `approve_review`, `reject_review`) and separately assert supported legacy aliases.
- [ ] Use the configured internal sink in send probes.
- [ ] Run focused harness tests and TypeScript checking; record any unchanged baseline failures.

### Task 3: Correct event-log guidance and verify

- [ ] Replace environment-specific event-log assumptions with capability-based suite comments.
- [ ] Update the operations production note to reflect that the current production event log is enabled.
- [ ] Run relevant unit/type checks and inspect the diff.
- [ ] Request independent implementation and adversarial reviews.
- [ ] Commit, push, and open pull requests without merging.
