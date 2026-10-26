# EWR server deployment

Since October 6, 2026, US East (`FLY_REGION=ewr`, vantage `us-east`) runs
on `dist@m45core.com`, SSH port `5313`, as a systemd user service.
The `dist` user has lingering enabled, so the service starts at boot without
an interactive login. Only one EWR Scout may run at a time.

## Runtime layout

- Service: `stratumscout-ewr.service` in the `dist` user manager.
- Unit: `/home/dist/.config/systemd/user/stratumscout-ewr.service`.
- Binary: `/home/dist/.local/lib/stratumscout/818bb1c822ad/stratumscout`.
- Credentials: `/home/dist/.config/stratumscout/ewr.env`, owner `dist`, mode `0600`.
- Collector: `https://stratumstats.m45core.com`.
- Continuous operation, `PROCESS_NICE=0`, memory limit `256M`, CPU weight `25`.
- Binary source revision: `818bb1c822ad`, built with Go 1.26.6,
  `CGO_ENABLED=0`, `-buildvcs=false -trimpath -ldflags='-s -w'`.
- Binary SHA-256: `a3d18d7a433d1a6fd55a8e7225e01fde2d97ccfadc2926d8a19ea572b1e3b078`.

The server's older `/home/dist/StratumScout` checkout is not the deployed
source and was left untouched. Scout has no listener or persistent measurement
store. Its credentials are the collector's existing shared ingest pair;
do not rotate them independently of the other regional Scouts or display them.

## Service operations

After connecting with `ssh -p 5313 dist@m45core.com`:

```sh
systemctl --user status stratumscout-ewr.service --no-pager
journalctl --user -u stratumscout-ewr.service -n 50 --no-pager
systemctl --user restart stratumscout-ewr.service
```

Updates should install a tested binary in a new revision directory, change
`ExecStart`, reload the user manager, and restart this one service. Preserve
the previous binary for rollback. Check zero unexpected restarts, a completed
block upload, and fresh US East collector data. Never start a second EWR
process for validation while this service is active.

## Fly rollback

Fly app `m45-stratumscout` retains stopped Machine `8dd4e6ae3547e8` with image
`registry.fly.io/m45-stratumscout:scout-818bb1c822ad@sha256:c0605d9c2746345387e055a1df3cbfbbecce283e2d4062819af978c925209f23`.
Its Fly deployment manifest entry has `enabled: false`, preventing the
deployment helper from creating another EWR Scout while permitting the stopped
rollback Machine to remain in the inventory. Do not destroy it until rollback
is no longer needed.

First stop and disable the server service, then start the Fly Machine:

```sh
ssh -p 5313 dist@m45core.com 'systemctl --user disable --now stratumscout-ewr.service'
fly machine start 8dd4e6ae3547e8 --app m45-stratumscout
```

Verify an accepted block upload. To move back to the server, stop Fly EWR
and confirm it is stopped before enabling and starting the user service.

## Measurement limitations

This EWR observer shares a host and network with M45Core's pool, Bitcoin Core,
and BTCFlux. The local pool has a shorter network path, and server load or
outages affect both the pool and observer. EWR measurements before and after
this migration represent different network paths even though the region key
is unchanged. Use independent regional observers when assessing comparisons.

During migration, `m45core.com:4333` served a certificate that expired on
September 29, 2026. This pre-existing endpoint issue causes Scout's TLS
connection to fail until the pool's certificate is renewed; do not disable
Scout's certificate verification as a workaround.
