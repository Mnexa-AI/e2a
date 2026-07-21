import os
from pathlib import Path
import subprocess
import sys

import pytest

from agent_webhooks.dry_run import run


def test_no_key_dry_run_replies_once_and_marks_duplicate(
    capsys: pytest.CaptureFixture[str],
) -> None:
    evidence = run()
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


def test_installed_dry_run_command_needs_no_provider_key() -> None:
    executable = Path(sys.executable).with_name(
        "agent-framework-webhooks-dry-run"
    )
    env = os.environ.copy()
    for name in (
        "OPENAI_API_KEY",
        "ANTHROPIC_API_KEY",
        "GEMINI_API_KEY",
        "GOOGLE_API_KEY",
        "GOOGLE_GENAI_USE_VERTEXAI",
        "GOOGLE_CLOUD_PROJECT",
        "GOOGLE_CLOUD_LOCATION",
    ):
        env.pop(name, None)

    completed = subprocess.run(
        [str(executable)],
        check=False,
        capture_output=True,
        text=True,
        env=env,
        timeout=10,
    )

    assert completed.returncode == 0
    assert completed.stdout.strip() == (
        "status=replied status=duplicate "
        "reply=Deterministic fake reply reply_count=1"
    )
    assert completed.stderr == ""
