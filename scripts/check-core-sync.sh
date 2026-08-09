#!/usr/bin/env bash
set -euo pipefail

scout_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
stats_dir="${STRATUMSTATS_DIR:-$scout_dir/../StratumStats}"

if [[ ! -d "$stats_dir/internal/model" || ! -d "$stats_dir/internal/probe" ]]; then
  echo "StratumStats source not found at $stats_dir" >&2
  exit 1
fi

model_files=(model.go protocol.go version.go observation_json.go observation_json_test.go)
for file in "${model_files[@]}"; do
  diff -u "$stats_dir/internal/model/$file" "$scout_dir/internal/model/$file"
done

for source in "$stats_dir"/internal/probe/*.go; do
  file="$(basename "$source")"
  diff -u \
    <(sed 's#github.com/proofofmike/stratumstats/internal/model#github.com/Distortions81/StratumScout/internal/model#g' "$source") \
    "$scout_dir/internal/probe/$file"
done

echo "StratumStats model and probe cores are in sync."
