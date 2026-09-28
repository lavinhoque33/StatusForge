#!/bin/sh
set -eu

# The caller (make demo) loads .env. Only the dedicated demo table is legal.
if [ "${STATUSFORGE_DYNAMODB_TABLE:-statusforge}" != statusforge_demo ]; then
  echo 'demo refuses any table other than statusforge_demo' >&2
  exit 1
fi
: "${STATUSFORGE_HTTP_ADDR:=127.0.0.1:8080}"
: "${STATUSFORGE_SAMPLE_TARGET_ADDR:=127.0.0.1:8090}"
: "${STATUSFORGE_RECEIVER_ADDR:=127.0.0.1:8091}"
: "${STATUSFORGE_SAMPLE_JOB_ADDR:=127.0.0.1:8092}"
: "${STATUSFORGE_DYNAMODB_ENDPOINT:=http://127.0.0.1:8000}"
: "${STATUSFORGE_SAMPLE_JOB_INTERVAL_SECONDS:=60}"
export STATUSFORGE_HTTP_ADDR STATUSFORGE_SAMPLE_TARGET_ADDR STATUSFORGE_RECEIVER_ADDR STATUSFORGE_SAMPLE_JOB_ADDR STATUSFORGE_DYNAMODB_ENDPOINT STATUSFORGE_SAMPLE_JOB_INTERVAL_SECONDS
case "$STATUSFORGE_HTTP_ADDR $STATUSFORGE_SAMPLE_TARGET_ADDR $STATUSFORGE_RECEIVER_ADDR $STATUSFORGE_SAMPLE_JOB_ADDR" in
  *0.0.0.0*|*':::'*) echo 'demo requires loopback listeners' >&2; exit 1 ;;
esac
STATUSFORGE_ALLOWED_TARGETS="${STATUSFORGE_SAMPLE_TARGET_ADDR}"
STATUSFORGE_NOTIFY_URL="http://${STATUSFORGE_RECEIVER_ADDR}/notify"
STATUSFORGE_SAMPLE_JOB_TOKEN="sfh_$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')"
export STATUSFORGE_ALLOWED_TARGETS STATUSFORGE_NOTIFY_URL STATUSFORGE_SAMPLE_JOB_TOKEN

work=$(mktemp -d)
pids=''
cleanup() {
  trap - EXIT INT TERM
  for pid in $pids; do kill "$pid" 2>/dev/null || :; done
  for pid in $pids; do wait "$pid" 2>/dev/null || :; done
  rm -rf "$work"
}
trap 'exit 130' INT
trap 'exit 143' TERM
trap cleanup EXIT
(
 cd backend
 go build -o "$work/sample-target" ./cmd/sample-target
 go build -o "$work/notification-receiver" ./cmd/notification-receiver
 go build -o "$work/sample-job" ./cmd/sample-job
 go build -o "$work/demo" ./cmd/demo
)
seed=$($work/demo)
case "$seed" in
  *heartbeat=*) heartbeat=${seed#*heartbeat=}; heartbeat=${heartbeat%% *} ;;
  *) echo 'demo seed did not return a heartbeat ID' >&2; exit 1 ;;
esac
STATUSFORGE_SAMPLE_JOB_REPORT_URL="http://${STATUSFORGE_HTTP_ADDR}/ingest/heartbeats/${heartbeat}"
export STATUSFORGE_SAMPLE_JOB_REPORT_URL
unset STATUSFORGE_SAMPLE_JOB_DEPLOY_URL STATUSFORGE_SAMPLE_JOB_DEPLOY_TOKEN
"$work/sample-target" & pids="$pids $!"
"$work/notification-receiver" & pids="$pids $!"
"${STATUSFORGE_DEMO_BINARY:-backend/bin/statusforge}" & pids="$pids $!"
"$work/sample-job" & pids="$pids $!"
printf '%s\n' "$seed" "demo live: API http://${STATUSFORGE_HTTP_ADDR}/; Ctrl-C stops the API and fixtures"
# All children remain foreground-owned by this script and are stopped by the exit trap.
for pid in $pids; do wait "$pid" || exit 1; done
