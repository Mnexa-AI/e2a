/**
 * TypeScript contract-test runner for scenarios.yaml.
 *
 * Runs against a live test server. Requires env vars:
 *   E2A_TEST_BASE_URL  — test server URL (e.g. http://localhost:8080)
 *   E2A_TEST_API_KEY   — valid API key for the test user
 *
 * The runner drives the server over raw HTTP (a thin scenario interpreter,
 * not the ergonomic client) plus {@link WSListener} for WebSocket steps.
 *
 * Setup steps that require direct store access (inject_message,
 * verify_domain as setup) cause the scenario to be skipped.
 *
 * NOTE: scenario `path`s are repointed from `/api/v1` to `/v1` as part of the
 * cross-language scenarios.yaml migration (tracked separately); this runner is
 * gated behind live-server env vars and is not part of the unit build.
 */
import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve as pathResolve } from "node:path";
import { parse as yamlParse } from "yaml";
import { WSListener } from "../../src/v1/ws.js";

// Minimal raw-HTTP driver — the scenario runner needs a generic
// request(method, path, body) shim, not the ergonomic client surface.
class RawApiError extends Error {
  constructor(public readonly statusCode: number, message: string) {
    super(message);
    this.name = "RawApiError";
  }
}

class RawApi {
  constructor(
    private readonly apiKey: string,
    private readonly baseUrl: string,
  ) {}

  async raw(method: string, path: string, body?: unknown): Promise<Response> {
    const headers: Record<string, string> = { Authorization: `Bearer ${this.apiKey}` };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const resp = await fetch(`${this.baseUrl}${path}`, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    if (resp.status >= 400) throw new RawApiError(resp.status, await resp.text());
    return resp;
  }

  async registerDomain(input: { domain: string }): Promise<void> {
    await this.raw("POST", "/v1/domains", input);
  }

  async registerAgent(input: { email: string; agent_mode: string }): Promise<void> {
    await this.raw("POST", "/v1/agents", { address: input.email, agent_mode: input.agent_mode });
  }
}

// ── YAML schema ─────────────────────────────────────────────────

interface Scenario {
  name: string;
  description: string;
  auth_override?: string;
  setup?: SetupStep[];
  steps: Step[];
}

interface SetupStep {
  register_domain?: string;
  verify_domain?: string;
  register_agent?: { email: string; agent_mode: string };
  inject_message?: { agent_email: string; from: string; subject: string };
}

interface Step {
  id: string;
  action: string;
  method?: string;
  path?: string;
  body?: Record<string, unknown>;
  auth_override?: string;
  agent_email?: string;
  from?: string;
  subject?: string;
  verify_domain?: string;
  expect?: Expectation;
}

interface Expectation {
  status?: number;
  body_contains?: string[];
  body_excludes?: string[];
  body_match?: Record<string, unknown>;
  fields_present?: string[];
  fields_absent?: string[];
  field_match?: Record<string, unknown>;
}

// ── Helpers ─────────────────────────────────────────────────────

function loadScenarios(): Scenario[] {
  const yamlPath = pathResolve(
    import.meta.dirname ?? ".",
    "../../../../tests/contract/scenarios.yaml",
  );
  const raw = readFileSync(yamlPath, "utf-8");
  const parsed = yamlParse(raw);
  return parsed.scenarios;
}

/** JSON-path evaluator: "agents[0].email", "agents.length" */
function jsonPathGet(obj: Record<string, unknown>, path: string): unknown {
  const parts = path.split(".");
  let current: unknown = obj;

  for (const part of parts) {
    if (part === "length") {
      return Array.isArray(current) ? current.length : undefined;
    }
    const bracketIdx = part.indexOf("[");
    if (bracketIdx !== -1) {
      const name = part.slice(0, bracketIdx);
      const arrIdx = parseInt(part.slice(bracketIdx + 1, -1), 10);
      const map = current as Record<string, unknown>;
      const arr = map?.[name];
      if (!Array.isArray(arr) || arrIdx >= arr.length) return undefined;
      current = arr[arrIdx];
    } else {
      const map = current as Record<string, unknown>;
      if (map == null || typeof map !== "object") return undefined;
      current = map[part];
    }
  }
  return current;
}

/** Cross-type value comparison (JSON number vs YAML int). */
function valuesEqual(jsonVal: unknown, yamlVal: unknown): boolean {
  if (typeof yamlVal === "number" && typeof jsonVal === "number") {
    return jsonVal === yamlVal;
  }
  if (typeof yamlVal === "boolean") return jsonVal === yamlVal;
  if (typeof yamlVal === "string") return jsonVal === yamlVal;
  return String(jsonVal) === String(yamlVal);
}

/** Extract agent email from a WS path like /api/v1/agents/bot@ws.test.dev/ws */
function extractEmailFromWSPath(path: string): string {
  const parts = path.replace(/^\/+/, "").split("/");
  for (let i = 0; i < parts.length; i++) {
    if (parts[i] === "agents" && i + 1 < parts.length) return parts[i + 1];
  }
  return "";
}

// ── Runner ──────────────────────────────────────────────────────

class Runner {
  private vars: Record<string, string> = {};
  private api: RawApi;
  private wsListener: WSListener | null = null;
  /** Buffer of notifications received from WSListener. */
  private wsMessages: string[] = [];

