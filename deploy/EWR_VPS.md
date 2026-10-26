# Retired EWR VPS deployment

The former EWR VPS has been removed. US East (`us-east`, region `ewr`) now
runs on `m45core.com`; see the [EWR server runbook](EWR_SERVER.md).

The EWR Fly Machine is stopped and retained only for rollback. Do not start
it or recreate the former VPS probe while the server service is running:
each region must have only one active Scout.
