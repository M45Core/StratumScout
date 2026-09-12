# Dedicated Fly deployment

The production Fly deployment uses a dedicated StratumScout app. It contains
one continuous Scout Machine for every enabled entry in
[`fly-regions.json`](fly-regions.json). The shared StratumStats/StratumScout
region catalog still defines every valid reporting vantage; this smaller
deployment manifest defines only the operator-owned Fly Machines.

The EWR Scout remains an independent VPS deployment. NRT and SIN remain valid
measurement vantages but are not part of this Fly inventory.

Each Fly Machine has one shared CPU, 256 MiB of memory, restart policy
`always`, an ephemeral root filesystem, and no schedule, service, public IP,
volume, standby, or autostop. The process runs continuously with normal
scheduler priority because it does not share a VM with BTCFlux.

The app has only these secrets:

- `INGEST_KEY_ID`
- `INGEST_SECRET`

Fly cannot reveal or copy secret values between apps. Import the installed
StratumStats ingest pair from a trusted environment without printing it, then
confirm only the secret names. The BTCFlux helper accepts an explicit target:

```sh
./scripts/import-scout-ingest-secrets.sh --app m45-stratumscout
./scripts/import-scout-ingest-secrets.sh --app m45-stratumscout --apply
fly secrets list --app m45-stratumscout
```

## Immutable image

Build and push only a clean, committed, upstream-matched StratumScout revision.
Record the resulting tag and digest. Deployment requires the digest-qualified
reference and rejects a floating tag:

```sh
commit="$(git rev-parse --short=12 HEAD)"
fly deploy . \
  --app m45-stratumscout \
  --build-only \
  --push \
  --image-label "scout-$commit"

SCOUT_IMAGE="registry.fly.io/m45-stratumscout:scout-$commit@sha256:<digest>"
```

## Region-by-region deployment

The deployment script is a dry run unless `--apply` is supplied. It creates
only the requested missing region and refuses duplicate regions, unexpected
Machines, allocated public IPs, missing secret names, mutable image references,
or an unsafe Machine shape.

```sh
./scripts/deploy-fly-region.sh \
  --app m45-stratumscout \
  --region lax \
  --image "$SCOUT_IMAGE"
```

For a migration from another app, stop the old regional Scout immediately
before applying the new Machine so the collector does not receive duplicate
regional samples. Keep the stopped Machine for rollback until its replacement
has completed a healthy block report.

```sh
./scripts/deploy-fly-region.sh \
  --app m45-stratumscout \
  --region lax \
  --image "$SCOUT_IMAGE" \
  --apply
```

Validate the Machine shape, stable pool sessions, a successful block upload,
fresh StratumStats vantage health, and the absence of OOM exits. Repeat the
same cutover for FRA only after LAX passes.

Destroy the stopped legacy Machines only after both replacements are healthy.
Removing `INGEST_KEY_ID` and `INGEST_SECRET` from the BTCFlux app is a separate
cleanup step because changing app secrets can restart its relay Machines.

## Routine updates

Build a new digest-qualified image, deploy it to LAX first, and validate a
completed block report before updating FRA. Never use `latest` or another
floating tag. Keep the prior image reference and stopped Machine configuration
until rollback is no longer required.
