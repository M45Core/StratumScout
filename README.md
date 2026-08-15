# StratumScout

StratumScout is the disposable regional measurement probe for
[StratumStats](https://github.com/M45Core/StratumStats). It fetches a
collector-provided endpoint list, observes Stratum behavior continuously, and
uploads authenticated results after each block.

The probe is stateless. It cannot submit mining shares and has no listener,
database, volume, or durable local state.

Long-lived state is explicitly bounded. Upload buffering is capped at 2,000
observations, no more than 32 active block windows are retained for 30 seconds,
configured endpoint and session maps cannot exceed the validated endpoint list,
and completed-block deduplication retains only the latest 256 block IDs.

## Operating modes

By default, the process stays connected continuously. Each reporting cohort:

1. fetches the current collector configuration and establishes pool connections
   once at process startup;
2. assigns a random identifier to the current reporting cohort;
3. observes until a block transition is detected and decodes its BIP34 coinbase
   height when present;
4. keeps the block window open for 30 seconds from the first observation;
5. flushes block observations and publishes one terminal run record; and
6. rotates only the reporting run ID while the process and pool connections
   remain active for the next block.

Pool sessions are periodically refreshed for setup telemetry. Each process
chooses a connection age from 1 hour 45 minutes through 2 hours 15 minutes,
then waits for the next completed 30-second block window, publishes that block,
and immediately reconnects. The Scout process and current reporting cohort
remain alive across this planned refresh. The jitter avoids synchronized probe
reconnections while retaining roughly a dozen connect, TLS, subscribe, and
authorize samples per endpoint per day and placing the brief connection gap
immediately after a measured block.

This boundary is required by StratumStats: remote block observations affect
scores only after the matching terminal record proves that the cohort uploaded
without loss. An unexpected collector failure restarts the long-lived run with
exponential backoff capped at one minute. `SIGINT` and `SIGTERM` stop the
process.

Each uploaded block observation carries the decoded Bitcoin height when the
coinbase input contains a valid BIP34 height. StratumStats uses that value for
the selected region's block-height indicator; Scout does not query a separate
Bitcoin node for chain-tip state.

Scout timestamps each complete Stratum message immediately after the wire read,
before JSON parsing, coinbase reconstruction, or merkle verification. Validation
still decides whether a block-template arrival is accepted. Protocol response
timings use the same wire-completion boundary so parsing work is not attributed
to the pool.

## Configuration

| Variable | Required | Default | Purpose |
|---|---:|---:|---|
| `COLLECTOR_URL` | yes | — | HTTPS origin of the StratumStats collector |
| `INGEST_KEY_ID` | yes | — | Identifier for authenticated ingestion |
| `INGEST_SECRET` | yes | — | Ingest secret of at least 32 bytes |
| `FLY_REGION` | yes | — | Maps the Machine region to a reporting vantage |
| `FLY_MACHINE_ID` | yes | — | Binds authenticated envelopes to the Machine |
| `RUN_FOR` | no | `5m` | One-shot window when `CONTINUOUS=false`; ignored in continuous mode |
| `CONTINUOUS` | no | `true` | Stay active and publish a completed cohort after each block |
| `PROCESS_NICE` | no | `0` | Linux scheduler niceness from 0 through 19 |
| `FILTER_CONTINENTS` | no | `false` | Skip endpoints explicitly assigned to another continent |

Supported Fly mappings are `iad` to `us-east`, `fra` to `europe`, `lax` to
`us-west`, `nrt` to `japan`, and `sin` to `singapore`.
The embedded [`regions.json`](internal/model/regions.json) is synchronized with
StratumStats and controls which `FLY_REGION` values are accepted. Disabled
catalog entries remain documented but cannot upload measurements.

`PROCESS_NICE=10` is the recommended setting when Scout shares a Fly Machine
with a latency-sensitive BTCFlux edge. BTCFlux remains at its normal priority;
Scout voluntarily receives less CPU when both processes are runnable. A
non-zero value is rejected on non-Linux platforms.

Set `CONTINUOUS=false` only for a bounded one-shot diagnostic process. In the
production mode, `RUN_FOR` does not impose a periodic cutoff: Scout remains
connected until a block, reports approximately 30 seconds after the first
observation, and begins the next reporting cohort without reconnecting.

Never place ingest credentials in an image, `fly.toml`, ordinary Machine
environment, logs, or command-line arguments. Load them as Fly app secrets.
Fly exposes app secrets to every container in a multi-container Machine, so
co-location deliberately expands both processes' access to the combined app's
secret set.

## BTCFlux co-location

The production co-location layout uses independent scratch-based BTCFlux and
StratumScout images inside one 256 MiB Fly Machine in each of `iad`, `fra`,
`lax`, `nrt`, and `sin`. Fly Pilot supplies the multi-container init; neither runtime
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
