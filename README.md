# StratumScout

StratumScout is the disposable regional measurement probe for
[StratumStats](https://github.com/Distortions81/StratumStats). It fetches a
collector-provided endpoint list, observes Stratum behavior for a bounded
window, uploads authenticated results, and exits.

The probe is finite and stateless. It cannot submit mining shares and has no
listener, database, volume, or durable local state.

Design, deployment, and operational documentation belongs in the main
StratumStats repository.
