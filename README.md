# StratumScout

StratumScout is the disposable regional measurement probe for
[StratumStats](https://github.com/M45Core/StratumStats). It fetches a
collector-provided endpoint list, observes Stratum behavior continuously, and
uploads authenticated results after each block.

The probe is stateless. It cannot submit mining shares and has no listener,
database, volume, or durable local state.

Long-lived state is explicitly bounded. Scout retains at most one pending setup
result per operation and endpoint, one coinbase source per observed endpoint in
no more than 32 active block windows for 30 seconds, endpoint and session maps
no larger than the validated configuration, and only the latest 256 completed
block IDs. It has no timed upload buffer.

## Operating modes

By default, the process stays connected continuously. For each Bitcoin block it:

1. establishes the configured pool connections;
2. timestamps the first clean previous-block-hash transition from each endpoint
   as soon as the message's first byte is readable;
3. keeps the block window open for 30 seconds from the first observation;
4. places those timestamps, the webpage-required coinbase source, and any
   pending setup timings into one nested block sample; and
5. makes one authenticated collector request for that sample.

If Bitcoin blocks arrive less than 30 seconds apart, their overlapping windows
remain independent and each produces exactly one block upload when its window
closes. A block sample is never split, queued, or retried. If the collector is
unavailable, that block is dropped and the Stratum sessions continue unchanged.

Connect, TLS, subscribe, and authorize timings are held only until the next
block. Each Scout chooses a connection age from 1 hour 45 minutes through 2
hours 15 minutes. After reaching that age, it waits for the next completed
30-second block window, uploads that block, and recreates its pool sessions.
The jitter keeps regional Scouts from reconnecting together, while the block
boundary keeps the brief planned gap outside an active measurement window.
Ordinary disconnects still reconnect with bounded backoff. Multiple attempts
before one block collapse to the latest connection path. Scout does not send
Stratum ping requests, and an idle authorized session remains blocked on its
network read. Collector configuration is fetched once at process startup and
changes take effect when Scout restarts.

Each accepted request is the completion proof for its entire block sample; no
separate protocol or terminal records are uploaded. An unexpected observation
loop failure restarts the long-lived process. Repeated endpoint failures back
off to 15 minutes and reset only after a session remains stable for 10 minutes.
`SIGINT` and `SIGTERM` stop the process.

Scout timestamps each Stratum message as soon as its first byte is readable,
before waiting for the remaining bytes and before JSON parsing. For
`mining.notify`, it extracts the previous-block hash and `clean_jobs` flag.
Only after accepting a new block does it copy `coinbase1` and `coinbase2` into
the block sample with the subscribed extranonce context and a SHA-256 hash of
the generated worker output script. It does not decode the transaction or
retain merkle branches, version, difficulty, time, or other job fields.
Same-hash job updates are ignored without copying their coinbases.
StratumStats derives the webpage's height, payout, and solo-fee fields and
calculates relative arrival offsets after authenticated ingest. Protocol
response timings use the same first-byte boundary so message length and parsing
work are not attributed to the pool.

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

Supported Fly mappings are `ewr` to `us-east`, `fra` to `europe`, `lax` to
`us-west`, `nrt` to `japan`, and `sin` to `singapore`.
The embedded [`regions.json`](internal/model/regions.json) is synchronized with
StratumStats and controls which `FLY_REGION` values are accepted. Disabled
catalog entries remain documented but cannot upload measurements.

`PROCESS_NICE=0` is used in the dedicated production Fly app. A higher value is
useful only for an intentional diagnostic co-location with a latency-sensitive
process. A non-zero value is rejected on non-Linux platforms.

Set `CONTINUOUS=false` only for a bounded one-shot diagnostic process. In the
production mode, `RUN_FOR` does not impose a periodic cutoff: Scout remains
connected until a block, reports approximately 30 seconds after the first
observation, and continues waiting with planned session refreshes after safe
block boundaries.

Never place ingest credentials in an image, `fly.toml`, ordinary Machine
environment, logs, or command-line arguments. Load them as Fly app secrets.
Fly exposes app secrets to every container in a multi-container Machine, so
co-location deliberately expands both processes' access to the combined app's
secret set.

## Fly deployment

The operator-owned Fly deployment is a dedicated StratumScout app with
continuous Machines in FRA and LAX. It does not share Machines, images, or
secrets with BTCFlux. The EWR Scout remains an independent VPS deployment;
NRT and SIN remain valid measurement vantages but are not in this Fly
inventory.

[`deploy/fly-regions.json`](deploy/fly-regions.json) is the Fly Machine source
of truth. See the [Fly deployment runbook](deploy/FLY_DEPLOYMENT.md) for the
immutable-image, region-by-region migration, validation, update, and rollback
procedure. General measurement design and collector operations belong in the
main StratumStats repository.

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
