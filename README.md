# StratumScout

StratumScout is the disposable regional measurement probe for
[StratumStats](https://github.com/M45Core/StratumStats). It fetches a
collector-provided endpoint list, observes Stratum behavior for a bounded
window, uploads authenticated results, and exits.

The probe is finite and stateless. It cannot submit mining shares and has no
listener, database, volume, or durable local state.

Set `CONTINUOUS=true` when a long-running container should execute bounded
probe runs back to back. Every run still receives a fresh identifier and a
terminal upload before the next run starts. `PROCESS_NICE=10` lowers the Linux
scheduler priority when the container shares a Fly Machine with a
latency-sensitive service.

Design, deployment, and operational documentation belongs in the main
StratumStats repository.
