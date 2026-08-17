# StratumScout

StratumScout is the disposable regional measurement probe for
[StratumStats](https://github.com/M45Core/StratumStats). It fetches a
collector-provided endpoint list, observes Stratum behavior continuously, and
uploads authenticated results after each block.

The probe is stateless. It cannot submit mining shares and has no listener,
database, volume, or durable local state.

Long-lived state is explicitly bounded. Scout retains at most one pending setup
result per operation and endpoint, no more than 32 active block windows for 30
seconds, endpoint and session maps no larger than the validated configuration,
and only the latest 256 completed block IDs. It has no timed upload buffer.

## Operating modes

By default, the process stays connected continuously. For each Bitcoin block it:

1. establishes the configured pool connections;
2. timestamps the first clean previous-block-hash transition from each endpoint
   as soon as the message's first byte is readable;
3. keeps the block window open for 30 seconds from the first observation;
4. places those timestamps and any pending setup timings into one nested block
   sample; and
5. makes one authenticated collector request for that sample.

If Bitcoin blocks arrive less than 30 seconds apart, their overlapping windows
remain independent and each produces exactly one block upload when its window
closes. A block sample is never split, queued, or retried. If the collector is
unavailable, that block is dropped and the Stratum sessions continue unchanged.

Connect, TLS, subscribe, and authorize timings are held only until the next
block. Sessions reconnect only after an actual disconnect; Scout does not tear
down healthy sessions to manufacture setup samples. If no reconnect operation
occurred, the corresponding JSON field is omitted rather than populated with
stale data. Multiple attempts before one block collapse to the latest connection
path. Scout does not send Stratum ping requests, and an idle authorized session
remains blocked on its network read. Collector configuration is fetched once at
process startup and changes take effect when Scout restarts.

Each accepted request is the completion proof for its entire block sample; no
separate protocol or terminal records are uploaded. An unexpected observation
loop failure restarts the long-lived process. Repeated endpoint failures back
off to 15 minutes and reset only after a session remains stable for 10 minutes.
`SIGINT` and `SIGTERM` stop the process.

Scout timestamps each Stratum message as soon as its first byte is readable,
before waiting for the remaining bytes and before JSON parsing. For
`mining.notify`, it extracts only the previous-block hash and `clean_jobs` flag;
coinbase, merkle, version, difficulty, time, and extranonce fields are neither
decoded nor uploaded. Same-hash job updates are ignored regardless of
transaction changes. StratumStats calculates relative arrival offsets after
authenticated ingest. Protocol response timings use the same first-byte
boundary so message length and parsing work are not attributed to the pool.

## Configuration

| Variable | Required | Default | Purpose |
|---|---:|---:|---|
| `COLLECTOR_URL` | yes | — | HTTPS origin of the StratumStats collector |
| `INGEST_KEY_ID` | yes | — | Identifier for authenticated ingestion |
| `INGEST_SECRET` | yes | — | Ingest secret of at least 32 bytes |
| `FLY_REGION` | yes | — | Maps the Machine region to a reporting vantage |
| `RUN_FOR` | no | `5m` | One-shot window when `CONTINUOUS=false`; ignored in continuous mode |
| `CONTINUOUS` | no | `true` | Stay active and publish one sample after each block |
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
observation, and continues waiting on the same sessions without reconnecting.

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
