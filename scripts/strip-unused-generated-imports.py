#!/usr/bin/env python3
"""Remove selected unused imports from generated SDK files.

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
PYTHON_FROM_IMPORT = re.compile(
    r"^from\s+(?P<module>[A-Za-z_][A-Za-z0-9_.]*)\s+import\s+"
    r"(?P<names>[A-Za-z_][A-Za-z0-9_]*(?:[ \t]*,[ \t]*[A-Za-z_][A-Za-z0-9_]*)*)"
    r"(?:[ \t]+#.*)?(?P<newline>\r?\n)?$"
)
TYPESCRIPT_IMPORT = re.compile(
    r"^import\s*\{\s*(?P<names>[A-Za-z_$][A-Za-z0-9_$]*"
    r"(?:\s*,\s*[A-Za-z_$][A-Za-z0-9_$]*)*)\s*\}\s*"
    r"from\s*(?P<quote>['\"])(?P<module>[^'\"]+)(?P=quote)\s*;[ \t]*"
    r"(?P<newline>\r?\n)?$"
)
TYPESCRIPT_STRING = re.compile(
    r"(?P<quote>['\"`])(?:\\.|(?!(?P=quote))[\s\S])*(?P=quote)"
)
TYPESCRIPT_COMMENT = re.compile(r"//[^\r\n]*|/\*[\s\S]*?\*/")


def _identifier_is_used(name: str, source: str, *, python: bool) -> bool:
    if not python:
        source = TYPESCRIPT_STRING.sub("", source)
        source = TYPESCRIPT_COMMENT.sub("", source)
    return re.search(rf"\b{re.escape(name)}\b", source) is not None


def _strip_grouped_import(
    line: str,
    source_without_import: str,
    names_to_check: set[str] | None,
    *,
    python: bool,
) -> tuple[str, bool] | None:
    pattern = PYTHON_FROM_IMPORT if python else TYPESCRIPT_IMPORT
    match = pattern.match(line)
    if match is None:
        return None

    imported_names = [name.strip() for name in match.group("names").split(",")]
    retained_names = [
        name
        for name in imported_names
        if (
            names_to_check is not None and name not in names_to_check
        ) or _identifier_is_used(name, source_without_import, python=python)
    ]
    if retained_names == imported_names:
        return line, False
    if not retained_names:
        return "", True

    newline = match.group("newline") or ""
    if python:
        replacement = (
            f"from {match.group('module')} import {', '.join(retained_names)}{newline}"
        )
    else:
        quote = match.group("quote")
        replacement = (
            f"import {{ {', '.join(retained_names)} }} from "
            f"{quote}{match.group('module')}{quote};{newline}"
        )
    return replacement, True


def strip_file(path: Path, names: set[str] | None = None) -> bool:
    source = path.read_text(encoding="utf-8")
    lines = source.splitlines(keepends=True)
    output: list[str] = []
    changed = False

    for line in lines:
        source_without_import = source.replace(line, "", 1)
        grouped = _strip_grouped_import(
            line,
            source_without_import,
            names,
            python=path.suffix == ".py",
        )
        if grouped is not None:
            replacement, line_changed = grouped
            output.append(replacement)
            changed = changed or line_changed
            continue

        if path.suffix != ".py":
            output.append(line)
            continue

        match = PYTHON_IMPORT.match(line)
        if match is None:
            output.append(line)
            continue

        name = match.group(1)
        if names is not None and name not in names:
            output.append(line)
            continue
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
