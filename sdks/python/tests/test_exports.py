"""Tests that e2a.v1 and top-level e2a export the expected public surface (3.0)."""


def test_v1_exports():
    from e2a.v1 import (  # noqa: F401
        AutoPager,
        E2AClient,
        E2AError,
        E2ANotFoundError,
        E2AWebhookSignatureError,
        Page,
        WebhookEvent,
        WSNotification,
        WSStream,
        construct_event,
        verify_webhook_signature,
    )

    assert E2AClient is not None
    assert E2AError is not None
    assert construct_event is not None


def test_toplevel_aliases_point_to_v1():
    import e2a
    import e2a.v1

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
        "E2AClient",
        "E2AError",
        "E2ANotFoundError",
        "AutoPager",
        "Page",
        "verify_webhook_signature",
        "construct_event",
        "WebhookEvent",
        "WSNotification",
        "WSStream",
    }
    assert expected.issubset(set(e2a.v1.__all__))


def test_toplevel_all_is_explicit():
    import e2a

    assert hasattr(e2a, "__all__")
    assert {"E2AClient", "E2AError", "construct_event"}.issubset(set(e2a.__all__))


def test_generated_models_accessible_from_v1():
    """Generated Pydantic models are re-exported via e2a.v1 (and e2a.v1.models)."""
    from e2a.v1 import AgentView, SendEmailRequest, models

    assert AgentView is not None
    assert SendEmailRequest is not None
    assert models.MessageView is not None


def test_legacy_surface_is_gone():
    """The retired flat/sync surface must no longer import."""
    import e2a.v1

    for name in ("E2AApi", "AsyncE2AClient", "InboundEmail", "AuthHeaders"):
        assert not hasattr(e2a.v1, name), f"{name} should be retired"


def test_contract_scenarios_yaml_exists():
    """The shared scenarios.yaml must be findable from the contract runner."""
    from pathlib import Path

    scenarios_path = Path(__file__).resolve().parents[3] / "tests" / "contract" / "scenarios.yaml"
    assert scenarios_path.exists(), f"scenarios.yaml not found at {scenarios_path}"