  constructor(
    private readonly baseUrl: string,
    private readonly apiKey: string,
    private readonly scenario: Scenario,
  ) {
    this.api = new RawApi(apiKey, baseUrl);
  }

  resolve(s: string): string {
    s = s.replaceAll("{base_url}", this.baseUrl);
    s = s.replaceAll("{api_key}", this.apiKey);
    for (const [k, v] of Object.entries(this.vars)) {
      s = s.replaceAll(`{${k}}`, v);
    }
    return s;
  }

  resolveValue(v: unknown): unknown {
    if (typeof v === "string") return this.resolve(v);
    if (Array.isArray(v)) return v.map((item) => this.resolveValue(item));
    if (v && typeof v === "object") {
      const out: Record<string, unknown> = {};
      for (const [k, val] of Object.entries(v)) {
        out[k] = this.resolveValue(val);
      }
      return out;
    }
    return v;
  }

  /** Returns the auth override for a step, or undefined for default SDK auth. */
  private authOverride(step: Step): string | undefined {
    return step.auth_override ?? this.scenario.auth_override;
  }

  /** Whether a step uses non-default auth (needs raw fetch instead of SDK). */
  private hasAuthOverride(step: Step): boolean {
    return this.authOverride(step) !== undefined;
  }

  /** Returns true if setup requires store access (scenario should be skipped). */
  async executeSetup(): Promise<boolean> {
    if (!this.scenario.setup) return false;

    for (const s of this.scenario.setup) {
      if (s.inject_message) return true;
      if (s.verify_domain) return true;

      if (s.register_domain) {
        try {
          await this.api.registerDomain({ domain: this.resolve(s.register_domain) });
        } catch (err) {
          if (err instanceof RawApiError && err.statusCode === 409) {
            /* already exists */
          } else {
            throw err;
          }
        }
      }

      if (s.register_agent) {
        const email = this.resolve(s.register_agent.email);
        try {
          await this.api.registerAgent({
            email,
            agent_mode: s.register_agent.agent_mode,
          });
        } catch (err) {
          if (err instanceof RawApiError && err.statusCode === 409) {
            /* already exists */
          } else {
            throw err;
          }
        }
        this.vars["agent_email"] = email;
      }
    }
    return false;
  }

  async executeSteps(): Promise<void> {
    for (const step of this.scenario.steps) {
      switch (step.action) {
        case "request":
          await this.execRequest(step);
          break;
        case "inject_message":
          throw new Error(`step ${step.id}: inject_message not supported in TS runner`);
        case "ws_connect":
          await this.execWSConnect(step);
          break;
        case "ws_reconnect":
          await this.execWSReconnect(step);
          break;
        case "ws_read":
          await this.execWSRead(step);
          break;
        case "verify_and_retry":
          throw new Error(`step ${step.id}: verify_and_retry not supported in TS runner`);
        default:
          throw new Error(`step ${step.id}: unknown action ${step.action}`);
      }
    }
  }

  cleanup(): void {
    if (this.wsListener) {
      this.wsListener.close();
      this.wsListener = null;
    }
  }

  // ── Step executors ──────────────────────────────────────────

  private async execRequest(step: Step): Promise<void> {
    const path = this.resolve(step.path!);
    const body = step.body !== undefined ? this.resolveValue(step.body) : undefined;
    const ex = step.expect;

    let status: number;
    let rawBody: string;

    if (this.hasAuthOverride(step)) {
      // Auth-override scenarios bypass the SDK's auth layer by design.
      const override = this.authOverride(step)!;
      const headers: Record<string, string> = {};
      if (override !== "none") headers["Authorization"] = this.resolve(override);
      if (body !== undefined) headers["Content-Type"] = "application/json";

      const resp = await fetch(`${this.baseUrl}${path}`, {
        method: step.method,
        headers,
        body: body !== undefined ? JSON.stringify(body) : undefined,
      });
      status = resp.status;
      rawBody = await resp.text();
    } else {
      // Happy path — route through the raw HTTP driver.
      try {
        const resp = await this.api.raw(step.method!, path, body);
        status = resp.status;
        rawBody = await resp.text();
      } catch (err) {
        if (err instanceof RawApiError) {
          // SDK threw on a non-2xx status — verify it matches expectation.
          if (ex?.status) {
            expect(err.statusCode, `step ${step.id}: status`).toBe(ex.status);
          }
          return;
        }
        throw err;
      }
    }

    if (!ex) return;

    if (ex.status) {
      expect(status, `step ${step.id}: status`).toBe(ex.status);
    }

    if (!ex.body_contains?.length && !ex.body_match && !ex.body_excludes?.length) return;

    const json = JSON.parse(rawBody) as Record<string, unknown>;

    for (const key of ex.body_contains ?? []) {
      const resolved = this.resolve(key);
      expect(json, `step ${step.id}: body_contains ${resolved}`).toHaveProperty(resolved);
    }
    for (const key of ex.body_excludes ?? []) {
      const resolved = this.resolve(key);
      expect(json, `step ${step.id}: body_excludes ${resolved}`).not.toHaveProperty(resolved);
    }
    if (ex.body_match) {
      for (const [jsonPath, expected] of Object.entries(ex.body_match)) {
        const resolvedPath = this.resolve(jsonPath);
        const actual = jsonPathGet(json, resolvedPath);
        const resolvedExpected = this.resolveValue(expected);
        expect(
          valuesEqual(actual, resolvedExpected),
          `step ${step.id}: body_match ${resolvedPath} = ${JSON.stringify(actual)}, want ${JSON.stringify(resolvedExpected)}`,
        ).toBe(true);
      }
    }
  }

