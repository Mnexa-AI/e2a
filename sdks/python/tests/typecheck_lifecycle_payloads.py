"""Static contract for lifecycle rows carried by stable webhook payloads."""

from e2a.v1.webhook_signature import (
    DomainSuppressionAddedData,
    EmailBouncedData,
    EmailComplainedData,
    EmailDeliveredData,
    EmailFailedData,
    EmailReceivedData,
    EmailSentData,
)
from e2a.v1.generated.models import MessageLifecycleTransition
from typing import Optional


def inspect_transition(row: MessageLifecycleTransition) -> None:
    recipient: Optional[str] = row.recipient
    evidence: dict[str, object] = row.evidence
    correlations: dict[str, str] = row.correlation_ids
    reconstructed: bool = row.reconstructed
    direction: str = row.direction
    stage: str = row.stage
    outcome: str = row.outcome
    reason: str = row.reason_code
    _ = (recipient, evidence, correlations, reconstructed, direction, stage, outcome, reason)


def inspect_payloads(
    received: EmailReceivedData,
    sent: EmailSentData,
    failed: EmailFailedData,
    delivered: EmailDeliveredData,
    bounced: EmailBouncedData,
    complained: EmailComplainedData,
    suppressed: DomainSuppressionAddedData,
) -> None:
    for payload in (
        received,
        sent,
        failed,
        delivered,
        bounced,
        complained,
        suppressed,
    ):
        for row in payload.get("lifecycle_transitions", []):
            inspect_transition(row)
