"""Tests for the slice 6/7/8 events SDK surface in E2AApi.

Covers:
  * list_events with all query params
  * get_event happy path + 404 + 410
  * redeliver_event targeted + fan-out
  * redeliver_webhook_since happy path + 400
  * Header propagation (Bearer auth)
  * Concurrent calls via threading

Uses httpx.MockTransport to intercept requests without needing a live
server — same pattern as test_v1_api.py.
"""

from __future__ import annotations

import json
import threading
from typing import Any

import httpx
import pytest

from e2a.v1.api import E2AApi, E2AApiError
from e2a.v1.generated import (
    ListEventsResponse,
    RedeliverResponse,
    RedeliverSinceResponse,
    WebhookEvent,
)


def make_api(handler: callable) -> E2AApi:
    """Build an E2AApi backed by httpx.MockTransport with a custom handler."""
    transport = httpx.MockTransport(handler)
    api = E2AApi(api_key="e2a_test", base_url="http://test.local")
    # Replace the underlying client with one using our transport.
    api._client.close()  # type: ignore[attr-defined]
    api._client = httpx.Client(  # type: ignore[attr-defined]
        base_url="http://test.local",
        transport=transport,
        headers={"Authorization": "Bearer e2a_test"},
    )
    return api


def test_list_events_no_params():
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["url"] = str(request.url)
        captured["method"] = request.method
        captured["headers"] = dict(request.headers)
        return httpx.Response(200, json={"events": [], "next_token": ""})

    api = make_api(handler)
    res = api.list_events()
    assert isinstance(res, ListEventsResponse)
    assert captured["url"] == "http://test.local/api/v1/events"
    assert captured["method"] == "GET"
    assert captured["headers"]["authorization"] == "Bearer e2a_test"


def test_list_events_with_filters():
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["params"] = dict(request.url.params)
        return httpx.Response(200, json={"events": [], "next_token": ""})

    api = make_api(handler)
    api.list_events(
        type="email.received",
        agent_id="ag_x",
        conversation_id="conv_y",
        message_id="msg_z",
        since="2026-06-01T00:00:00Z",
        until="2026-06-02T00:00:00Z",
        page_size=25,
        token="opaque",
    )
    assert captured["params"]["type"] == "email.received"
    assert captured["params"]["agent_id"] == "ag_x"
    assert captured["params"]["conversation_id"] == "conv_y"
    assert captured["params"]["message_id"] == "msg_z"
    assert captured["params"]["since"] == "2026-06-01T00:00:00Z"
    assert captured["params"]["until"] == "2026-06-02T00:00:00Z"
    assert captured["params"]["page_size"] == "25"
    assert captured["params"]["token"] == "opaque"


def test_list_events_parses_response():
    sample = {
        "events": [
            {
                "id": "evt_a",
                "type": "email.received",
                "schema_version": 1,
                "created_at": "2026-06-01T12:00:00Z",
                "status": "processed",
                "data": {"x": 1},
            },
            {
                "id": "evt_b",
                "type": "email.sent",
                "schema_version": 1,
                "created_at": "2026-06-01T11:00:00Z",
                "status": "processed",
                "data": {},
            },
        ],
        "next_token": "next_cursor",
    }

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json=sample)

    api = make_api(handler)
    res = api.list_events()
    assert len(res.events) == 2
    assert res.events[0].id == "evt_a"
    assert res.next_token == "next_cursor"


def test_get_event_happy_path():
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["path"] = request.url.path
        return httpx.Response(
            200,
            json={
                "id": "evt_abc",
                "type": "email.received",
                "schema_version": 1,
                "created_at": "2026-06-01T12:00:00Z",
                "status": "processed",
                "data": {"hello": "world"},
            },
        )

    api = make_api(handler)
    e = api.get_event("evt_abc")
    assert isinstance(e, WebhookEvent)
    assert e.id == "evt_abc"
    assert captured["path"] == "/api/v1/events/evt_abc"


