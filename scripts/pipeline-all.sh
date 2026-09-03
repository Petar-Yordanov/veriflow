#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

printf '\n################ POSITIVE PIPELINE ################\n'
bash ./scripts/pipeline-smoke.sh

printf '\n################ NEGATIVE CONTRACTS ################\n'
bash ./scripts/pipeline-negative.sh

printf '\nAll pipeline checks passed.\n'

