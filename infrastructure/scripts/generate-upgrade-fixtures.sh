#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
cd "$root"
case "${1:-all}" in all) milestones='1 2 3 4 5';; 1|2|3|4|5) milestones="$1";; *) echo 'usage: generate-upgrade-fixtures.sh [1|2|3|4|5|all]' >&2; exit 2;; esac
for n in $milestones; do
  case "$n" in 1) revision=63372db;; 2) revision=38dcaf4;; 3) revision=33e8717;; 4) revision=4091a2c;; 5) revision=db7ab1d;; esac
  table="statusforge_m6fix_m$n"
  tmp=$(mktemp -d)
  active=no
  api= target= receiver=
  cleanup() {
    for pid in "$api" "$target" "$receiver"; do if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || :; wait "$pid" 2>/dev/null || :; fi; done
    if [ "$active" = yes ]; then (cd "$root/backend" && go run ./cmd/upgrade-fixture-table delete "$table") || :; fi
    rm -rf "$tmp"
  }
  trap cleanup EXIT INT TERM
  (cd backend && go run ./cmd/upgrade-fixture-table check "$table")
  git archive "$revision" | tar -x -C "$tmp"
  (cd "$tmp/backend" && go build -o "$tmp/api" ./cmd/statusforge && go build -o "$tmp/target" ./cmd/sample-target)
  if [ "$n" -ge 3 ]; then (cd "$tmp/backend" && go build -o "$tmp/receiver" ./cmd/notification-receiver); fi
  STATUSFORGE_SAMPLE_TARGET_ADDR=127.0.0.1:18090 "$tmp/target" >"$tmp/target.log" 2>&1 & target=$!
  if [ "$n" -ge 3 ]; then STATUSFORGE_RECEIVER_ADDR=127.0.0.1:18091 "$tmp/receiver" >"$tmp/receiver.log" 2>&1 & receiver=$!; fi
  scheduler=true
  if [ "$n" -eq 3 ]; then scheduler=false; fi
  active=yes
  start_api() {
    STATUSFORGE_HTTP_ADDR=127.0.0.1:18082 \
      STATUSFORGE_DYNAMODB_ENDPOINT=http://127.0.0.1:8000 STATUSFORGE_DYNAMODB_TABLE="$table" \
      STATUSFORGE_DYNAMODB_REGION=local STATUSFORGE_DYNAMODB_ACCESS_KEY_ID=local \
      STATUSFORGE_DYNAMODB_SECRET_ACCESS_KEY=local STATUSFORGE_ALLOWED_TARGETS=127.0.0.1:18090 \
      STATUSFORGE_NOTIFY_URL=http://127.0.0.1:18091/notify STATUSFORGE_MIN_INTERVAL_SECONDS=10 \
      STATUSFORGE_DELIVERY_RETRY_SCHEDULE=1s STATUSFORGE_LIVENESS_INTERVAL_SECONDS=2 STATUSFORGE_SCHEDULER_ENABLED="$scheduler" \
      "$tmp/api" >"$tmp/api.log" 2>&1 & api=$!
  }
  start_api
  if ! python3 infrastructure/scripts/generate-upgrade-fixtures.py "$n" "$tmp/facts.json" --scene-only; then
    cat "$tmp/api.log" "$tmp/target.log" >&2
    exit 1
  fi
  if [ "$n" -ge 2 ]; then
    kill "$api"; wait "$api" || :; api=
    sleep 24
    scheduler=true
    start_api
    python3 infrastructure/scripts/generate-upgrade-facts.py "$n" "$tmp/facts.json" --wait-gap
    kill "$api"; wait "$api" || :; api=
    scheduler=false
    start_api
  fi
  python3 infrastructure/scripts/generate-upgrade-facts.py "$n" "$tmp/facts.json"
  kill "$api"; wait "$api" || :; api=
  kill "$target"; wait "$target" || :; target=
  if [ -n "$receiver" ]; then kill "$receiver"; wait "$receiver" || :; receiver=; fi
  mkdir -p backend/internal/upgrade/testdata
  (cd backend && STATUSFORGE_DYNAMODB_ENDPOINT=http://127.0.0.1:8000 go run ./cmd/statusforge export --table "$table" --out "$tmp/export.jsonl")
  cp "$tmp/export.jsonl" "backend/internal/upgrade/testdata/m$n.jsonl"
  cp "$tmp/facts.json" "backend/internal/upgrade/testdata/m$n.facts.json"
  bytes=$(wc -c < "backend/internal/upgrade/testdata/m$n.jsonl")
  if [ "$bytes" -gt 1048576 ]; then echo "fixture m$n exceeds 1 MiB" >&2; exit 1; fi
  cleanup
  trap - EXIT INT TERM
done
