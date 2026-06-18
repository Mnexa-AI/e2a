"""Python contract-test runner for the shared scenarios.yaml.

Runs against a live test server. Requires env vars:
  E2A_TEST_BASE_URL  — test server URL (e.g. http://localhost:8080)
  E2A_TEST_API_KEY   — valid API key for the test user

The runner drives the server over raw HTTP (a thin scenario interpreter, not
the ergonomic client):
- Each request step issues a raw bearer-authed httpx request to step.path
- Auth-override scenarios send their own Authorization header (by design)
- WS scenarios skipped in this sync runner (async WS tested separately)

Setup steps requiring direct store access (inject_message, verify_domain as
setup) cause the scenario to be skipped with a clear reason.
"""

from __future__ import annotations

import json as json_mod
import os
import re
from pathlib import Path
from typing import Any

import httpx
import pytest
import yaml

# NOTE: the runner drives the server over raw HTTP (a thin scenario interpreter,
# not the ergonomic client). scenario `path`s are repointed from /api/v1 to /v1
# as part of the cross-language scenarios.yaml migration (tracked separately);
# this runner is gated behind live-server env vars and not part of unit CI.

# ── Config ────────────────────────────────────────────────────────

BASE_URL = os.environ.get("E2A_TEST_BASE_URL", "")
API_KEY = os.environ.get("E2A_TEST_API_KEY", "")

# tests/test_contract.py -> sdks/python/tests/ -> sdks/python/ -> sdks/ -> repo root
SCENARIOS_PATH = Path(__file__).resolve().parents[3] / "tests" / "contract" / "scenarios.yaml"

# ── Helpers ───────────────────────────────────────────────────────


def load_scenarios() -> list[dict[str, Any]]:
    with open(SCENARIOS_PATH) as f:
        data = yaml.safe_load(f)
    return data["scenarios"]


def json_path_get(obj: Any, path: str) -> Any:
    """Evaluate a simple JSON path like 'agents[0].email' or 'agents.length'."""
    parts = path.split(".")
    current = obj
    for part in parts:
        if part == "length":
            return len(current) if isinstance(current, list) else None
        m = re.match(r"^(.+)\[(\d+)\]$", part)
        if m:
            name, idx = m.group(1), int(m.group(2))
            arr = current.get(name) if isinstance(current, dict) else None
            if not isinstance(arr, list) or idx >= len(arr):
                return None
            current = arr[idx]
        else:
            if not isinstance(current, dict):
                return None
            current = current.get(part)
    return current


def values_equal(json_val: Any, yaml_val: Any) -> bool:
    """Cross-type comparison (JSON number vs YAML int, bool, string)."""
    if isinstance(yaml_val, bool):
        return json_val is yaml_val or json_val == yaml_val
    if isinstance(yaml_val, (int, float)) and isinstance(json_val, (int, float)):
        return json_val == yaml_val
    return str(json_val) == str(yaml_val)


# ── Scenario determination ────────────────────────────────────────

STORE_ACTIONS = {"inject_message", "verify_and_retry"}


def scenario_needs_store(sc: dict[str, Any]) -> bool:
    setup = sc.get("setup") or []
    if any("inject_message" in s or "verify_domain" in s for s in setup):
        return True
    steps = sc.get("steps") or []
    if any(s.get("action") in STORE_ACTIONS for s in steps):
        return True
    return False


# ── Runner ────────────────────────────────────────────────────────


