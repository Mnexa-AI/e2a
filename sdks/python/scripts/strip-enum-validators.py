#!/usr/bin/env python3
"""Strip OpenAPI-Generator's `*_validate_enum` pydantic validators from the
generated models so the client tolerates unknown enum values.

Why: the generator emits, for every enum-typed field, a `@field_validator` that
raises `ValueError` when the value isn't in a hard-coded set. On a RESPONSE
field that means the day the server adds a new enum value (a new event `type`,
`delivery_status`, `inbound_policy`, …) every deployed client crashes while
deserializing — turning an additive, non-breaking server change into a
client-breaking one. The TypeScript SDK passes unknown enum values through
untouched; this makes Python match, keeping enum fields typed as plain strings.

Run as part of generate-oag.sh (so the drift gate's regenerated output matches)
and idempotently re-runnable on the committed tree. Removes each block of the
form:

        @field_validator('field')
        def field_validate_enum(cls, value):
            \"\"\"Validates the enum\"\"\"
            if value not in set([...]):
                raise ValueError(...)
            return value
"""

from __future__ import annotations

import glob
import os
import re
import sys


def strip_file(path: str) -> bool:
    with open(path, "r", encoding="utf-8") as f:
        lines = f.readlines()

    out: list[str] = []
    i = 0
    n = len(lines)
    changed = False
    decorator = re.compile(r"^\s*@field_validator\(")
    enum_def = re.compile(r"^\s*def\s+\w+_validate_enum\(")
    while i < n:
        line = lines[i]
        # A `@field_validator(...)` decorator whose method is an enum validator:
        # drop the decorator(s) + the entire def body. The body runs until the
        # next non-blank line indented no deeper than the def itself (the next
        # class member). Consuming by indentation — not by a `return value`
        # sentinel — correctly handles optional-field validators that early-
        # return on None before the enum check.
        if decorator.match(line):
            j = i + 1
            while j < n and lines[j].strip().startswith("@"):
                j += 1
            if j < n and enum_def.match(lines[j]):
                def_indent = len(lines[j]) - len(lines[j].lstrip())
                k = j + 1
                while k < n:
                    stripped = lines[k].strip()
                    if stripped == "":
                        k += 1
                        continue
                    indent = len(lines[k]) - len(lines[k].lstrip())
                    if indent > def_indent:
                        k += 1
                        continue
                    break
                i = k
                changed = True
                continue
        out.append(line)
        i += 1

    if changed:
        with open(path, "w", encoding="utf-8") as f:
            f.writelines(out)
    return changed


def main() -> int:
    root = sys.argv[1] if len(sys.argv) > 1 else os.path.join(
        os.path.dirname(__file__), "..", "src", "e2a", "v1", "generated"
    )
    models = glob.glob(os.path.join(root, "models", "*.py"))
    touched = sum(1 for p in sorted(models) if strip_file(p))
    print(f"strip-enum-validators: removed enum validators from {touched} model file(s)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
