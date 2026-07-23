from __future__ import annotations

import importlib.util
from pathlib import Path
import unittest


SCRIPT = Path(__file__).with_name("check-sdk-operation-coverage.py")


def load_checker():
    spec = importlib.util.spec_from_file_location("check_sdk_operation_coverage", SCRIPT)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load {SCRIPT}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class OperationIDToSDKMethodTest(unittest.TestCase):
    def test_converts_camel_cased_operation_id_to_snake_case(self) -> None:
        checker = load_checker()

        self.assertEqual(
            "get_message_lifecycle",
            checker.snake("getMessageLifecycle"),
        )

    def test_camel_cased_operation_id_is_already_lower_camel_case(self) -> None:
        checker = load_checker()
        typescript_method = getattr(checker, "typescript_method", lambda name: name)

        self.assertEqual(
            "getMessageLifecycle",
            typescript_method("getMessageLifecycle"),
        )


if __name__ == "__main__":
    unittest.main()
