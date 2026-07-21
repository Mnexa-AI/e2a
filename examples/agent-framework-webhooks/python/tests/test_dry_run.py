from agent_webhooks.dry_run import main
import pytest


def test_no_key_dry_run_replies_once_and_marks_duplicate(
    capsys: pytest.CaptureFixture[str],
) -> None:
    evidence = main()
    output = capsys.readouterr().out

    assert evidence == {
        "first_status": "replied",
        "second_status": "duplicate",
        "reply": "Deterministic fake reply",
        "reply_count": 1,
    }
    assert "status=replied" in output
    assert "status=duplicate" in output
    assert "reply=Deterministic fake reply" in output
