#!/bin/sh
# Runs from any directory. No AWS access; scanner network traffic is limited
# to Go modules, the Go vulnerability database and the npm registry.
set -u
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd) || exit 1
cd "$root" || exit 1
# Never fall through from a module proxy to VCS hosts or download a Go toolchain.
# The checked-in go.sum files still verify every fetched module.
GOPROXY=https://proxy.golang.org
GOSUMDB=off
GOTOOLCHAIN=local
export GOPROXY GOSUMDB GOTOOLCHAIN
scratch=$(mktemp -d) || exit 1
trap 'rm -rf "$scratch"' EXIT HUP INT TERM
failed=0

if (cd tools && go build -mod=readonly -o "$scratch/govulncheck" golang.org/x/vuln/cmd/govulncheck) >"$scratch/build-go.log" 2>&1 &&
    (cd backend && "$scratch/govulncheck" ./...) >"$scratch/go.log" 2>&1; then
    printf '%s\n' 'govulncheck (backend): PASS — no reachable vulnerabilities'
else
    printf '%s\n' 'govulncheck (backend): FAIL — findings or scanner error'
    cat "$scratch/go.log" 2>/dev/null || :
    cat "$scratch/build-go.log" 2>/dev/null || :
    failed=1
fi

if npm --prefix web audit --omit=dev --audit-level=low --registry=https://registry.npmjs.org >"$scratch/npm.log" 2>&1; then
    printf '%s\n' 'npm audit (web production, low and above): PASS'
else
    printf '%s\n' 'npm audit (web production, low and above): FAIL — findings or registry error'
    cat "$scratch/npm.log"
    failed=1
fi

# infra/ only: reasoned, version-pinned exceptions in security/audit-exceptions.json.
if python3 infrastructure/scripts/security/npm-audit.py >"$scratch/npm-infra.log" 2>&1; then
    printf '%s\n' 'npm audit (infra production, low and above): PASS'
    cat "$scratch/npm-infra.log"
else
    printf '%s\n' 'npm audit (infra production, low and above): FAIL — findings, stale exception, or registry error'
    cat "$scratch/npm-infra.log"
    failed=1
fi

if python3 infrastructure/scripts/security/licenses.py >"$scratch/licenses.log" 2>&1; then
    printf '%s\n' 'licence allowlist (production): PASS'
    cat "$scratch/licenses.log"
else
    printf '%s\n' 'licence allowlist (production): FAIL'
    cat "$scratch/licenses.log"
    failed=1
fi

if (cd tools && go build -mod=readonly -o "$scratch/gitleaks" github.com/zricethezav/gitleaks/v8) >"$scratch/build-leaks.log" 2>&1; then
    for scope in dir git; do
        if "$scratch/gitleaks" "$scope" --no-banner --redact --config infrastructure/scripts/security/gitleaks.toml . >"$scratch/$scope.log" 2>&1; then
            printf 'gitleaks (%s): PASS — no secrets\n' "$scope"
        else
            printf 'gitleaks (%s): FAIL — findings or scanner error\n' "$scope"
            cat "$scratch/$scope.log"
            failed=1
        fi
    done
else
    printf '%s\n' 'gitleaks (working tree and history): FAIL — scanner build error'
    failed=1
    cat "$scratch/build-leaks.log" 2>/dev/null || :
fi

if [ "$failed" -eq 0 ]; then
    printf '%s\n' 'security-check: PASS'
else
    printf '%s\n' 'security-check: FAIL'
fi
exit "$failed"
