#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
regions_file="$repo_dir/internal/model/regions.json"
app=""
apply=false
start=false

usage() {
  cat <<'EOF'
Usage: ./scripts/update-fly-machines.sh --app APP [options]

Build one immutable Scout image and update the app's existing Machines in
place. The script never creates or deletes Machines or other Fly resources.

Options:
  --app APP   Fly app name (required).
  --apply     Build, push, and update. Without this flag, only validate/plan.
  --start     Start every updated Machine once for a validation run. Requires
              --apply; otherwise Machines remain stopped until their schedule.
  -h, --help  Show this help.

The script requires exactly one Machine in each configured region, permits only
iad, fra, lax, nrt, and sin, and refuses public IPs, services, mounts, standby
Machines, non-hourly schedules, or restart policies other than no.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --app)
      [[ $# -ge 2 ]] || { echo "--app requires a value" >&2; exit 2; }
      app="$2"
      shift 2
      ;;
    --apply)
      apply=true
      shift
      ;;
    --start)
      start=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "$app" ]]; then
  echo "--app is required." >&2
  usage >&2
  exit 2
fi
if [[ "$start" == true && "$apply" != true ]]; then
  echo "--start requires --apply." >&2
  exit 2
fi

if command -v flyctl >/dev/null 2>&1; then
  fly_cmd="$(command -v flyctl)"
elif command -v fly >/dev/null 2>&1; then
  fly_cmd="$(command -v fly)"
else
  echo "flyctl (or fly) is required." >&2
  exit 1
fi
for command in git go jq; do
  command -v "$command" >/dev/null 2>&1 || { echo "$command is required." >&2; exit 1; }
done

"$fly_cmd" auth whoami >/dev/null
machines="$("$fly_cmd" machine list --app "$app" --json)"
machine_count="$(jq 'length' <<<"$machines")"
expected_regions="$(jq -r '[.[] | select(.enabled) | .code] | sort | join(" ")' "$regions_file")"
region_count="$(jq '[.[] | select(.enabled)] | length' "$regions_file")"
if [[ "$machine_count" -ne "$region_count" ]]; then
  echo "Expected exactly $region_count Machines; found $machine_count. Refusing update." >&2
  exit 1
fi

inventory_errors="$(jq -r --slurpfile registry "$regions_file" '
  group_by(.region)[] |
  select(length != 1 or (.[0].region as $region | any($registry[0][]; .enabled and .code == $region) | not)) |
  "invalid region inventory: " + (map(.region) | join(","))
' <<<"$machines")"
regions="$(jq -r 'map(.region) | sort | join(" ")' <<<"$machines")"
if [[ -n "$inventory_errors" || "$regions" != "$expected_regions" ]]; then
  [[ -z "$inventory_errors" ]] || echo "$inventory_errors" >&2
  echo "Expected exactly one Machine in each enabled registry region ($expected_regions); found: $regions" >&2
  exit 1
fi

unsafe="$(jq -r '
  .[] |
  select(
    .config.schedule != "hourly" or
    .config.restart.policy != "no" or
    ((.config.services // []) | length) != 0 or
    ((.config.mounts // []) | length) != 0 or
    ((.config.standbys // []) | length) != 0 or
    .config.guest.cpu_kind != "shared" or
    .config.guest.cpus != 1 or
    .config.guest.memory_mb != 256
  ) |
  "unsafe Machine configuration in region \(.region)"
' <<<"$machines")"
if [[ -n "$unsafe" ]]; then
  echo "$unsafe" >&2
  exit 1
fi

ips="$("$fly_cmd" ips list --app "$app" --json)"
if [[ "$(jq 'length' <<<"$ips")" -ne 0 ]]; then
  echo "App $app has allocated public IPs; refusing update." >&2
  exit 1
fi

commit="$(git -C "$repo_dir" rev-parse --short=12 HEAD)"
echo "Validated app $app: one safe hourly Machine in each enabled registry region ($expected_regions)."
echo "Image tag: registry.fly.io/$app:$commit"
if [[ "$apply" != true ]]; then
  echo "Dry run; no Fly resources were changed. Rerun with --apply after review."
  exit 0
fi
if [[ -n "$(git -C "$repo_dir" status --porcelain)" ]]; then
  echo "Working tree is not clean; refusing to build an uncommitted image." >&2
  exit 1
fi

(
  cd "$repo_dir"
  go test ./...
  ./scripts/check-core-sync.sh
  "$fly_cmd" deploy . \
    --app "$app" \
    --build-only \
    --push \
    --image-label "$commit"
)

image="registry.fly.io/$app:$commit"
while IFS=$'\t' read -r machine_id region; do
  echo "Updating Machine in $region to $image"
  "$fly_cmd" machine update "$machine_id" \
    --app "$app" \
    --image "$image" \
    --env CONTINUOUS=false \
    --schedule hourly \
    --restart no \
    --skip-start \
    --yes
done < <(jq -r 'sort_by(.region)[] | [.id, .region] | @tsv' <<<"$machines")

updated="$("$fly_cmd" machine list --app "$app" --json)"
wrong_image="$(jq -r --arg tag "$commit" '.[] | select(.image_ref.tag != $tag) | .region' <<<"$updated")"
if [[ -n "$wrong_image" ]]; then
  echo "Some regions do not use image tag $commit: $wrong_image" >&2
  exit 1
fi

if [[ "$start" == true ]]; then
  while read -r machine_id; do
    "$fly_cmd" machine start "$machine_id" --app "$app"
  done < <(jq -r 'sort_by(.region)[].id' <<<"$updated")
  echo "Started one validation run in every region. Follow with: $fly_cmd logs --app $app"
else
  echo "Updated all $region_count Machines. They will start on their hourly schedules."
fi
