#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

TMP_ROOT=".tmp/pipeline-positive"
BIN_DIR="$TMP_ROOT/bin"
REPORT_DIR="$TMP_ROOT/reports"
EVENTS_FILE="$TMP_ROOT/events.jsonl"
RUN_OUTPUT="$TMP_ROOT/run-output.txt"
SERVER_LOG="$TMP_ROOT/example-api.log"
CONSOLIDATED_REPORT="$TMP_ROOT/consolidated.json"
JUNIT_REPORT="$TMP_ROOT/junit.xml"
SCHEMA_FILE="$TMP_ROOT/spec.schema.json"

rm -rf "$TMP_ROOT" examples/project/artifacts
mkdir -p "$BIN_DIR" "$REPORT_DIR"

section() {
  printf '\n============================================================\n'
  printf '%s\n' "$1"
  printf '============================================================\n'
}

section "1/5 Build Veriflow and the local fixture API"
go build -o "$BIN_DIR/veriflow" ./cmd/veriflow
go build -o "$BIN_DIR/veriflow-example-api" ./cmd/example-api

section "2/5 Start deterministic local fixture API"
"$BIN_DIR/veriflow-example-api" --addr 127.0.0.1:18080 >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!
trap 'kill "$SERVER_PID" 2>/dev/null || true' EXIT

ready=0
for _ in $(seq 1 50); do
  if curl -fsS http://127.0.0.1:18080/health >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 0.1
done

if [ "$ready" -ne 1 ]; then
  echo "fixture API did not become ready" >&2
  cat "$SERVER_LOG" >&2 || true
  exit 1
fi
printf 'Fixture API is ready on http://127.0.0.1:18080\n'

section "3/5 Validate and discover the positive example project"
"$BIN_DIR/veriflow" validate project examples/project
"$BIN_DIR/veriflow" discover suites examples/project
"$BIN_DIR/veriflow" discover requests examples/project
"$BIN_DIR/veriflow" discover environments examples/project
"$BIN_DIR/veriflow" discover tests examples/project --json >/dev/null
"$BIN_DIR/veriflow" schema --output "$SCHEMA_FILE"

section "4/5 Run all discovered suites"
export VERIFLOW_TEST_SECRET="pipeline-secret-123"
"$BIN_DIR/veriflow" run discovered examples/project \
  -e ci \
  --ci \
  --cookie-jar \
  --var-file examples/project/fixtures/overrides.yml \
  --var 'pipelineName=ci-command-line' \
  --secret-var-from-env 'tokenValue=VERIFLOW_TEST_SECRET' \
  --report-consolidated-json "$CONSOLIDATED_REPORT" \
  --report-junit "$JUNIT_REPORT" \
  --report-dir "$REPORT_DIR" \
  --event-jsonl "$EVENTS_FILE" | tee "$RUN_OUTPUT"

section "5/5 Verify pipeline outputs and consolidated totals"

test -s "$EVENTS_FILE"
test -s "$CONSOLIDATED_REPORT"
test -s "$JUNIT_REPORT"
test -s "$SCHEMA_FILE"
grep -q '"$schema"' "$SCHEMA_FILE"
grep -q '<testsuites' "$JUNIT_REPORT"
grep -q '"totalTests": 18' "$CONSOLIDATED_REPORT"
grep -q '"statusCode": 0' "$CONSOLIDATED_REPORT"
test -s examples/project/artifacts/pipeline/text-response.txt
test -s examples/project/artifacts/pipeline/text-headers.json
test -s examples/project/artifacts/pipeline/text-timing.json

report_count="$(find "$REPORT_DIR" -type f -name '*.json' | wc -l | tr -d '[:space:]')"
if [ "$report_count" -ne 9 ]; then
  echo "expected 9 suite reports, got $report_count" >&2
  exit 1
fi

consolidated="$(awk '/=== Consolidated Test Results ===/{capture=1} capture' "$RUN_OUTPUT")"
grep -Eq 'Suites:[[:space:]]+9' <<<"$consolidated"
grep -Eq 'Total tests:[[:space:]]+18' <<<"$consolidated"
grep -Eq 'Passed:[[:space:]]+17' <<<"$consolidated"
grep -Eq 'Failed:[[:space:]]+0' <<<"$consolidated"
grep -Eq 'Skipped:[[:space:]]+1' <<<"$consolidated"
grep -Eq 'Status:[[:space:]]+passed' <<<"$consolidated"

# CLI overlay precedence: --var beats --var-file, while nested values from the
# variable file override the environment defaults.
grep -q 'ci-command-line' "$REPORT_DIR/02_Request_shapes.json"
grep -q 'value=9' "$REPORT_DIR/02_Request_shapes.json"

# Secrets imported from process environment must never appear in pipeline output,
# events, per-suite reports, aggregate reports, or JUnit.
if grep -R --binary-files=without-match -q 'pipeline-secret-123' "$TMP_ROOT"; then
  echo 'secret value leaked into pipeline output' >&2
  exit 1
fi
grep -q '*******' "$REPORT_DIR/02_Request_shapes.json"

printf '\nPositive pipeline smoke test passed.\n'
printf 'Reports: %s\n' "$REPORT_DIR"
printf 'Events:  %s\n' "$EVENTS_FILE"
printf 'JUnit:  %s\n' "$JUNIT_REPORT"
printf 'Combined report: %s\n' "$CONSOLIDATED_REPORT"
printf 'API log: %s\n' "$SERVER_LOG"

