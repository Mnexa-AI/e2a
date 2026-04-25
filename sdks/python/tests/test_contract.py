"""Python contract-test runner for the shared scenarios.yaml.

Runs against a live test server. Requires env vars:
  E2A_TEST_BASE_URL  — test server URL (e.g. http://localhost:8080)
  E2A_TEST_API_KEY   — valid API key for the test user

The runner routes scenario steps through the SDK's public API:
- Agent/domain CRUD via E2AApi methods
- Message listing/fetching via E2AApi methods
- Auth-override scenarios via a bare httpx.Client (by design, to bypass SDK auth)
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
from urllib.parse import quote

import httpx
import pytest
import yaml

from e2a.v1.api import E2AApi, E2AApiError
from e2a.v1.generated import RegisterAgentRequest, RegisterDomainRequest

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


# ── SDK method routing ────────────────────────────────────────────

# Maps (method, path_pattern) to an SDK method name and how to extract args.
# This lets us route scenario steps through SDK public methods instead of raw HTTP.


def _route_to_sdk(api: E2AApi, method: str, path: str, body: dict | None, resolve_fn) -> tuple[int, dict]:
    """Try to route a request through SDK public methods.

    Returns (status_code, response_body_dict).
    Raises E2AApiError on HTTP errors (which the caller can inspect).
    """
    # POST /api/v1/agents
    if method == "POST" and path == "/api/v1/agents":
        result = api.register_agent(RegisterAgentRequest(**(body or {})))
        return 201, result.model_dump(by_alias=True, exclude_none=True)

    # GET /api/v1/agents
    if method == "GET" and path == "/api/v1/agents":
        result = api.list_agents()
        return 200, result.model_dump(by_alias=True, exclude_none=True)

    # GET /api/v1/agents/{email}
    m = re.match(r"^/api/v1/agents/([^/]+)$", path)
    if m and method == "GET":
        result = api.get_agent(m.group(1))
        return 200, result.model_dump(by_alias=True, exclude_none=True)

    # DELETE /api/v1/agents/{email}
    if m and method == "DELETE":
        api.delete_agent(m.group(1))
        return 200, {}

    # POST /api/v1/domains
    if method == "POST" and path == "/api/v1/domains":
        result = api.register_domain(RegisterDomainRequest(**(body or {})))
        return 201, result.model_dump(by_alias=True, exclude_none=True)

    # GET /api/v1/domains
    if method == "GET" and path == "/api/v1/domains":
        result = api.list_domains()
        return 200, result.model_dump(by_alias=True, exclude_none=True)

    # POST /api/v1/domains/{domain}/verify
    m = re.match(r"^/api/v1/domains/([^/]+)/verify$", path)
    if m and method == "POST":
        result = api.verify_domain(m.group(1))
        return 200, result.model_dump(by_alias=True, exclude_none=True)

    # DELETE /api/v1/domains/{domain}
    m = re.match(r"^/api/v1/domains/([^/]+)$", path)
    if m and method == "DELETE":
        api.delete_domain(m.group(1))
        return 204, {}

    # GET /api/v1/agents/{email}/messages?...
    m = re.match(r"^/api/v1/agents/([^/]+)/messages$", path.split("?")[0])
    if m and method == "GET":
        # Parse query params from path
        import urllib.parse
        parsed = urllib.parse.urlparse(path)
        params = urllib.parse.parse_qs(parsed.query)
        result = api.list_messages(
            m.group(1),
            status=params.get("status", ["unread"])[0],
            page_size=int(params.get("page_size", ["50"])[0]),
            token=params.get("token", [None])[0],
        )
        return 200, result.model_dump(by_alias=True, exclude_none=True)

    # GET /api/v1/agents/{email}/messages/{id}
    m = re.match(r"^/api/v1/agents/([^/]+)/messages/([^/]+)$", path)
    if m and method == "GET":
        result = api.get_message(m.group(1), m.group(2))
        return 200, result.model_dump(by_alias=True, exclude_none=True)

    # POST /api/v1/send
    if method == "POST" and path == "/api/v1/send":
        from e2a.v1.generated import SendEmailRequest
        result = api.send_email(SendEmailRequest(**(body or {})))
        return 200, result.model_dump(by_alias=True, exclude_none=True)

    # Fallback: unrecognized path — cannot route through SDK
    return None, None  # type: ignore


# ── Runner ────────────────────────────────────────────────────────


class Runner:
    def __init__(self, base_url: str, api_key: str, scenario: dict[str, Any]):
        self.base_url = base_url
        self.api_key = api_key
        self.scenario = scenario
        self.vars: dict[str, str] = {}
        self.api = E2AApi(api_key=api_key, base_url=base_url)
        # Bare HTTP client for auth-override and legacy-path scenarios only
        self._http = httpx.Client(base_url=base_url, timeout=30)

    def close(self):
        self.api.close()
        self._http.close()

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
                try:
                    self.api.register_domain(RegisterDomainRequest(domain=domain))
                except E2AApiError as e:
                    if e.status_code != 409:
                        raise

            if "register_agent" in s:
                agent = s["register_agent"]
                email = self.resolve(agent["email"])
                try:
                    self.api.register_agent(RegisterAgentRequest(
                        email=email, agent_mode=agent.get("agent_mode"),
                    ))
                except E2AApiError as e:
                    if e.status_code != 409:
                        raise
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
            # Route through SDK public methods
            try:
                status, data = _route_to_sdk(self.api, step["method"], path, body, self.resolve)
            except E2AApiError as e:
                # SDK threw on non-2xx — check it matches expectation
                if "status" in ex:
                    assert e.status_code == ex["status"], (
                        f"step {step['id']}: expected {ex['status']}, got {e.status_code}"
                    )
                return

            if status is None:
                # Unrecognized path (e.g. legacy /api/... aliases) — fall back to bare HTTP
                resp = self._http.request(
                    step["method"], path,
                    headers={"Authorization": f"Bearer {self.api_key}"},
                    json=body if body is not None else None,
                )
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
