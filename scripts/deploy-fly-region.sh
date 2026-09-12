#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
app=""
region=""
image=""
regions_file="$repo_dir/deploy/fly-regions.json"
apply=false

usage() {
  cat <<'EOF'
Usage: ./scripts/deploy-fly-region.sh --app APP --region REGION --image IMAGE [options]

Create one missing continuous StratumScout Machine in a dedicated Fly app.
This is a read-only preflight unless --apply is supplied.

Options:
  --app APP           Existing Fly app name (required)
  --region REGION     Enabled deployment region to create (required)
  --image IMAGE       Digest-qualified immutable image reference (required)
  --regions-file FILE Deployment manifest (default: deploy/fly-regions.json)
  --apply             Create and validate the regional Machine
  -h, --help          Show this help

The target app must contain only dedicated StratumScout Machines and the staged
INGEST_KEY_ID and INGEST_SECRET names. The script never reads secret values.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --app) app="$2"; shift 2 ;;
    --region) region="$2"; shift 2 ;;
    --image) image="$2"; shift 2 ;;
    --regions-file) regions_file="$2"; shift 2 ;;
    --apply) apply=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ -n "$app" ]] || { echo "--app is required." >&2; exit 2; }
[[ -n "$region" ]] || { echo "--region is required." >&2; exit 2; }
[[ "$image" == *@sha256:* ]] || {
  echo "--image must include an immutable sha256 digest." >&2
  exit 2
}

for command in jq; do
  command -v "$command" >/dev/null 2>&1 || { echo "$command is required." >&2; exit 1; }
done
if command -v flyctl >/dev/null 2>&1; then
  fly_cmd="$(command -v flyctl)"
elif command -v fly >/dev/null 2>&1; then
  fly_cmd="$(command -v fly)"
else
  echo "flyctl (or fly) is required." >&2
  exit 1
fi

jq -e '
  type == "array" and length > 0 and
  (map(.code) | length == (unique | length)) and
  all(.[].code; type == "string" and length > 0) and
  all(.[] | select(.enabled);
    (.city | type == "string" and length > 0) and
    (.country | type == "string" and length > 0) and
    (.vm_cpus | type == "number" and floor == . and . >= 1) and
    (.vm_memory_mb | type == "number" and floor == .) and
    (.vm_memory_mb >= (.vm_cpus * 256)))
' "$regions_file" >/dev/null || {
  echo "Invalid deployment manifest: $regions_file" >&2
  exit 1
}
entry="$(jq -cer --arg region "$region" '.[] | select(.enabled and .code == $region)' "$regions_file")" || {
  echo "Region $region is not enabled in $regions_file." >&2
  exit 1
}

"$fly_cmd" auth whoami >/dev/null
secret_names="$("$fly_cmd" secrets list --app "$app" --json | jq '[.[] | (.Name // .name)]')"
for secret in INGEST_KEY_ID INGEST_SECRET; do
  jq -e --arg secret "$secret" 'index($secret) != null' <<<"$secret_names" >/dev/null || {
    echo "App $app is missing staged secret $secret." >&2
    exit 1
  }
done

if [[ "$("$fly_cmd" ips list --app "$app" --json | jq 'length')" -ne 0 ]]; then
  echo "App $app has allocated public IPs; refusing deployment." >&2
  exit 1
fi
machines="$("$fly_cmd" machine list --app "$app" --json)"
unexpected="$(jq -r --slurpfile registry "$regions_file" '
  .[] |
  select(.region as $region | all($registry[0][]; .code != $region)) |
  "\(.id) (\(.region))"
' <<<"$machines")"
if [[ -n "$unexpected" ]]; then
  echo "Unexpected Machines outside the deployment manifest: $unexpected" >&2
  exit 1
fi
duplicates="$(jq -r 'group_by(.region)[] | select(length > 1) | .[0].region' <<<"$machines")"
if [[ -n "$duplicates" ]]; then
  echo "Duplicate regional Machines: $duplicates" >&2
  exit 1
fi
if jq -e --arg region "$region" 'any(.[]; .region == $region)' <<<"$machines" >/dev/null; then
  echo "App $app already has a Machine in $region; refusing a duplicate." >&2
  exit 1
fi

vm_cpus="$(jq -r '.vm_cpus' <<<"$entry")"
vm_memory_mb="$(jq -r '.vm_memory_mb' <<<"$entry")"
echo "Plan: create scout-$region in app=$app from $image"
echo "Shape: region=$region shared-cpu=${vm_cpus}x memory=${vm_memory_mb}MiB restart=always continuous=true"
echo "Network/storage: no services, public IPs, volumes, schedule, standby, or autostop"
if [[ "$apply" != true ]]; then
  echo "Dry run; no Fly resources were changed. Rerun with --apply after review."
  exit 0
fi

"$fly_cmd" machine run "$image" \
  --app "$app" \
  --name "scout-$region" \
  --region "$region" \
  --autostart=false \
  --autostop=off \
  --restart always \
  --vm-cpu-kind shared \
  --vm-cpus "$vm_cpus" \
  --vm-memory "$vm_memory_mb" \
  --env COLLECTOR_URL=https://stratumstats.m45core.com \
  --env CONTINUOUS=true \
  --env PROCESS_NICE=0

target="$("$fly_cmd" machine list --app "$app" --json | jq -cer --arg region "$region" '[.[] | select(.region == $region)] | select(length == 1) | first')" || {
  echo "Expected exactly one $region Machine after creation; inspect app $app immediately." >&2
  exit 1
}
unsafe="$(jq -r --arg image "$image" --argjson cpus "$vm_cpus" --argjson memory "$vm_memory_mb" '
  select(
    .state != "started" or
    .config.schedule != null or
    .config.restart.policy != "always" or
    ((.config.services // []) | length) != 0 or
    ((.config.mounts // []) | length) != 0 or
    ((.config.standbys // []) | length) != 0 or
    .config.env.COLLECTOR_URL != "https://stratumstats.m45core.com" or
    .config.env.CONTINUOUS != "true" or
    .config.env.PROCESS_NICE != "0" or
    .config.guest.cpu_kind != "shared" or
    .config.guest.cpus != $cpus or
    .config.guest.memory_mb != $memory or
    ((.image_ref.repository + ":" + .image_ref.tag + "@" + .image_ref.digest) != ($image | sub("^registry.fly.io/"; "")))
  ) |
  "unsafe Machine configuration: \(.id) (\(.region))"
' <<<"$target")"
if [[ -n "$unsafe" ]]; then
  echo "$unsafe" >&2
  exit 1
fi
echo "Created and validated continuous StratumScout Machine in $region."