class Runner:
    def __init__(self, base_url: str, api_key: str, scenario: dict[str, Any]):
        self.base_url = base_url
        self.api_key = api_key
        self.scenario = scenario
        self.vars: dict[str, str] = {}
        self._http = httpx.Client(base_url=base_url, timeout=30)

    def close(self):
        self._http.close()

    def _raw(self, method: str, path: str, body: Any = None) -> httpx.Response:
        headers = {"Authorization": f"Bearer {self.api_key}"}
        if body is not None:
            headers["Content-Type"] = "application/json"
        return self._http.request(method, path, headers=headers, json=body)

    def resolve(self, s: str) -> str:
        s = s.replace("{base_url}", self.base_url)
        s = s.replace("{api_key}", self.api_key)
        for k, v in self.vars.items():
            s = s.replace(f"{{{k}}}", v)
        return s

    def resolve_value(self, v: Any) -> Any:
        if isinstance(v, str):
            return self.resolve(v)
        if isinstance(v, list):
            return [self.resolve_value(item) for item in v]
        if isinstance(v, dict):
            return {k: self.resolve_value(val) for k, val in v.items()}
        return v

    def auth_override(self, step: dict[str, Any]) -> str | None:
        return step.get("auth_override") or self.scenario.get("auth_override")

    def has_auth_override(self, step: dict[str, Any]) -> bool:
        return self.auth_override(step) is not None

    def execute_setup(self) -> bool:
        """Returns True if scenario should be skipped (needs store access)."""
        setup = self.scenario.get("setup") or []
        for s in setup:
            if "inject_message" in s or "verify_domain" in s:
                return True

            if "register_domain" in s:
                domain = self.resolve(s["register_domain"])
                resp = self._raw("POST", "/v1/domains", {"domain": domain})
                if resp.status_code >= 400 and resp.status_code != 409:
                    resp.raise_for_status()

            if "register_agent" in s:
                agent = s["register_agent"]
                email = self.resolve(agent["email"])
                resp = self._raw(
                    "POST", "/v1/agents", {"address": email, "agent_mode": agent.get("agent_mode")}
                )
                if resp.status_code >= 400 and resp.status_code != 409:
                    resp.raise_for_status()
                self.vars["agent_email"] = email

        return False

    def execute_steps(self):
        for step in self.scenario.get("steps", []):
            action = step["action"]
            if action == "request":
                self._exec_request(step)
            elif action == "inject_message":
                pytest.skip(f"step {step['id']}: inject_message not supported in Python runner")
            elif action in ("ws_connect", "ws_reconnect", "ws_read"):
                pytest.skip(f"step {step['id']}: WS actions require async runner")
            elif action == "verify_and_retry":
                pytest.skip(f"step {step['id']}: verify_and_retry not supported in Python runner")
            else:
                raise ValueError(f"step {step['id']}: unknown action {action}")

    def _exec_request(self, step: dict[str, Any]):
        path = self.resolve(step["path"])
        body = self.resolve_value(step["body"]) if "body" in step else None
        ex = step.get("expect") or {}

        if self.has_auth_override(step):
            # Auth-override scenarios bypass SDK auth by design
            override = self.auth_override(step)
            headers: dict[str, str] = {}
            if override != "none":
                headers["Authorization"] = self.resolve(override)
            if body is not None:
                headers["Content-Type"] = "application/json"

            resp = self._http.request(
                step["method"], path,
                headers=headers,
                json=body if body is not None else None,
            )
            status = resp.status_code
            data = None
            raw_body = resp.text
        else:
            # Default auth — raw bearer request.
            resp = self._raw(step["method"], path, body)
            status = resp.status_code
            raw_body = resp.text
            data = None

        if "status" in ex:
            assert status == ex["status"], f"step {step['id']}: expected {ex['status']}, got {status}"

        if not any(k in ex for k in ("body_contains", "body_excludes", "body_match")):
            return

        if data is None:
            data = json_mod.loads(raw_body)

        for key in ex.get("body_contains", []):
            resolved = self.resolve(key)
            assert resolved in data, f"step {step['id']}: body_contains {resolved}"

        for key in ex.get("body_excludes", []):
            resolved = self.resolve(key)
            assert resolved not in data, f"step {step['id']}: body_excludes {resolved}"

        if "body_match" in ex:
            for json_path, expected in ex["body_match"].items():
                resolved_path = self.resolve(json_path)
                actual = json_path_get(data, resolved_path)
                resolved_expected = self.resolve_value(expected)
                assert values_equal(actual, resolved_expected), (
                    f"step {step['id']}: body_match {resolved_path} = {actual!r}, want {resolved_expected!r}"
                )


# ── Test entry point ──────────────────────────────────────────────


pytestmark = pytest.mark.skipif(
    not BASE_URL or not API_KEY,
    reason="E2A_TEST_BASE_URL and E2A_TEST_API_KEY required for contract tests",
)


def _scenario_ids():
    if not SCENARIOS_PATH.exists():
        return []
    return [sc["name"] for sc in load_scenarios()]


def _scenario_by_name(name: str) -> dict[str, Any]:
    for sc in load_scenarios():
        if sc["name"] == name:
            return sc
    raise ValueError(f"scenario {name!r} not found")


@pytest.fixture(params=_scenario_ids() if SCENARIOS_PATH.exists() else [])
def scenario(request):
    return _scenario_by_name(request.param)


def test_contract_scenario(scenario):
    if scenario_needs_store(scenario):
        pytest.skip(f"scenario {scenario['name']}: requires store access (inject_message/verify_domain)")

    runner = Runner(BASE_URL, API_KEY, scenario)
    try:
        skipped = runner.execute_setup()
        if skipped:
            pytest.skip(f"scenario {scenario['name']}: setup requires store access")
        runner.execute_steps()
    finally:
        runner.close()
