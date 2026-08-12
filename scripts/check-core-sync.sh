#!/usr/bin/env bash
set -euo pipefail

scout_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
stats_dir="${STRATUMSTATS_DIR:-$scout_dir/../StratumStats}"

if [[ ! -d "$stats_dir/internal/model" ]]; then
  echo "StratumStats source not found at $stats_dir" >&2
  exit 1
fi

model_files=(model.go protocol.go version.go regions.go regions.json observation_json.go observation_json_test.go)
for file in "${model_files[@]}"; do
  diff -u "$stats_dir/internal/model/$file" "$scout_dir/internal/model/$file"
done

# StratumScout owns the hardened network-facing probe implementation. Do not
# overwrite it from StratumStats, whose optional local collector may evolve on
# a different schedule.
echo "StratumStats observation model is in sync; StratumScout owns the probe core."
