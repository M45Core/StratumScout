#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
app=""
organization=""
create_app=false
apply=false

usage() {
  cat <<'EOF'
Usage: ./scripts/setup-fly-canary.sh --app APP [options]

Options:
  --app APP       Globally unique Fly app name (required).
  --org ORG       Fly organization slug, used with --create-app.
  --create-app    Create the Fly app before configuring it.
  --apply         Make changes. Without this flag, print a dry run only.
  -h, --help      Show this help.

For --apply, export INGEST_KEY_ID and INGEST_SECRET first. The script refuses
to create a canary if the app already contains any Machines or allocated IPs.
It creates exactly one hourly 256 MiB shared-CPU Machine in lax, with restart
policy no and no Fly service, port, volume, standby, or autoscaler.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --app)
      [[ $# -ge 2 ]] || { echo "--app requires a value" >&2; exit 2; }
      app="$2"
      shift 2
      ;;
    --org)
      [[ $# -ge 2 ]] || { echo "--org requires a value" >&2; exit 2; }
      organization="$2"
      shift 2
      ;;
    --create-app)
      create_app=true
      shift
      ;;
    --apply)
      apply=true
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
if [[ "$create_app" == true && -z "$organization" ]]; then
  echo "--org is required with --create-app." >&2
  exit 2
fi

if [[ "$apply" != true ]]; then
  echo "Dry run; no Fly resources will be changed."
  if [[ "$create_app" == true ]]; then
    echo "fly apps create $app --org $organization"
  fi
  echo "Verify that app $app has zero Machines and zero allocated IPs."
  echo "Import INGEST_KEY_ID and INGEST_SECRET from the current environment."
  echo "Build the repository image and create one hourly Machine named scout-lax in lax."
  echo "Resources: shared CPU, 1 CPU, 256 MiB, restart=no, no services, ports, volumes, or standby."
  echo "Rerun with --apply after reviewing this plan."
  exit 0
fi

for command in fly jq; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "$command is required." >&2
    exit 1
  fi
done
secret_value="${INGEST_SECRET:-}"
if [[ -z "${INGEST_KEY_ID:-}" || ${#secret_value} -lt 32 ]]; then
  echo "Export INGEST_KEY_ID and an INGEST_SECRET of at least 32 bytes first." >&2
  exit 1
fi

fly auth whoami >/dev/null
if [[ "$create_app" == true ]]; then
  fly apps create "$app" --org "$organization"
fi

machines="$(fly machine list --app "$app" --json)"
if [[ "$(jq 'length' <<<"$machines")" -ne 0 ]]; then
  echo "App $app already has Machines; refusing to create a duplicate canary." >&2
  exit 1
fi
ips="$(fly ips list --app "$app" --json)"
if [[ "$(jq 'length' <<<"$ips")" -ne 0 ]]; then
  echo "App $app already has allocated IPs; refusing canary provisioning." >&2
  exit 1
fi

printf 'INGEST_KEY_ID=%s\nINGEST_SECRET=%s\n' "$INGEST_KEY_ID" "$INGEST_SECRET" |
  fly secrets import --app "$app" --stage

(
  cd "$repo_dir"
  fly machine run . \
    --app "$app" \
    --name scout-lax \
    --region lax \
    --schedule hourly \
    --restart no \
    --vm-cpu-kind shared \
    --vm-cpus 1 \
    --vm-memory 256 \
    --env COLLECTOR_URL=https://stratumstats.m45core.com \
    --env RUN_FOR=14m
)

machines="$(fly machine list --app "$app" --json)"
if [[ "$(jq 'length' <<<"$machines")" -ne 1 || "$(jq -r '.[0].region' <<<"$machines")" != "lax" ]]; then
  echo "Unexpected Machine inventory after provisioning; inspect app $app immediately." >&2
  exit 1
fi
if [[ "$(jq 'length' <<<"$(fly ips list --app "$app" --json)")" -ne 0 ]]; then
  echo "Unexpected public IP allocation; inspect app $app immediately." >&2
  exit 1
fi

echo "Created the single lax canary Machine."
echo "Inspect with: fly machine list --app $app"
echo "Follow logs with: fly logs --app $app"
echo "Do not add dfw, ewr, or fra until the 48-hour canary gate passes."
