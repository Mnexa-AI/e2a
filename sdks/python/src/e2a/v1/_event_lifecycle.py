"""Shared lifecycle coercion for v1 stable event envelopes."""

from __future__ import annotations

from typing import Any, Dict

from .generated.models import MessageLifecycleTransition


_V1_STABLE_LIFECYCLE_EVENTS = {
    "email.received",
    "email.sent",
    "email.failed",
    "email.delivered",
    "email.bounced",
    "email.complained",
    "domain.suppression_added",
}


def coerce_v1_stable_lifecycle(
    schema_version: str,
    event_type: str,
    data: Dict[str, Any],
) -> Dict[str, Any]:
    """Return typed lifecycle rows only for the closed v1 stable contract.

    When the field is absent—or the envelope version/type is outside that
    contract—the original data object is returned unchanged. When present, a
    shallow copy keeps ``event.raw`` as the original wire representation.
    """
    if (
        schema_version != "1"
        or event_type not in _V1_STABLE_LIFECYCLE_EVENTS
        or "lifecycle_transitions" not in data
    ):
        return data

    coerced = dict(data)
    coerced["lifecycle_transitions"] = [
        MessageLifecycleTransition.model_validate(row)
        for row in data["lifecycle_transitions"]
    ]
    return coerced
