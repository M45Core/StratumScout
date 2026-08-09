# Repository guidance

- Keep the probe finite, stateless, and unable to submit mining shares.
- Preserve compatibility with the StratumStats authenticated ingest contract.
- Never log ingest secrets, signatures, generated worker credentials, raw Stratum messages, or machine IDs.
- Run `go test ./...` and `git diff --check` before committing.
