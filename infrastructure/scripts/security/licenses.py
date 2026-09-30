#!/usr/bin/env python3
"""Check installed production dependencies, not build/test tool modules."""
import json
import os
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[3]
ALLOWED = {"MIT", "BSD-2-Clause", "BSD-3-Clause", "Apache-2.0", "ISC"}
exceptions = json.loads((pathlib.Path(__file__).parent / "licence-exceptions.json").read_text())
errors = []


def accepted(kind, name, license_id):
    if not isinstance(license_id, str) or not license_id or license_id.startswith("unknown"):
        return False
    # Every component of a compound SPDX expression must be allowed.
    parts = re.split(r"\s+(?:AND|OR)\s+", license_id)
    if all(part in ALLOWED for part in parts):
        return True
    reason = exceptions.get(kind, {}).get(name, {}).get(license_id)
    return isinstance(reason, str) and bool(reason.strip())


def go_licence(directory):
    # A *-SUMMARY file only indexes the licence files beside it.
    files = [
        p for p in pathlib.Path(directory).iterdir()
        if p.is_file() and p.name.lower().startswith(("license", "licence", "copying"))
        and not p.name.lower().endswith("-summary")
    ]
    if not files:
        return "unknown (missing licence file)"
    # Several licence files: every one applies to some part of the module.
    ids = sorted({licence_text_id(p.read_text(errors="replace")) for p in files})
    unknown = [i for i in ids if i.startswith("unknown")]
    return unknown[0] if unknown else " AND ".join(ids)


def licence_text_id(text):
    if re.search(r"Apache License\s+Version 2\.0", text, re.I):
        return "Apache-2.0"
    if text.lstrip().startswith("MIT No Attribution"):
        return "MIT-0"
    if ("Permission is hereby granted, free of charge" in text
            and 'THE SOFTWARE IS PROVIDED "AS IS"' in text):
        return "MIT"
    if ("Redistributions of source code must retain" in text
            and "Redistributions in binary form must reproduce" in text):
        return "BSD-3-Clause" if "Neither the name" in text else "BSD-2-Clause"
    if ("Permission to use, copy, modify, and/or distribute this software" in text
            and 'THE SOFTWARE IS PROVIDED "AS IS"' in text):
        return "ISC"
    return "unknown (unrecognized licence text)"


# Every shipped binary (Makefile build and lambda-build). cmd/statusforge is
# listed without the release tag: that tag only swaps internal/webui's embed
# file (standard-library imports only) and needs a built web bundle, which a
# fresh checkout and the CI security job do not have.
GO_BINARIES = [
    (["./cmd/statusforge"], [], {}),
    (["./cmd/lambda-planner", "./cmd/lambda-worker"], ["-tags", "lambda.norpc"],
     {"GOOS": "linux", "GOARCH": "arm64", "CGO_ENABLED": "0"}),
]


def check_go():
    modules = {}
    for packages, flags, env in GO_BINARIES:
        output = subprocess.check_output(
            ["go", "list", "-deps", "-json", *flags, *packages],
            cwd=ROOT / "backend", text=True, env={**os.environ, **env},
        )
        decoder = json.JSONDecoder()
        while output.strip():
            item, offset = decoder.raw_decode(output.lstrip())
            output = output.lstrip()[offset:]
            module = item.get("Module")
            if module and module.get("Path") != "github.com/lavinhoque33/statusforge/backend":
                # A replace directive supplies the actual source and licence.
                source = module.get("Replace", module)
                modules[module["Path"]] = source["Dir"]
    for name, directory in sorted(modules.items()):
        license_id = go_licence(directory)
        if not accepted("go", name, license_id):
            errors.append(f"Go {name}: {license_id}")
    print(f"licences/Go: {len(modules)} production modules, {len(errors)} findings")


def check_npm(project):
    """Walk a project's locked production tree (bundled dependencies included)."""
    root = ROOT / project
    output = subprocess.check_output(
        ["npm", "ls", "--omit=dev", "--all", "--json"],
        cwd=root, text=True,
    )
    tree = json.loads(output)
    seen = set()
    count = 0
    before = len(errors)

    def walk(node, parent):
        nonlocal count
        for name, child in node.get("dependencies", {}).items():
            if child.get("extraneous"):
                # Not in package-lock.json, so its licence says nothing about the release.
                errors.append(f"npm {name}: extraneous (not in package-lock.json); run npm ci")
                continue
            # npm hoists dependencies: resolve from the requesting package upwards.
            directory = parent
            while True:
                candidate = directory / "node_modules" / name
                if candidate.exists() or directory == root:
                    break
                directory = directory.parent
            manifest = candidate / "package.json"
            if not manifest.is_file():
                errors.append(f"npm {name}: installed package.json missing")
                continue
            package = json.loads(manifest.read_text())
            if package.get("version") != child.get("version") or package.get("name") != name:
                errors.append(f"npm {name}: installed version/name does not match npm ls")
                continue
            key = (name, child["version"], str(candidate))
            if key in seen:
                continue
            seen.add(key)
            count += 1
            license_id = package.get("license")
            if not isinstance(license_id, str) or not accepted("npm", name, license_id):
                errors.append(f"npm {name}@{child['version']}: {license_id or 'missing licence'}")
            walk(child, candidate)

    walk(tree, root)
    print(f"licences/npm ({project}): {count} production packages, {len(errors) - before} findings")


try:
    check_go()
    check_npm("web")
    # The CDK app's production tree, including aws-cdk-lib's bundled dependencies.
    check_npm("infra")
except (OSError, ValueError, subprocess.CalledProcessError) as exc:
    errors.append(f"licence checker could not finish: {exc}")
for error in errors:
    print(f"  {error}", file=sys.stderr)
sys.exit(bool(errors))
