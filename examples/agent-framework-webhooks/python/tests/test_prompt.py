from types import SimpleNamespace

from agent_webhooks.prompt import email_prompt


def test_email_prompt_projects_only_safe_inbound_fields() -> None:
    email = SimpleNamespace(
        from_="Ada <ada@example.com>",
        subject="Question",
        verified=True,
        flagged=False,
        text="Can you help?",
        message=SimpleNamespace(raw_message="SECRET RAW MIME"),
    )

    prompt = email_prompt(email)

    assert prompt == (
        "From: Ada <ada@example.com>\n"
        "Subject: Question\n"
        "Sender DMARC verified: yes\n"
        "Policy flagged: no\n"
        "\n"
        "Can you help?"
    )
    assert "SECRET RAW MIME" not in prompt


def test_email_prompt_labels_a_missing_sender() -> None:
    email = SimpleNamespace(
        from_=None,
        subject="Question",
        verified=False,
        flagged=True,
        text="Can you help?",
    )

    assert email_prompt(email).startswith("From: (missing)\n")
