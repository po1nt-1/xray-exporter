# Xray Exporter

An exporter that collects Xray (and V2Ray) metrics over its Stats API and exports them to Prometheus.

- [Quick Start](#quick-start)
  - [Binaries](#binaries)
  - [Docker](#docker-recommended)
- [Command Line Options](#command-line-options)
- [Tutorial](#tutorial)
- [Grafana Dashboard](#grafana-dashboard)
- [Available Metrics](#available-metrics)
- [Special Thanks](#special-thanks)

## Quick Start

### Binaries

The latest binaries are available on the [GitHub Releases Page](https://github.com/compassvpn/xray-exporter/releases/latest).

### Docker

You can also find the Docker images built automatically by CI from [GitHub Container Registry](https://github.com/compassvpn/xray-exporter/pkgs/container/xray-exporter).

```bash
docker run --rm -it --read-only ghcr.io/compassvpn/xray-exporter:latest
```

Available tags:
- main _(Latest commit: may not be stable)_
- latest _(Recommended: Points to latest stable build)_
- 0.0.1 _(And any version number)_

### Grafana Dashboard

A simple Grafana dashboard is also available [here](soon). Please refer to the Grafana docs to get the steps for importing dashboards from JSON files.

## Command Line Options

You can view all available command line options by running:

```bash
xray-exporter -h
```

Available options:

```
Usage:
  xray-exporter [OPTIONS]

Application Options:
  -l, --listen [ADDR]:PORT          Listen address (default: :9550)
  -m, --metrics-path PATH           Metrics path (default: /scrape)
  -e, --xray-endpoint HOST:PORT     Xray API endpoint (default: 127.0.0.1:8080)
  -t, --scrape-timeout N            The timeout in seconds for every individual scrape (default: 3)
      --version                     Display the version and exit

Help Options:
  -h, --help                        Show this help message
```

## Tutorial

Before we start, let's assume you have already set up Prometheus and Grafana.

First, you need to make sure the API and statistics-related features have been enabled in your Xray _(or V2Ray)_ config file. For example:

```json
{
  "routing": {
    "rules": [
      {
        "inboundTag": [
          "api"
        ],
        "outboundTag": "api"
      }
    ]
  },
  "policy": {
    "levels": {
      "0": {
        "statsUserUplink": true,
        "statsUserDownlink": true
      }
    },
    "system": {
      "statsInboundUplink": true,
      "statsInboundDownlink": true,
      "statsOutboundUplink": true,
      "statsOutboundDownlink": true
    }
  },
  "stats": {},
  "api": {
    "tag": "api",
    "services": [
      "StatsService"
    ]
  },
  "inbounds": [
    {
      "tag": "api",
      "listen": "127.0.0.1",
      "port": 54321,
      "protocol": "dokodemo-door",
      "settings": {
        "address": "127.0.0.1"
      }
    },
    {
      "tag": "inbound-1",
      "port": 12345,
      "protocol": "vmess",
      "settings": {
        "clients": [
          {
            "email": "email",
            "id": "uuid",
            "level": 0
          }
        ]
      }
    }
  ],
  "outbounds": [
    {
      "tag": "direct",
      "protocol": "freedom",
      "settings": {}
    }
  ]
}
```

As you can see, we opened two inbounds in the configuration above. The first inbound listens on port 54321 on localhost and handles the API calls, which is the endpoint that the exporter scrapes. The second inbound accepts VMess connections from the user `email`. If you'd like to run Xray/V2Ray and an exporter on different machines, consider using `0.0.0.0` instead of `127.0.0.1`, but be careful with the security risks.

Additionally, you should also enable `stats`, `api`, and `policy` settings, and set up proper routing rules to get traffic statistics working. For more information, please visit [xray-core API docs](https://xtls.github.io/config/api.html) and [xray-core Stats docs](https://xtls.github.io/en/config/stats.html).

The next step is to start the exporter:

```bash
xray-exporter --xray-endpoint "127.0.0.1:54321"
## Or with Docker
docker run --rm -d --read-only ghcr.io/compassvpn/xray-exporter:latest --xray-endpoint "127.0.0.1:54321"
```

The logs signify that the exporter started to listen on the default address (`:9550`).

```plain
Xray Exporter XXX-a1b2c3d (built 2025-01-01T21:00:00Z)
time="2025-01-15T10:30:45Z" level=info msg="Server is ready to handle incoming scrape requests."
```

Use `--listen` option if you'd like to change the listen address or port. You can open `http://ip:9550` in your browser:

Click the `Scrape Xray Metrics` and the exporter will expose all metrics, including Xray/V2Ray runtime and statistics data in the Prometheus metrics format, for example:

```shell
...
# HELP xray_up Indicate scrape succeeded or not
# TYPE xray_up gauge
xray_up 1
# HELP xray_uptime_seconds V2Ray uptime in seconds
# TYPE xray_uptime_seconds gauge
xray_uptime_seconds 150624
...
```

If `xray_up 1` doesn't exist in the response, that means the scrape failed. Please check out the logs (STDOUT or STDERR) of Xray Exporter for more detailed information.

We have the metrics exposed. Now let Prometheus scrape these data points and visualize them with Grafana. Here is an example Prometheus configuration:

```yaml
global:
  scrape_interval: 15s
  scrape_timeout: 5s

scrape_configs:
  - job_name: xray
    metrics_path: /scrape
    static_configs:
      - targets: [IP:9550]
```

To learn more about Prometheus, please visit the [official docs](https://prometheus.io/docs/prometheus/latest/configuration/configuration/).

## Available Metrics

For users who do not care about the internal changes, but only need a mapping table, here it is:

| Runtime Metric   | Exposed Metric                     |
| :--------------- | :--------------------------------- |
| `uptime`         | `xray_uptime_seconds`             |
| `num_goroutine`  | `xray_goroutines`                 |
| `alloc`          | `xray_memstats_alloc_bytes`       |
| `total_alloc`    | `xray_memstats_alloc_bytes_total` |
| `sys`            | `xray_memstats_sys_bytes`         |
| `mallocs`        | `xray_memstats_mallocs_total`     |
| `frees`          | `xray_memstats_frees_total`       |
| `num_gc`         | `xray_memstats_num_gc`            |
| `pause_total_ns` | `xray_memstats_pause_total_ns`    |

| Statistic Metric                          | Exposed Metric                                                              |
| :---------------------------------------- | :-------------------------------------------------------------------------- |
| `inbound>>>tag-name>>>traffic>>>uplink`   | `xray_traffic_uplink_bytes_total{dimension="inbound",target="tag-name"}`   |
| `inbound>>>tag-name>>>traffic>>>downlink` | `xray_traffic_downlink_bytes_total{dimension="inbound",target="tag-name"}` |
| `outbound>>>tag-name>>>traffic>>>uplink`   | `xray_traffic_uplink_bytes_total{dimension="outbound",target="tag-name"}`   |
| `outbound>>>tag-name>>>traffic>>>downlink` | `xray_traffic_downlink_bytes_total{dimension="outbound",target="tag-name"}` |
| `user>>>user-email>>traffic>>>uplink`     | `xray_traffic_uplink_bytes_total{dimension="user",target="user-email"}`    |
| `user>>>user-email>>>traffic>>>downlink`  | `xray_traffic_downlink_bytes_total{dimension="user",target="user-email"}`  |
| ...                                       | ...                                                                         |

## Special Thanks

- <https://github.com/wi1dcard/v2ray-exporter>
