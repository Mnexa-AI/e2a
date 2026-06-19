"""Forward-compatibility: generated models must NOT raise on unknown enum values.

A new server-side enum value (a new event ``type``, ``delivery_status``,
``inbound_policy``, …) is an additive, non-breaking change. OpenAPI Generator
emits ``*_validate_enum`` pydantic validators that raise ``ValueError`` on any
value outside a hard-coded set — which would turn that additive change into a
crash for every deployed client. ``scripts/strip-enum-validators.py`` (run by
generate-oag.sh) removes them; these tests lock that in so a regeneration can't
silently reintroduce the crash. Mirrors the TypeScript SDK's passthrough.
"""

from __future__ import annotations

import glob
import os

from e2a.v1.generated.models.event_json import EventJSON


def test_unknown_event_type_parses() -> None:
    e = EventJSON.from_dict(
        {
            "id": "evt_1",
            "type": "email.some_future_type",  # not in the known catalog
            "status": "delivered",
            "schema_version": 1,
            "created_at": "2026-06-18T00:00:00Z",
            "agent_id": "a@x.com",
            "data": {},
        }
    )
    assert e is not None
    assert e.type == "email.some_future_type"


def test_unknown_event_status_parses() -> None:
    e = EventJSON.from_dict(
        {
            "id": "evt_1",
            "type": "email.received",
            "status": "future_status",
            "schema_version": 1,
            "created_at": "2026-06-18T00:00:00Z",
            "agent_id": "a@x.com",
            "data": {},
        }
    )
    assert e is not None
    assert e.status == "future_status"


def test_no_enum_validators_remain_in_generated_models() -> None:
    root = os.path.join(
        os.path.dirname(__file__), "..", "src", "e2a", "v1", "generated", "models"
    )
    offenders = [
        os.path.basename(p)
        for p in glob.glob(os.path.join(root, "*.py"))
        if "_validate_enum" in open(p, encoding="utf-8").read()
    ]
    assert not offenders, (
        "enum validators must be stripped for forward-compat; found in: "
        f"{offenders} — run scripts/strip-enum-validators.py"
    )
