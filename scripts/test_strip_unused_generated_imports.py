from __future__ import annotations

import importlib.util
from pathlib import Path
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("strip-unused-generated-imports.py")


def load_cleaner():
    spec = importlib.util.spec_from_file_location("strip_unused_generated_imports", SCRIPT)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load {SCRIPT}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class StripUnusedGeneratedImportsTest(unittest.TestCase):
    def test_removes_only_unused_standalone_typescript_imports(self) -> None:
        cleaner = load_cleaner()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "Model.ts"
            path.write_text(
                "import { HttpFile } from '../http/http.js';\n"
                "import { Nested } from './Nested.js';\n\n"
                "export class Model { value: Nested; }\n",
                encoding="utf-8",
            )

            self.assertTrue(cleaner.strip_file(path))
            self.assertEqual(
                "import { Nested } from './Nested.js';\n\n"
                "export class Model { value: Nested; }\n",
                path.read_text(encoding="utf-8"),
            )

    def test_import_path_does_not_count_as_typescript_usage(self) -> None:
        cleaner = load_cleaner()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "PromiseAPI.ts"
            path.write_text(
                "import { SendingRampView } from '../models/SendingRampView.js';\n\n"
                "export class PromiseAPI {}\n",
                encoding="utf-8",
            )

            self.assertTrue(cleaner.strip_file(path))
            self.assertEqual(
                "\nexport class PromiseAPI {}\n",
                path.read_text(encoding="utf-8"),
            )

    def test_removes_only_unused_python_imports(self) -> None:
        cleaner = load_cleaner()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "model.py"
            path.write_text(
                "import pprint\n"
                "import re  # noqa: F401\n\n"
                "def render(value):\n"
                "    return pprint.pformat(value)\n",
                encoding="utf-8",
            )

            self.assertTrue(cleaner.strip_file(path))
            self.assertEqual(
                "import pprint\n\n"
                "def render(value):\n"
                "    return pprint.pformat(value)\n",
                path.read_text(encoding="utf-8"),
            )

    def test_leaves_file_unchanged_when_every_import_is_used(self) -> None:
        cleaner = load_cleaner()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "used.ts"
            source = (
                "import { HttpFile } from '../http/http.js';\n\n"
                "export type Upload = HttpFile;\n"
            )
            path.write_text(source, encoding="utf-8")

            self.assertFalse(cleaner.strip_file(path))
            self.assertEqual(source, path.read_text(encoding="utf-8"))

    def test_can_limit_cleanup_to_requested_symbols(self) -> None:
        cleaner = load_cleaner()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "generated.ts"
            path.write_text(
                "import { HttpFile } from '../http/http.js';\n"
                "import { HistoricalModel } from '../models/HistoricalModel.js';\n",
                encoding="utf-8",
            )

            self.assertTrue(cleaner.strip_file(path, {"HttpFile"}))
            self.assertEqual(
                "import { HistoricalModel } from '../models/HistoricalModel.js';\n",
                path.read_text(encoding="utf-8"),
            )

    def test_removes_selected_unused_name_from_grouped_typescript_import(self) -> None:
        cleaner = load_cleaner()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "ObjectSerializer.ts"
            path.write_text(
                "import { DKIMResult, DKIMResultStatusEnum } "
                "from '../models/DKIMResult.js';\n\n"
                "export const typeMap = { DKIMResult };\n"
                "export const enumsMap = [\"DKIMResultStatusEnum\"];\n",
                encoding="utf-8",
            )

            self.assertTrue(cleaner.strip_file(path, {"DKIMResultStatusEnum"}))
            self.assertEqual(
                "import { DKIMResult } from '../models/DKIMResult.js';\n\n"
                "export const typeMap = { DKIMResult };\n"
                "export const enumsMap = [\"DKIMResultStatusEnum\"];\n",
                path.read_text(encoding="utf-8"),
            )

    def test_removes_selected_unused_name_from_grouped_python_import(self) -> None:
        cleaner = load_cleaner()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "message_view.py"
            path.write_text(
                "from pydantic import BaseModel, ConfigDict, field_validator\n\n"
                "class MessageView(BaseModel):\n"
                "    model_config = ConfigDict()\n",
                encoding="utf-8",
            )

            self.assertTrue(cleaner.strip_file(path, {"field_validator"}))
            self.assertEqual(
                "from pydantic import BaseModel, ConfigDict\n\n"
                "class MessageView(BaseModel):\n"
                "    model_config = ConfigDict()\n",
                path.read_text(encoding="utf-8"),
            )


if __name__ == "__main__":
    unittest.main()
