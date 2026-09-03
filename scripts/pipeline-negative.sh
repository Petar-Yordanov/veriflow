#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

TMP_ROOT=".tmp/pipeline-negative"
BIN_DIR="$TMP_ROOT/bin"
LOG_DIR="$TMP_ROOT/logs"

rm -rf "$TMP_ROOT"
mkdir -p "$BIN_DIR" "$LOG_DIR"

go build -o "$BIN_DIR/veriflow" ./cmd/veriflow

run_expected_failure() {
  local name="$1"
  local expected_exit="$2"
  shift 2

  local log_file="$LOG_DIR/$name.log"

  set +e
  "$@" >"$log_file" 2>&1
  local actual_exit=$?
  set -e

  if [ "$actual_exit" -ne "$expected_exit" ]; then
    printf '[FAIL] %s: expected exit %s, got %s\n' "$name" "$expected_exit" "$actual_exit" >&2
    printf '\n--- command output ---\n' >&2
    cat "$log_file" >&2 || true
    exit 1
  fi

  printf '[PASS] %-34s rejected as expected (exit %s)\n' "$name" "$expected_exit"
}

printf 'Expected-failure contract checks\n'
printf 'These commands are intentionally invalid. Their raw output is captured under %s.\n\n' "$LOG_DIR"

run_expected_failure \
  legacy-request-reference \
  2 \
  "$BIN_DIR/veriflow" validate file examples/negative/legacy-request-reference.yml --project-root examples/project

run_expected_failure \
  conflicting-body-modes \
  2 \
  "$BIN_DIR/veriflow" validate file examples/negative/conflicting-body-modes.yml --project-root examples/project

run_expected_failure \
  variable-cycle \
  3 \
  "$BIN_DIR/veriflow" run suite examples/negative/variable-cycle.yml --project-root examples/project -e ci

run_expected_failure \
  body-file-traversal \
  2 \
  "$BIN_DIR/veriflow" run suite examples/negative/body-file-traversal.yml --project-root examples/project -e ci

run_expected_failure \
  environment-path-selector \
  4 \
  "$BIN_DIR/veriflow" run suite examples/project/suites/01-core.yml --project-root examples/project -e examples/project/environments/ci.yml

printf '\nAll expected-failure contract checks passed.\n'

