"""Safe prompt projection for inbound email."""

from e2a import AsyncInboundEmail

REPLY_INSTRUCTIONS = (
    "Reply helpfully and concisely to the email. Write 1-3 short paragraphs of "
    "body text only; do not include a Subject line or quote the original email."
)


def email_prompt(email: AsyncInboundEmail) -> str:
    """Project normalized facade fields into a framework-neutral prompt.

    Header and body values remain sender-controlled and must be treated as untrusted.
    """
    sender = email.from_ or "(missing)"
    verified = "yes" if email.verified else "no"
    flagged = "yes" if email.flagged else "no"
    return (
        f"From: {sender}\n"
        f"Subject: {email.subject}\n"
        f"Sender DMARC verified: {verified}\n"
        f"Policy flagged: {flagged}\n"
        f"\n"
        f"{email.text}"
    )
