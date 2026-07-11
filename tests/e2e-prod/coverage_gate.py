#!/usr/bin/env python3
"""Conformance coverage gate.

Every OpenAPI operation must be exercised by the e2e-prod suite, or the gate
fails — so if a new operationId is added to api/openapi.yaml (the drift-gated
SSOT) and no suite calls it, CI catches the coverage regression.

Inputs:
  - reports/coverage-*.json : shards written by the harness (harness/coverage.ts),
    each a JSON array of "METHOD /concrete/path" strings the ApiClient issued.
  - api/openapi.yaml        : the operation catalog (method + path template +
    operationId).

Maps each exercised concrete path to the most-specific matching operationId, then
compares the covered set to the full catalog (minus an explicit allowlist of
operations the black-box suite legitimately must not call).

Usage: python3 coverage_gate.py [--openapi PATH] [--reports DIR]
Exit 0 = all covered (or allowlisted); 1 = coverage gap; 2 = usage/IO error.
"""
import argparse
import glob
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))

# Operations the black-box conformance suite intentionally does NOT exercise, with
# the reason. Keep this list SHORT and justified — every entry is coverage we're
# knowingly forgoing.
ALLOWLIST = {
    "deleteAccount": "destructive — the suite must never delete its own account",
}


def load_ops(openapi_path):
    try:
        import yaml
    except ImportError:
        print("coverage_gate: PyYAML required (pip install pyyaml)", file=sys.stderr)
        sys.exit(2)
    with open(openapi_path) as f:
        spec = yaml.safe_load(f)
    ops = []  # (METHOD, [template segments], operationId)
    for path, item in (spec.get("paths") or {}).items():
        for method, op in (item or {}).items():
            if isinstance(op, dict) and "operationId" in op:
                ops.append((method.upper(), path.strip("/").split("/"), op["operationId"]))
    return ops


def match_op(method, segments, ops):
    """Most-specific (most literal segments) operationId matching a concrete path."""
    best, best_literals = None, -1
    for m, tsegs, opid in ops:
        if m != method or len(tsegs) != len(segments):
            continue
        literals, ok = 0, True
        for t, s in zip(tsegs, segments):
            if t.startswith("{") and t.endswith("}"):
                if not s:
                    ok = False
                    break
            elif t != s:
                ok = False
                break
            else:
                literals += 1
        if ok and literals > best_literals:
            best, best_literals = opid, literals
    return best


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--openapi", default=os.path.join(HERE, "../../api/openapi.yaml"))
    ap.add_argument("--reports", default=os.path.join(HERE, "reports"))
    args = ap.parse_args()

    if not os.path.exists(args.openapi):
        print(f"coverage_gate: openapi spec not found: {args.openapi}", file=sys.stderr)
        return 2

    ops = load_ops(args.openapi)
    all_ids = {opid for _, _, opid in ops}

    shards = glob.glob(os.path.join(args.reports, "coverage-*.json"))
    if not shards:
        print(f"coverage_gate: no coverage shards in {args.reports} — did the suite run?", file=sys.stderr)
        return 2
    exercised = set()
    for shard in shards:
        with open(shard) as f:
            exercised.update(json.load(f))

    covered_ids, non_v1 = set(), set()
    for pair in exercised:
        method, _, path = pair.partition(" ")
        opid = match_op(method, path.strip("/").split("/"), ops)
        if opid:
            covered_ids.add(opid)
        else:
            non_v1.add(pair)

    missing = all_ids - covered_ids
    allowlisted = missing & set(ALLOWLIST)
    uncovered = missing - set(ALLOWLIST)

    print(f"OpenAPI operations : {len(all_ids)}")
    print(f"Covered            : {len(covered_ids)}")
    print(f"Allowlisted        : {len(allowlisted)} " + (str(sorted(allowlisted)) if allowlisted else ""))
    print(f"Coverage shards    : {len(shards)}  (exercised pairs: {len(exercised)}, non-/v1: {len(non_v1)})")

    if uncovered:
        print(f"\nUNCOVERED ({len(uncovered)}):")
        for opid in sorted(uncovered):
            m, t, _ = next(o for o in ops if o[2] == opid)
            print(f"  - {opid:28s} {m} /{'/'.join(t)}")
        print("\nGATE: FAIL — the above operation(s) are in the spec but no suite exercises them.")
        return 1

    print("\nGATE: PASS — every OpenAPI operation is exercised (or explicitly allowlisted).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
