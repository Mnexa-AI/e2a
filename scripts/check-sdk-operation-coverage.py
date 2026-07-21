#!/usr/bin/env python3
"""SDK ergonomic-surface coverage gate.

Sibling of tests/e2e-prod/coverage_gate.py. That gate asks "is every operation
EXERCISED by the conformance suite?"; this one asks "is every operation REACHABLE
through the hand-written ergonomic client?" — the layer users actually call.

Why this exists: both SDKs are two-layer. `generated/` is produced from
api/openapi.yaml by OpenAPI Generator, so a new spec operation lands there
automatically and the generated-code-freshness gate stays green. But the
ergonomic surface (`client.messages.send(...)`, the pagers, typed errors) is
hand-written, and nothing forced a new operation to be wired into it. That's how
`deleteMessage` shipped generated-but-unreachable: PR #466 added the endpoint,
PR #480 wired up only the restore half, and no gate noticed the delete half was
never surfaced. Regenerating would not have fixed it — the generated layer had
the method all along.

How reachability is decided (deliberately stricter than a substring grep):
  - Comments and docstrings are stripped from the hand-written sources first, so
    an operation merely NAMED in prose can never produce a false green. This is
    the failure mode that matters — a stale doc comment silently satisfying the
    gate is worse than no gate.
  - What's matched is a CALL SITE, not a mention: `.<method>(` anchored on a word
    boundary and an opening paren. Anchoring on the paren also kills prefix
    collisions — `.deleteAgentSuppression(` does not satisfy `deleteAgent`,
    because the character after `deleteAgent` is `S`, not `(`.
  - The Python method name is derived from the operationId by snake_case, then
    CHECKED to exist in the generated api modules. A derivation that stops
    matching the generator's naming is a gate failure, not a silent pass.

Usage: python3 scripts/check-sdk-operation-coverage.py [--openapi PATH]
Exit 0 = every operation reachable (or explicitly allowlisted); 1 = a gap; 2 = usage/IO error.
"""
import argparse
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)

# Operations deliberately NOT surfaced on the ergonomic client, with the reason.
# Keep this list SHORT and justified — every entry is a hole in the typed surface
# that a user must drop to raw HTTP (or another client) to fill. An entry here is
# a decision; the absence of an entry is a bug.
#
# Keyed by operationId → {"ts": reason, "py": reason} (or a bare string to
# allowlist both SDKs for the same reason).
ALLOWLIST: dict[str, object] = {}


def die(msg):
    print(f"sdk-operation-coverage: {msg}", file=sys.stderr)
    return 2


def load_ops(openapi_path):
    try:
        import yaml
    except ImportError:
        return None, die("PyYAML required (pip install pyyaml)")
    with open(openapi_path) as f:
        spec = yaml.safe_load(f)
    ops = []  # (operationId, METHOD, path)
    for path, item in (spec.get("paths") or {}).items():
        for method, op in (item or {}).items():
            if isinstance(op, dict) and "operationId" in op:
                ops.append((op["operationId"], method.upper(), path))
    return ops, 0


def snake(name):
    """lowerCamelCase → snake_case, matching the Python generator's method names."""
    return re.sub(r"(?<!^)(?=[A-Z])", "_", name).lower()


def strip_ts_comments(src):
    """Remove // line and /* */ block comments (incl. JSDoc).

    Not a full JS lexer: a `//` or `/*` inside a string literal would be dropped
    too. That direction is SAFE for this gate — over-stripping can only hide a
    call site (false FAIL, loud), never invent one (false PASS, silent)."""
    src = re.sub(r"/\*.*?\*/", " ", src, flags=re.S)
    src = re.sub(r"//[^\n]*", " ", src)
    return src


def strip_py_comments(src):
    """Remove # comments and triple-quoted strings (docstrings).

    Same safety argument as the TS stripper: erring toward over-stripping can
    only cause a false failure, never a false pass."""
    src = re.sub(r'""".*?"""', " ", src, flags=re.S)
    src = re.sub(r"'''.*?'''", " ", src, flags=re.S)
    src = re.sub(r"#[^\n]*", " ", src)
    return src


def read_sources(root, exts, strip):
    """Concatenate the hand-written sources under `root`, skipping generated/."""
    if not os.path.isdir(root):
        return None
    chunks = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d != "generated"]
        for fn in sorted(filenames):
            if fn.endswith(exts):
                with open(os.path.join(dirpath, fn), encoding="utf-8") as f:
                    chunks.append(strip(f.read()))
    return "\n".join(chunks)


