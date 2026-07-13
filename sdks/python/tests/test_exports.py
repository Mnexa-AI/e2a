"""Tests that e2a.v1 and top-level e2a export the expected public surface (5.0)."""


def test_v1_exports():
    from e2a.v1 import (  # noqa: F401
        AsyncE2AClient,
        AutoPager,
        E2AClient,
        E2AError,
        E2ANotFoundError,
        E2AWebhookSignatureError,
        Page,
        EmailBouncedData,
        EmailReceivedData,
        SyncAutoPager,
        SyncStream,
        WebhookEvent,
        WSEvent,
        WSStream,
        construct_event,
        is_email_bounced,
        is_email_received,
        verify_webhook_signature,
    )

    assert AsyncE2AClient is not None
    assert E2AClient is not None
    assert E2AError is not None
    assert construct_event is not None


def test_toplevel_aliases_point_to_v1():
    import e2a
    import e2a.v1

    assert e2a.AsyncE2AClient is e2a.v1.AsyncE2AClient
    assert e2a.E2AClient is e2a.v1.E2AClient
    assert e2a.E2AError is e2a.v1.E2AError
    assert e2a.E2ANotFoundError is e2a.v1.E2ANotFoundError
    assert e2a.construct_event is e2a.v1.construct_event
    assert e2a.verify_webhook_signature is e2a.v1.verify_webhook_signature
    assert e2a.WSStream is e2a.v1.WSStream


def test_v1_all_is_explicit():
    import e2a.v1

    assert hasattr(e2a.v1, "__all__")
    expected = {
        "AsyncE2AClient",
        "E2AClient",
        "SyncAutoPager",
        "SyncStream",
        "E2AError",
        "E2ANotFoundError",
        "AutoPager",
        "Page",
        "verify_webhook_signature",
        "construct_event",
        "WebhookEvent",
        "EmailReceivedData",
        "EmailBouncedData",
        "is_email_received",
        "WSEvent",
        "WSStream",
    }
    assert expected.issubset(set(e2a.v1.__all__))


def test_toplevel_all_is_explicit():
    import e2a

    assert hasattr(e2a, "__all__")
    assert {"E2AClient", "AsyncE2AClient", "E2AError", "construct_event"}.issubset(set(e2a.__all__))


def test_generated_models_accessible_from_v1():
    """Generated Pydantic models are re-exported via e2a.v1 (and e2a.v1.models)."""
    from e2a.v1 import AgentView, SendEmailRequest, models

    assert AgentView is not None
    assert SendEmailRequest is not None
    assert models.MessageView is not None


def test_legacy_surface_is_gone():
    """The retired flat/sync surface must no longer import."""
    import e2a.v1

    for name in ("E2AApi", "InboundEmail", "AuthHeaders"):
        assert not hasattr(e2a.v1, name), f"{name} should be retired"


def test_e2aclient_is_the_sync_client():
    """`E2AClient` is the synchronous client — the v5 rename freed the plain
    name for it (plain name = sync, `Async*` = async, per httpx/openai/anthropic
    convention). It must be the sync facade class, distinct from the async
    client, and exported from both import paths.
    """
    import e2a
    import e2a.v1
    from e2a.v1.sync_client import E2AClient as SyncClient

    assert e2a.E2AClient is SyncClient
    assert e2a.v1.E2AClient is SyncClient
    assert e2a.E2AClient is not e2a.AsyncE2AClient

    assert "E2AClient" in e2a.__all__
    assert "E2AClient" in e2a.v1.__all__


def test_module_unknown_name_still_attributeerror():
    """Unknown attributes on both packages raise plain AttributeError."""
    import pytest

    import e2a
    import e2a.v1

    for mod in (e2a, e2a.v1):
        with pytest.raises(AttributeError):
            getattr(mod, "DefinitelyNotAThing")


def test_contract_scenarios_yaml_exists():
    """The shared scenarios.yaml must be findable from the contract runner."""
    from pathlib import Path

    scenarios_path = Path(__file__).resolve().parents[3] / "tests" / "contract" / "scenarios.yaml"
    assert scenarios_path.exists(), f"scenarios.yaml not found at {scenarios_path}"
