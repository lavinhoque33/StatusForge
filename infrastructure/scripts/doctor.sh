#!/bin/sh
# Reports whether the local prerequisites are available. Read-only; installs nothing.
# Exit status is non-zero when a required tool is missing so `make doctor` is usable in scripts.

status=0

check() {
  name="$1"; shift
  if out="$("$@" 2>&1 | head -n 1)"; then
    printf '  ok       %-16s %s\n' "$name" "$out"
  else
    printf '  MISSING  %-16s %s\n' "$name" "$out"
    status=1
  fi
}

printf 'StatusForge prerequisites\n'
check "go" go version
check "node" node --version
check "npm" npm --version
check "git" git --version
check "make" make --version
check "docker" docker --version
check "docker compose" docker compose version
check "docker daemon" docker info --format 'server {{.ServerVersion}}'

node_major="$(node --version 2>/dev/null | sed 's/^v\([0-9]*\).*/\1/')"
if [ -n "$node_major" ] && [ "$node_major" -lt 24 ]; then
  printf '\n  MISSING  node >= 24 required (found v%s); see .nvmrc\n' "$node_major"
  status=1
fi

exit "$status"
