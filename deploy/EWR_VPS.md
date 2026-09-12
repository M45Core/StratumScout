# EWR VPS deployment

The EWR Scout runs on `root@163.245.219.213` as the `stratumscout` system
user. It is intentionally independent of Fly machines.

## Runtime layout

- Binary: `/opt/stratumscout/stratumscout`
- Unit: `/etc/systemd/system/stratumscout.service`
- Root-only configuration: `/etc/stratumscout/scout.env` (`0600`)
- State directory: `/var/lib/stratumscout`
- Region label: `FLY_REGION=ewr`

The service has a `ConditionPathExists` guard and must not be started until
the configuration file exists. This avoids a restart loop and prevents a
partially configured probe from running.

## Credentials

The configuration needs the collector's active shared ingest pair:

```sh
INGEST_KEY_ID=...
INGEST_SECRET=...
```

These are the values of `STRATUMSTATS_INGEST_KEY_ID` and
`STRATUMSTATS_INGEST_SECRET` on the collector. They are not BTCFlux header
HMAC keys, must never be committed, and must not be copied from command
output or shell history. The current collector has one shared pair, so
generating a new pair requires an intentional collector restart and updating
every Scout at the same time.

Install the existing pair directly from a root session on the collector, or
create `/etc/stratumscout/scout.env` on the VPS with mode `0600` and these
non-secret settings:

```sh
COLLECTOR_URL=https://stratumstats.m45core.com
FLY_REGION=ewr
CONTINUOUS=true
PROCESS_NICE=0
```

Then start and verify the service without printing its environment:

```sh
systemctl start stratumscout
systemctl is-active --quiet stratumscout
journalctl -u stratumscout -n 50 --no-pager
```
