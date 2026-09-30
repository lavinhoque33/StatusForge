#!/usr/bin/env python3
"""npm audit (production, low and above) for infra/, with reasoned exceptions.

Only infra/ may carry exceptions (web/ runs plain npm audit). An exception pins
an installed package path, its installed version, and each advisory ID. A
finding passes only when every advisory behind it is excepted at every path it
is installed at, with the pinned version installed there. A new advisory,
another path, or a version change fails; so does an exception that no longer
matches a finding (stale), so the list shrinks as upstream fixes land.
"""
import json
import pathlib
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[3]
PROJECT = ROOT / "infra"
allowed = json.loads((pathlib.Path(__file__).parent / "audit-exceptions.json").read_text())

result = subprocess.run(
    ["npm", "audit", "--omit=dev", "--audit-level=low", "--json",
     "--registry=https://registry.npmjs.org"],
    cwd=PROJECT, capture_output=True, text=True,
)
try:
    report = json.loads(result.stdout)
except ValueError:
    print(result.stdout + result.stderr)
    sys.exit(1)
if "error" in report or "vulnerabilities" not in report:
    print(json.dumps(report.get("error", report), indent=2))
    sys.exit(1)

vulnerabilities = report["vulnerabilities"]
used = set()
problems = []


def installed_version(node):
    try:
        return json.loads((PROJECT / node / "package.json").read_text()).get("version")
    except (OSError, ValueError):
        return None


def excepted(name, trail=()):
    """True when every advisory behind this package's finding is excepted at its paths."""
    vulnerability = vulnerabilities[name]
    for via in vulnerability["via"]:
        if isinstance(via, str):
            # Vulnerable only through another package's advisory.
            if via in trail or via not in vulnerabilities or not excepted(via, trail + (name,)):
                return False
            continue
        advisory = via.get("url", "").rsplit("/", 1)[-1]
        for node in vulnerability["nodes"]:
            entry = allowed.get(node, {})
            reason = entry.get("advisories", {}).get(advisory)
            if not isinstance(reason, str) or not reason.strip():
                return False
            if installed_version(node) != entry.get("version"):
                problems.append(f"{node}: installed {installed_version(node)}, "
                                f"exception pins {entry.get('version')}")
                return False
            used.add((node, advisory))
    return True


for name in sorted(vulnerabilities):
    accepted_before = set(used)
    if not excepted(name):
        # A partly excepted finding accepts nothing.
        used.intersection_update(accepted_before)
        v = vulnerabilities[name]
        advisories = [via.get("url", "?") if isinstance(via, dict) else f"via {via}"
                      for via in v["via"]]
        problems.append(f"{name} ({v['severity']}, {v['range']}) at {', '.join(v['nodes'])}: "
                        f"{', '.join(advisories)}")
reported = {
    (node, via.get("url", "").rsplit("/", 1)[-1])
    for v in vulnerabilities.values() for via in v["via"] if isinstance(via, dict)
    for node in v["nodes"]
}
for node, entry in allowed.items():
    for advisory in entry.get("advisories", {}):
        if (node, advisory) not in reported:
            problems.append(f"stale exception (no longer reported): {node} {advisory}")
for problem in problems:
    print(f"  {problem}")
for node, advisory in sorted(used):
    print(f"  accepted risk: {node}@{allowed[node]['version']} {advisory}")
sys.exit(1 if problems else 0)
