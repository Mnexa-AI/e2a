#!/usr/bin/env python3
"""Remove unused standalone imports from selected generated SDK files.

OpenAPI Generator emits some imports unconditionally. Call this script from a
generator entrypoint with SYMBOL FILE pairs that need normalization so the
committed output remains reproducible without rewriting historical output.
"""

from __future__ import annotations

from pathlib import Path
import re
import sys


PYTHON_IMPORT = re.compile(
    r"^import\s+([A-Za-z_][A-Za-z0-9_]*)(?:\s+#.*)?(?:\r?\n)?$"
)
TYPESCRIPT_IMPORT = re.compile(
    r"^import\s*\{\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\}\s*"
    r"from\s*['\"][^'\"]+['\"]\s*;\s*(?:\r?\n)?$"
)


def strip_file(path: Path, names: set[str] | None = None) -> bool:
    source = path.read_text(encoding="utf-8")
    pattern = PYTHON_IMPORT if path.suffix == ".py" else TYPESCRIPT_IMPORT
    lines = source.splitlines(keepends=True)
    output: list[str] = []
    changed = False

    for line in lines:
        match = pattern.match(line)
        if match is None:
            output.append(line)
            continue

        name = match.group(1)
        if names is not None and name not in names:
            output.append(line)
            continue
        source_without_import = source.replace(line, "", 1)
        if re.search(rf"\b{re.escape(name)}\b", source_without_import) is None:
            changed = True
            continue
        output.append(line)

    if changed:
        path.write_text("".join(output), encoding="utf-8")
    return changed


def main(argv: list[str]) -> int:
    if not argv or len(argv) % 2 != 0:
        print(
            "usage: strip-unused-generated-imports.py SYMBOL FILE [SYMBOL FILE ...]",
            file=sys.stderr,
        )
        return 2

    changed = sum(
        strip_file(Path(argv[index + 1]), {argv[index]})
        for index in range(0, len(argv), 2)
    )
    print(f"strip-unused-generated-imports: normalized {changed} file(s)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