  private async execWSConnect(step: Step): Promise<void> {
    const path = this.resolve(step.path!);
    const email = extractEmailFromWSPath(path);

    this.wsMessages = [];
    const listener = new WSListener({
      apiKey: this.apiKey,
      agentEmail: email,
      baseUrl: this.baseUrl,
      reconnect: false,
    });

    listener.on("notification", (notif) => {
      this.wsMessages.push(JSON.stringify(notif));
    });

    await new Promise<void>((resolve, reject) => {
      listener.on("open", resolve);
      listener.on("error", reject);
      listener.connect();
    });

    this.wsListener = listener;
  }

  private async execWSReconnect(step: Step): Promise<void> {
    if (this.wsListener) {
      this.wsListener.close();
      this.wsListener = null;
      // Brief pause for server to process disconnect
      await new Promise((r) => setTimeout(r, 100));
    }
    await this.execWSConnect(step);
  }

  private async execWSRead(step: Step): Promise<void> {
    if (!this.wsListener) throw new Error(`step ${step.id}: no WS connection`);

    // Wait for a buffered or incoming notification.
    let raw: string;
    if (this.wsMessages.length > 0) {
      raw = this.wsMessages.shift()!;
    } else {
      raw = await new Promise<string>((resolve, reject) => {
        const timeout = setTimeout(
          () => reject(new Error(`step ${step.id}: ws_read timeout`)),
          5000,
        );
        const handler = (notif: unknown) => {
          clearTimeout(timeout);
          resolve(JSON.stringify(notif));
        };
        this.wsListener!.once("notification", handler);
      });
    }

    const notif = JSON.parse(raw) as Record<string, unknown>;
    const ex = step.expect;
    if (!ex) return;

    for (const field of ex.fields_present ?? []) {
      const resolved = this.resolve(field);
      expect(notif, `step ${step.id}: fields_present ${resolved}`).toHaveProperty(resolved);
    }
    for (const field of ex.fields_absent ?? []) {
      const resolved = this.resolve(field);
      expect(notif, `step ${step.id}: fields_absent ${resolved}`).not.toHaveProperty(resolved);
    }
    if (ex.field_match) {
      for (const [key, expected] of Object.entries(ex.field_match)) {
        const resolvedKey = this.resolve(key);
        const resolvedExpected = this.resolveValue(expected);
        expect(
          valuesEqual(notif[resolvedKey], resolvedExpected),
          `step ${step.id}: field_match ${resolvedKey} = ${JSON.stringify(notif[resolvedKey])}, want ${JSON.stringify(resolvedExpected)}`,
        ).toBe(true);
      }
    }
  }
}

// ── Scenarios that require store-level setup ────────────────────

const STORE_DEPENDENT_ACTIONS = new Set(["inject_message", "verify_and_retry"]);

function scenarioNeedsStore(sc: Scenario): boolean {
  if (sc.setup?.some((s) => s.inject_message || s.verify_domain)) return true;
  if (sc.steps.some((s) => STORE_DEPENDENT_ACTIONS.has(s.action))) return true;
  return false;
}

// ── Test entry point ────────────────────────────────────────────

const baseUrl = process.env.E2A_TEST_BASE_URL;
const apiKey = process.env.E2A_TEST_API_KEY;

describe.skipIf(!baseUrl || !apiKey)("Contract scenarios", () => {
  const scenarios = loadScenarios();

  for (const sc of scenarios) {
    const needsStore = scenarioNeedsStore(sc);

    (needsStore ? it.skip : it)(sc.name, async () => {
      const runner = new Runner(baseUrl!, apiKey!, sc);
      try {
        const skipped = await runner.executeSetup();
        if (skipped) return;
        await runner.executeSteps();
      } finally {
        runner.cleanup();
      }
    });
  }
});
