# StratumScout

StratumScout is the disposable regional measurement probe for
[StratumStats](https://github.com/M45Core/StratumStats). It fetches a
collector-provided endpoint list, observes Stratum behavior for a bounded
window, and uploads authenticated results. The process can execute one cohort
or continue with another bounded cohort.

The probe is stateless. It cannot submit mining shares and has no listener,
database, volume, or durable local state.

## Operating modes

By default, the process runs continuously. It executes bounded runs back to
back rather than creating one unbounded run. Each cohort:

1. fetches the current collector configuration;
2. receives a new random run identifier;
3. observes endpoints for `RUN_FOR`;
4. flushes pending uploads and publishes one terminal run record; and
5. waits one second before starting the next cohort.

This boundary is required by StratumStats: remote block observations affect
scores only after the matching terminal record proves that the cohort uploaded
without loss. Failed cohorts retry with exponential backoff capped at one
minute. `SIGINT` and `SIGTERM` cancel the active cohort and stop the process.

## Configuration

| Variable | Required | Default | Purpose |
|---|---:|---:|---|
| `COLLECTOR_URL` | yes | — | HTTPS origin of the StratumStats collector |
| `INGEST_KEY_ID` | yes | — | Identifier for authenticated ingestion |
| `INGEST_SECRET` | yes | — | Ingest secret of at least 32 bytes |
| `FLY_REGION` | yes | — | Maps the Machine region to a reporting vantage |
| `FLY_MACHINE_ID` | yes | — | Binds authenticated envelopes to the Machine |
| `RUN_FOR` | no | `5m` | Observation window; must be greater than zero and at most `14m` |
| `CONTINUOUS` | no | `true` | Start a new bounded cohort after the previous one completes |
| `PROCESS_NICE` | no | `0` | Linux scheduler niceness from 0 through 19 |
| `FILTER_CONTINENTS` | no | `false` | Skip endpoints explicitly assigned to another continent |

Supported Fly mappings are `lax` to `us-west`, `dfw` to `us-central`, `iad`
and `ewr` to `us-east`, and `fra` to `europe`.

`PROCESS_NICE=10` is the recommended setting when Scout shares a Fly Machine
with a latency-sensitive BTCFlux edge. BTCFlux remains at its normal priority;
Scout voluntarily receives less CPU when both processes are runnable. A
non-zero value is rejected on non-Linux platforms.

Set `CONTINUOUS=false` for a one-shot process that exits after one completed
cohort. The legacy hourly Fly fleet pins this override explicitly in
`fly.toml`, its setup script, and its update script. Do not remove that override
unless the Machine schedule and restart policy are also being replaced.

Never place ingest credentials in an image, `fly.toml`, ordinary Machine
environment, logs, or command-line arguments. Load them as Fly app secrets.
Fly exposes app secrets to every container in a multi-container Machine, so
co-location deliberately expands both processes' access to the combined app's
secret set.

## BTCFlux co-location

The production co-location layout uses independent scratch-based BTCFlux and
StratumScout images inside one 256 MiB Fly Machine in each of `iad`, `dfw`,
`lax`, and `fra`. Fly Pilot supplies the multi-container init; neither runtime
image contains Alpine or a shell.

The complete build, migration, validation, upgrade, and rollback procedure is
maintained in the BTCFlux
[Scout sidecar runbook](https://github.com/Distortions81/BTCFlux/blob/main/deploy/SCOUT_SIDECAR.md).
General measurement design and collector operations belong in the main
StratumStats repository.

## Development

Requires Go 1.26 or newer and has no third-party Go dependencies.

```sh
go test ./...
go vet ./...
./scripts/check-core-sync.sh
git diff --check
```

`check-core-sync.sh` verifies that the copied observation and ingest contract
still matches the adjacent StratumStats checkout. StratumScout owns its
hardened network-facing probe implementation; do not replace that package with
the optional local collector from StratumStats.