def test_get_event_url_encodes_id():
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        # raw_path preserves the percent-encoding from the wire shape
        # that the SDK actually sent. url.path decodes for display.
        captured["raw_path"] = request.url.raw_path.decode("ascii")
        return httpx.Response(
            200,
            json={
                "id": "x",
                "type": "email.received",
                "schema_version": 1,
                "created_at": "2026-06-01T00:00:00Z",
                "status": "processed",
                "data": {},
            },
        )

    api = make_api(handler)
    api.get_event("evt with spaces")
    # Verify quote() encoded the space into %20 on the wire.
    assert "%20" in captured["raw_path"]
    assert " " not in captured["raw_path"]


def test_get_event_404_raises():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(404, text="event not found")

    api = make_api(handler)
    with pytest.raises(E2AApiError) as exc:
        api.get_event("evt_missing")
    assert exc.value.status_code == 404


def test_get_event_410_raises():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(410, text="event expired")

    api = make_api(handler)
    with pytest.raises(E2AApiError) as exc:
        api.get_event("evt_expired")
    assert exc.value.status_code == 410


def test_redeliver_event_targeted():
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["url"] = str(request.url)
        captured["body"] = json.loads(request.content)
        return httpx.Response(200, json={"event_id": "evt_x", "webhook_id": "wh_y", "delivery_id": "whd_abc", "status": "pending"})

    api = make_api(handler)
    res = api.redeliver_event("evt_x", webhook_id="wh_y")
    assert isinstance(res, RedeliverResponse)
    assert captured["url"] == "http://test.local/api/v1/events/evt_x/redeliver"
    assert captured["body"] == {"webhook_id": "wh_y"}


def test_redeliver_event_fan_out_empty_body():
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["body"] = json.loads(request.content) if request.content else {}
        return httpx.Response(200, json={"event_id": "evt_x", "status": "scheduled", "deliveries": []})

    api = make_api(handler)
    api.redeliver_event("evt_x")
    assert captured["body"] == {}


def test_redeliver_event_409_when_not_originally_matched():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(409, text="webhook not in matched set")

    api = make_api(handler)
    with pytest.raises(E2AApiError) as exc:
        api.redeliver_event("evt_x", webhook_id="wh_other")
    assert exc.value.status_code == 409


def test_redeliver_webhook_since_happy():
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["url"] = str(request.url)
        captured["body"] = json.loads(request.content)
        return httpx.Response(
            200,
            json={
                "webhook_id": "wh_target",
                "since": "2026-06-01T00:00:00Z",
                "scheduled": 5,
                "skipped_already_pending": 0,
            },
        )

    api = make_api(handler)
    res = api.redeliver_webhook_since("wh_target", "2026-06-01T00:00:00Z")
    assert isinstance(res, RedeliverSinceResponse)
    assert res.scheduled == 5
    assert captured["url"] == "http://test.local/api/v1/webhooks/wh_target/redeliver-since"
    assert captured["body"] == {"since": "2026-06-01T00:00:00Z"}


def test_redeliver_webhook_since_400_when_out_of_window():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(400, text="since out of window")

    api = make_api(handler)
    with pytest.raises(E2AApiError) as exc:
        api.redeliver_webhook_since("wh_x", "2020-01-01T00:00:00Z")
    assert exc.value.status_code == 400


def test_concurrent_list_events_thread_safe():
    """Smoke test: 20 threads firing list_events should not crash or
    leak state across them."""

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            200,
            json={
                "events": [
                    {
                        "id": "evt_1",
                        "type": "email.received",
                        "schema_version": 1,
                        "created_at": "2026-06-01T00:00:00Z",
                        "status": "processed",
                        "data": {},
                    }
                ],
                "next_token": "",
            },
        )

    api = make_api(handler)
    results: list[ListEventsResponse] = []
    errors: list[Exception] = []
    lock = threading.Lock()

    def worker():
        try:
            res = api.list_events()
            with lock:
                results.append(res)
        except Exception as e:
            with lock:
                errors.append(e)

    threads = [threading.Thread(target=worker) for _ in range(20)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    assert len(errors) == 0, f"errors: {errors}"
    assert len(results) == 20
    for r in results:
        assert len(r.events) == 1
