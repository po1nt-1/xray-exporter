# Xray Exporter

A Prometheus exporter that collects Xray _(and V2Ray)_ metrics over its gRPC Stats API and exposes them in Prometheus format.

## Features

- **Runtime Metrics**: Memory usage, goroutines, uptime, GC stats
- **Traffic Statistics**: Per-user, per-inbound, per-outbound uplink/downlink byte counters
- **TLS Support**: Optional TLS for the Xray gRPC connection (`--xray-api-tls`)
- **Cross-platform**: Supports Linux, macOS, Windows
- **Minimal & Fast**: No external dependencies beyond Xray's gRPC API

## Quick Start

### Binaries

The latest binaries are available on the [GitHub Releases Page](https://github.com/po1nt-1/xray-exporter/releases/latest).

### Docker

Multi-arch images are available via [GitHub Container Registry](https://github.com/po1nt-1/xray-exporter/pkgs/container/xray-exporter).

```bash
docker run --rm -it --read-only ghcr.io/po1nt-1/xray-exporter:latest \
  --xray-endpoint "127.0.0.1:54321"
```

Available tags:
- `main` _(Latest commit: may not be stable)_
- `latest` _(Recommended: Points to latest stable build)_
- `0.0.1` _(And any version number)_

## Command Line Options

You can view all available options by running:

```bash
xray-exporter -h
```

```
Usage:
  xray-exporter [OPTIONS]

Application options:
  -l, --listen=[ADDR]:PORT         Listen address (default: 127.0.0.1:9550)
  -m, --metrics-path=PATH          Metrics path (default: /scrape)
  -e, --xray-endpoint=HOST:PORT    Xray API endpoint (default: 127.0.0.1:8080)
  -t, --scrape-timeout=N           The timeout in seconds for every scrape (default: 5)
      --xray-api-tls               Use TLS for the Xray gRPC connection
      --version                    Display the version and exit

Help Options:
  -h, --help                       Show this help message
```

## Setup

Make sure the API and stats features are enabled in your Xray _(or V2Ray)_ config:

```json
{
  "routing": {
    "rules": [
      { "inboundTag": ["api"], "outboundTag": "api" }
    ]
  },
  "policy": {
    "levels": {
      "0": { "statsUserUplink": true, "statsUserDownlink": true }
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
    "services": ["StatsService"]
  },
  "inbounds": [
    {
      "tag": "api",
      "listen": "127.0.0.1",
      "port": 54321,
      "protocol": "dokodemo-door",
      "settings": { "address": "127.0.0.1" }
    }
  ],
  "outbounds": [
    { "tag": "direct", "protocol": "freedom", "settings": {} }
  ]
}
```

## Usage

```bash
# Basic usage
xray-exporter --xray-endpoint "127.0.0.1:54321"

# With TLS
xray-exporter --xray-endpoint "xray:54321" --xray-api-tls

# With custom port
xray-exporter --xray-endpoint "xray:54321" --listen ":9550"

# Docker
docker run --rm -d --read-only \
  ghcr.io/po1nt-1/xray-exporter:latest \
  --xray-endpoint "xray:54321"
```

The exporter starts listening and logs:

```
Xray Exporter v0.1.0-a1b2c3d (built 2025-01-15T10:30:00Z)
time="2025-01-15T10:30:45Z" level=info msg="Server starting on :9550"
```

## Metrics

Open `http://ip:9550/scrape` to view all metrics.

### Core Metrics

| Metric | Description |
| :----- | :---------- |
| `xray_up` | Whether the last scrape was successful (1 = success, 0 = failure) |
| `xray_scrapes_total` | Total number of scrapes performed |
| `xray_scrape_duration_seconds` | Time spent scraping metrics from Xray |

### Runtime Metrics

| Stat | Exposed Metric | Description |
| :--- | :------------- | :---------- |
| `uptime` | `xray_uptime_seconds` | Xray uptime in seconds |
| `num_goroutine` | `xray_goroutines` | Number of goroutines |
| `alloc` | `xray_memstats_alloc_bytes` | Bytes allocated and in use |
| `total_alloc` | `xray_memstats_alloc_bytes_total` | Total bytes allocated |
| `sys` | `xray_memstats_sys_bytes` | Bytes obtained from system |
| `mallocs` | `xray_memstats_mallocs_total` | Total number of mallocs |
| `frees` | `xray_memstats_frees_total` | Total number of frees |
| `num_gc` | `xray_memstats_num_gc` | Number of GC cycles |
| `pause_total_ns` | `xray_memstats_pause_total_ns` | Total GC pause time |

### Traffic Statistics

| Stat Metric | Exposed Metric |
| :---------- | :------------- |
| `inbound>>>tag>>>traffic>>>uplink` | `xray_traffic_uplink_bytes_total{dimension="inbound",target="tag"}` |
| `inbound>>>tag>>>traffic>>>downlink` | `xray_traffic_downlink_bytes_total{dimension="inbound",target="tag"}` |
| `outbound>>>tag>>>traffic>>>uplink` | `xray_traffic_uplink_bytes_total{dimension="outbound",target="tag"}` |
| `outbound>>>tag>>>traffic>>>downlink` | `xray_traffic_downlink_bytes_total{dimension="outbound",target="tag"}` |
| `user>>>email>>>traffic>>>uplink` | `xray_traffic_uplink_bytes_total{dimension="user",target="email"}` |
| `user>>>email>>>traffic>>>downlink` | `xray_traffic_downlink_bytes_total{dimension="user",target="email"}` |

## Prometheus Configuration

```yaml
scrape_configs:
  - job_name: xray
    metrics_path: /scrape
    static_configs:
      - targets: ['exporter:9550']
```

## Special Thanks

- <https://github.com/wi1dcard/v2ray-exporter>
