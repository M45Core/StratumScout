# Repository purpose

StratumScout is a featherweight regional timestamp-and-forward probe. Its job is
limited to:

1. maintaining finite connections to the configured Stratum endpoints;
2. timestamping setup events and `mining.notify` receipt when the first byte is
   readable, before waiting for the rest of the message;
3. recognizing a clean new-block transition from the previous-block hash while
   ignoring later jobs for that same hash; and
4. sending StratumStats one authenticated batch of endpoint timestamps per
   block.

Analysis belongs in StratumStats. Scout must not parse or retain coinbase data,
merkle branches, transactions, or other job contents; derive block heights;
verify templates or payouts; calculate cross-endpoint statistics; or build a
local history. Payload size is secondary to keeping Scout's CPU, allocations,
wakeups, and timestamp path minimal.

# Repository guidance

- Keep the probe finite, memory-only, and unable to submit mining shares.
- Never add periodic measurement uploads, Stratum pings, or a second upload for
  connect, TLS, subscribe, or authorize timing. Attach any available setup
  timing to the next block batch and omit unavailable values.
- Attempt each block upload once. Do not queue, spool, split, or retry it. If
  StratumStats is unavailable, drop that block without disturbing the Stratum
  endpoint connections.
- Preserve compatibility with the StratumStats authenticated ingest contract.
- Keep parsing off the receive timestamp path. After timestamping, inspect only
  the minimum notification fields required to recognize a block transition.
- Never log ingest secrets, signatures, generated worker credentials, raw
  Stratum messages, or machine IDs.
- Run `go test ./...` and `git diff --check` before committing.
