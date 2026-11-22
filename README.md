# Xray Exporter

A Prometheus exporter for [Xray-core](https://github.com/XTLS/Xray-core). It reads the Xray gRPC API and shows
what the balancer does and why: which outbound it picks, which outbounds pass the observatory probes, how long
the probes take, and where the traffic goes.

It is small enough for routers (about 12 MiB RSS) and is built for linux/amd64, linux/arm64 and windows/amd64.

## Metrics

| Metric | Labels | Source |
| :----- | :----- | :----- |
| `xray_up` | | `StatsService` answers |
| `xray_scrape_collector_success` | `collector` | 0 also when the Xray service is not enabled |
| `xray_scrape_collector_errors_total` | `collector` | failed runs, disabled services excluded |
| `xray_scrape_duration_seconds`, `xray_scrapes_total` | | |
| `xray_uptime_seconds`, `xray_goroutines`, `xray_memstats_*` | | `StatsService.GetSysStats` |
| `xray_traffic_uplink_bytes_total`, `xray_traffic_downlink_bytes_total` | `dimension` (`inbound`, `outbound`, `user`), `target` | `StatsService.QueryStats` |
| `xray_users_online` | | `StatsService.GetUsersStats` |
| `xray_user_online_ips` | `user` | number of source addresses, never the addresses |
| `xray_observatory_outbound_alive` | `outbound` | `ObservatoryService` |
| `xray_observatory_outbound_delay_seconds` | `outbound` | only while alive |
| `xray_observatory_outbound_last_seen_timestamp_seconds`, `..._last_try_timestamp_seconds` | `outbound` | `observatory` only |
| `xray_observatory_health_ping_samples`, `..._failures`, `..._rtt_seconds` | `outbound`, `stat` | `burstObservatory` only |
| `xray_balancer_selected` | `balancer`, `outbound` | `RoutingService.GetBalancerInfo` |
| `xray_balancer_fallback` | `balancer` | 1 when the strategy picks nothing |
| `xray_balancer_override` | `balancer`, `outbound` | target forced through the API |
| `xray_balancer_switches_total` | `balancer` | pick changes seen between scrapes |
| `xray_exporter_build_info` | `version`, `commit` | |

Go runtime and process metrics of the exporter itself are exported too.

The observatory error text is not exported: it is free-form and may contain addresses.

## Xray configuration

Enable the API on loopback and the counters you need:

```json
{
  "api": {
    "tag": "api",
    "listen": "127.0.0.1:10085",
    "services": ["StatsService", "ObservatoryService", "RoutingService"]
  },
  "stats": {},
  "policy": {
    "levels": { "0": { "statsUserUplink": true, "statsUserDownlink": true, "statsUserOnline": true } },
    "system": {
      "statsInboundUplink": true, "statsInboundDownlink": true,
      "statsOutboundUplink": true, "statsOutboundDownlink": true
    }
  }
}
```

- Balancer metrics need `RoutingService`, an `observatory` or `burstObservatory`, and `--balancer <tag>` for every
  balancer to report. Xray cannot list balancers, so the tags are passed explicitly.
- User metrics need `statsUser*` in the policy level of the users.
- Missing services only turn off their collector; `xray_up` stays 1.

## Running

```
xray-exporter --xray-endpoint 127.0.0.1:10085 --balancer pool
```

| Flag | Default | |
| :--- | :------ | :- |
| `-l, --listen` | `127.0.0.1:9550` | |
| `-e, --xray-endpoint` | `127.0.0.1:8080` | `HOST:PORT` or `unix:///path` |
| `-b, --balancer` | | repeatable |
| `-t, --scrape-timeout` | `5s` | a plain number means seconds |
| `--user-label` | `email` | `email`, `hash` (12 hex chars of SHA-256) or `none` (no per-user series) |
| `-m, --metrics-path` | `/scrape` | served in addition to `/metrics` |
| `--xray-api-tls` | off | |
| `--log-level` | `info` | |

Endpoints: `/metrics`, the extra metrics path, `/health`.

### systemd

```ini
[Service]
DynamicUser=yes
Environment=GOMEMLIMIT=32MiB
ExecStart=/usr/local/bin/xray-exporter --xray-endpoint 127.0.0.1:10085 --balancer pool
Restart=always
MemoryMax=96M
```

### Routers (Entware)

Copy `xray-exporter-linux-arm64` to `/opt/sbin/xray-exporter` and `contrib/entware/S99xray-exporter` to
`/opt/etc/init.d/`, make both executable and run `/opt/etc/init.d/S99xray-exporter start`. With XKeen, put the API block above into its own file in the Xray config directory; Xray merges
the files, so keep `api`, `stats` and `policy` out of the other files.

### Docker

```
docker run --rm --network host ghcr.io/po1nt-1/xray-exporter:latest --xray-endpoint 127.0.0.1:10085
```

The image listens on `:9550`.

### Podman Quadlet (rootless)

`contrib/podman/xray-exporter.container` goes to `~/.config/containers/systemd/`. With `Network=pasta:-T,10085`
the container reaches only the Xray API port on the host loopback and nothing else on it.

When the collector runs in a podman network, the exporter has to join that network and cannot reach the host
loopback. In that case, make Xray listen on a unix socket (`"listen": "/run/user/1000/xray/api.sock,0600"`) and
mount its directory into the container. SELinux stops `container_t` from connecting to an unconfined Xray, so
the exporter needs its own type:

```
sudo semodule -i contrib/podman/xray_exporter.cil \
  /usr/share/udica/templates/base_container.cil /usr/share/udica/templates/net_container.cil
```

Then set `SecurityLabelType=xray_exporter.process` in the unit.

## Collecting

Scrape every 5-15 seconds; the probe and balancer state changes quickly. The dashboards group by a `host` label,
so set one per device:

```yaml
scrape_configs:
  - job_name: xray
    static_configs:
      - targets: ["127.0.0.1:9550"]
        labels:
          host: router
```

When the collector cannot reach the device all the time, run `vmagent` next to the exporter and let it push
with a bounded on-disk queue, so samples taken during an outage arrive later and the queue never outgrows the disk:

```
vmagent -promscrape.config=scrape.yaml \
  -remoteWrite.url=http://victoria-metrics:8428/api/v1/write \
  -remoteWrite.tmpDataPath=/var/lib/vmagent -remoteWrite.maxDiskUsagePerURL=512MiB \
  -memory.allowedBytes=48MiB -remoteWrite.label=host=router
```

vmagent rounds `maxDiskUsagePerURL` below 512 MiB up to 512 MiB. One exporter with a balancer of a few outbounds
produces about 100 series, roughly 10 MB a day at a 15 s interval.

## Dashboards and alerts

- `dashboards/xray-balancer.json`: picked outbound and probe results over time, probe delay, traffic per outbound,
  share of time each outbound was picked, switches, fallback.
- `dashboards/xray-server.json`: users online, traffic per inbound, outbound and user, upstream probes, metrics
  delivery lag and buffer.
- `alerts/xray.rules.yml`: rules for vmalert or Prometheus.

Each dashboard comes in two files: `*.json` for a Prometheus data source and `*-victoriametrics.json` for the
VictoriaMetrics plugin (`victoriametrics-metrics-datasource`). A dashboard only lists data sources of its own type.

The dashboards are generated by `go run dashboards/gen.go dashboards`; edit the generator, not the JSON.

## Development

```
make check                 # gofmt, go vet, golangci-lint, unit tests with -race
make integration XRAY_BIN=/path/to/xray
make dist                  # binaries for all platforms and checksums
```

The integration tests start a real Xray with a two-node balancer, take the faster node down and check that the
exporter reports the failed probe, the switch and finally the fallback.

## Thanks

- <https://github.com/wi1dcard/v2ray-exporter>
