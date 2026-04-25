"""Tests that e2a.v1 and top-level e2a export the expected public surface."""


def test_v1_exports():
    from e2a.v1 import (
        E2AApi,
        AsyncE2AApi,
        E2AApiError,
        E2AClient,
        AsyncE2AClient,
        InboundEmail,
        AsyncInboundEmail,
        Attachment,
        AuthHeaders,
        MessageList,
        MessageSummary,
        SendResult,
    )
    # All imports resolved without error
    assert E2AApi is not None
    assert AsyncE2AApi is not None
    assert E2AApiError is not None
    assert E2AClient is not None
    assert AsyncE2AClient is not None
    assert InboundEmail is not None
    assert AsyncInboundEmail is not None


def test_toplevel_aliases_point_to_v1():
    import e2a
    import e2a.v1

    # Top-level aliases should be the exact same classes as v1
    assert e2a.E2AClient is e2a.v1.E2AClient
    assert e2a.AsyncE2AClient is e2a.v1.AsyncE2AClient
    assert e2a.E2AApi is e2a.v1.E2AApi
    assert e2a.AsyncE2AApi is e2a.v1.AsyncE2AApi
    assert e2a.E2AApiError is e2a.v1.E2AApiError
    assert e2a.InboundEmail is e2a.v1.InboundEmail
    assert e2a.AsyncInboundEmail is e2a.v1.AsyncInboundEmail
    assert e2a.Attachment is e2a.v1.Attachment
    assert e2a.AuthHeaders is e2a.v1.AuthHeaders
    assert e2a.MessageList is e2a.v1.MessageList
    assert e2a.MessageSummary is e2a.v1.MessageSummary
    assert e2a.SendResult is e2a.v1.SendResult


def test_v1_all_is_explicit():
    import e2a.v1
    assert hasattr(e2a.v1, "__all__")
    expected = {
        "E2AApi", "AsyncE2AApi", "E2AApiError",
        "E2AClient", "AsyncE2AClient",
        "InboundEmail", "AsyncInboundEmail",
        "Attachment", "AuthHeaders", "MessageList", "MessageSummary", "SendResult",
    }
    assert expected.issubset(set(e2a.v1.__all__))


def test_toplevel_all_is_explicit():
    import e2a
    assert hasattr(e2a, "__all__")
    expected = {
        "E2AApi", "AsyncE2AApi", "E2AApiError",
        "E2AClient", "AsyncE2AClient",
        "InboundEmail", "AsyncInboundEmail",
        "Attachment", "AuthHeaders", "MessageList", "MessageSummary", "SendResult",
    }
    assert expected.issubset(set(e2a.__all__))


def test_generated_models_accessible_from_v1():
    """Generated Pydantic models should still be accessible via e2a.v1."""
    from e2a.v1 import MessageDetail, RegisterAgentRequest, SendEmailRequest
    assert MessageDetail is not None
    assert RegisterAgentRequest is not None
    assert SendEmailRequest is not None


def test_contract_scenarios_yaml_exists():
    """The shared scenarios.yaml must be findable from the contract runner."""
    from pathlib import Path
    scenarios_path = Path(__file__).resolve().parents[3] / "tests" / "contract" / "scenarios.yaml"
    assert scenarios_path.exists(), f"scenarios.yaml not found at {scenarios_path}"
