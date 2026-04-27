from e2a.v1.generated import MessageDetail, SendEmailRequest


def test_generated_models_accept_wire_aliases():
    detail = MessageDetail.model_validate({
        "from": "alice@example.com",
        "to": ["bot@agent.dev"],
        "recipient": "bot@agent.dev",
    })

    assert detail.from_ == "alice@example.com"
    assert detail.model_dump(by_alias=True, exclude_none=True) == {
        "from": "alice@example.com",
        "to": ["bot@agent.dev"],
        "recipient": "bot@agent.dev",
    }


def test_generated_models_accept_pythonic_field_names():
    request = SendEmailRequest(
        from_="bot@agent.dev",
        to=["alice@example.com"],
        subject="Hello",
        body="Hi Alice",
    )

    assert request.model_dump(by_alias=True, exclude_none=True) == {
        "from": "bot@agent.dev",
        "to": ["alice@example.com"],
        "subject": "Hello",
        "body": "Hi Alice",
    }