def calls(src, method):
    """True if `src` contains a call site `.method(` (word-boundary + paren)."""
    return re.search(r"\.\s*" + re.escape(method) + r"\s*\(", src) is not None


def reasons_for(opid, sdk):
    entry = ALLOWLIST.get(opid)
    if entry is None:
        return None
    if isinstance(entry, str):
        return entry
    return entry.get(sdk)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--openapi", default=os.path.join(ROOT, "api", "openapi.yaml"))
    args = ap.parse_args()

    if not os.path.exists(args.openapi):
        return die(f"openapi spec not found: {args.openapi}")

    ops, err = load_ops(args.openapi)
    if err:
        return err
    if not ops:
        return die("no operations parsed from the spec — wrong file?")

    ts_src = read_sources(os.path.join(ROOT, "sdks", "typescript", "src"), (".ts",), strip_ts_comments)
    py_src = read_sources(os.path.join(ROOT, "sdks", "python", "src"), (".py",), strip_py_comments)
    if ts_src is None:
        return die("TypeScript SDK sources not found")
    if py_src is None:
        return die("Python SDK sources not found")

    # The generated layer is the naming authority: if the snake_case derivation
    # stops matching what the generator emits, fail loudly rather than silently
    # reporting every Python operation as missing.
    py_generated = read_sources(
        os.path.join(ROOT, "sdks", "python", "src", "e2a", "v1", "generated", "api"),
        (".py",),
        lambda s: s,
    )
    if py_generated is None:
        return die("Python generated api modules not found")

    # An allowlist entry that no longer names a real operationId is a silent hole
    # (e.g. the op was renamed) — fail loudly so the allowlist can't drift stale.
    all_ids = {opid for opid, _, _ in ops}
    stale = set(ALLOWLIST) - all_ids
    if stale:
        return die(f"allowlist entries not in the spec (renamed/removed?): {sorted(stale)}")

    missing = {"ts": [], "py": []}
    allowed = {"ts": [], "py": []}
    bad_derivation = []

    for opid, method, path in sorted(ops):
        py_name = snake(opid)
        if not re.search(r"def\s+" + re.escape(py_name) + r"\s*\(", py_generated):
            bad_derivation.append((opid, py_name))
            continue
        for sdk, src, name in (("ts", ts_src, opid), ("py", py_src, py_name)):
            if calls(src, name):
                continue
            reason = reasons_for(opid, sdk)
            if reason:
                allowed[sdk].append((opid, reason))
            else:
                missing[sdk].append((opid, method, path, name))

    if bad_derivation:
        print("\nOPERATION-ID → PYTHON METHOD DERIVATION BROKEN:", file=sys.stderr)
        for opid, py_name in bad_derivation:
            print(f"  - {opid} → {py_name} (no such def in generated/api)", file=sys.stderr)
        print(
            "\nGATE: FAIL — the generator's naming changed; fix snake() in this script.",
            file=sys.stderr,
        )
        return 1

    print(f"OpenAPI operations      : {len(all_ids)}")
    for sdk, label in (("ts", "TypeScript"), ("py", "Python")):
        reachable = len(all_ids) - len(missing[sdk]) - len(allowed[sdk])
        note = f"  (allowlisted: {len(allowed[sdk])})" if allowed[sdk] else ""
        print(f"Reachable via {label:11s}: {reachable}{note}")
        for opid, reason in sorted(allowed[sdk]):
            print(f"    allowlisted: {opid} — {reason}")

    total_missing = missing["ts"] + missing["py"]
    if total_missing:
        for sdk, label in (("ts", "TypeScript"), ("py", "Python")):
            if not missing[sdk]:
                continue
            print(f"\nUNREACHABLE — {label} ({len(missing[sdk])}):")
            for opid, method, path, name in sorted(missing[sdk]):
                print(f"  - {opid:28s} {method} {path}")
                print(f"      generated as `{name}`, but no hand-written client calls it")
        print(
            "\nGATE: FAIL — the above operation(s) exist in the generated layer but are not\n"
            "exposed by the hand-written ergonomic client, so users can only reach them via\n"
            "raw HTTP. Add the method to the matching Resource class, or add an ALLOWLIST\n"
            "entry in this script with a written reason."
        )
        return 1

    print("\nGATE: PASS — every OpenAPI operation is reachable from both SDKs' ergonomic surface.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
