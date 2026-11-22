//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type m = map[string]any

type flavor struct{ file, uid, dsType, varRef string }

var flavors = []flavor{
	{"", "", "prometheus", "PrometheusVariableQueryEditor-VariableQuery"},
	{"-victoriametrics", "-vm", "victoriametrics-metrics-datasource", "StandardVariableQuery"},
}

var (
	cur flavor
	ds  m
)

type board struct {
	uid, title string
	vars       []m
	panels     []m
	x, y, rowH int
	nextID     int
}

func newBoard(uid, title string, vars ...m) *board {
	return &board{uid: uid, title: title, vars: vars, nextID: 1}
}

func (b *board) newline() {
	if b.x > 0 {
		b.y += b.rowH
		b.x, b.rowH = 0, 0
	}
}

func (b *board) row(title string) {
	b.newline()
	b.panels = append(b.panels, m{"type": "row", "title": title, "id": b.nextID, "collapsed": false,
		"gridPos": m{"h": 1, "w": 24, "x": 0, "y": b.y}, "panels": []m{}})
	b.nextID++
	b.y++
}

func (b *board) add(p m, w, h int) {
	if b.x+w > 24 {
		b.newline()
	}
	p["id"] = b.nextID
	p["gridPos"] = m{"h": h, "w": w, "x": b.x, "y": b.y}
	p["datasource"] = ds
	b.nextID++
	b.panels = append(b.panels, p)
	b.x += w
	b.rowH = max(b.rowH, h)
}

func (b *board) json() m {
	return m{
		"uid": b.uid + cur.uid, "title": b.title, "tags": []string{"xray", "xray-exporter"},
		"editable": true, "graphTooltip": 1, "refresh": "30s", "schemaVersion": 39,
		"time": m{"from": "now-6h", "to": "now"}, "timezone": "browser",
		"templating":  m{"list": b.vars},
		"panels":      b.panels,
		"annotations": m{"list": []m{}},
		"links":       []m{},
	}
}

func target(expr, legend, ref string) m {
	return m{"datasource": ds, "expr": expr, "legendFormat": legend, "refId": ref}
}

func instant(expr, legend string) m {
	t := target(expr, legend, "A")
	t["instant"], t["range"] = true, false
	return t
}

func steps(s ...any) m {
	var out []m
	for i := 0; i < len(s); i += 2 {
		out = append(out, m{"color": s[i], "value": s[i+1]})
	}
	return m{"mode": "absolute", "steps": out}
}

type mapping struct {
	value       int
	text, color string
}

func valueMap(ms ...mapping) []m {
	opts := m{}
	for i, v := range ms {
		opts[fmt.Sprint(v.value)] = m{"text": v.text, "color": v.color, "index": i}
	}
	return []m{{"type": "value", "options": opts}}
}

var upDown = valueMap(mapping{1, "up", "green"}, mapping{0, "down", "red"})
var aliveDead = valueMap(mapping{1, "alive", "green"}, mapping{0, "dead", "red"})

type statOpt func(defaults, panel m)

func unit(u string) statOpt     { return func(d, _ m) { d["unit"] = u } }
func maps(v []m) statOpt        { return func(d, _ m) { d["mappings"] = v } }
func thresholds(t m) statOpt    { return func(d, _ m) { d["thresholds"] = t } }
func decimals(n int) statOpt    { return func(d, _ m) { d["decimals"] = n } }
func describe(s string) statOpt { return func(_, p m) { p["description"] = s } }

func stat(title, expr string, opts ...statOpt) m {
	d := m{"unit": "none", "mappings": []m{}, "thresholds": steps("text", nil), "color": m{"mode": "thresholds"}}
	p := m{"type": "stat", "title": title, "targets": []m{instant(expr, "")},
		"options": m{"reduceOptions": m{"calcs": []string{"lastNotNull"}, "fields": "", "values": false},
			"colorMode": "background", "graphMode": "none", "textMode": "value", "justifyMode": "center", "orientation": "auto"}}
	for _, o := range opts {
		o(d, p)
	}
	p["fieldConfig"] = m{"defaults": d, "overrides": []m{}}
	return p
}

func series(title, u string, targets []m, opts ...statOpt) m {
	d := m{"unit": u, "color": m{"mode": "palette-classic"},
		"custom": m{"drawStyle": "line", "lineWidth": 1, "fillOpacity": 10, "showPoints": "never", "spanNulls": false,
			"stacking": m{"mode": "none", "group": "A"}}}
	p := m{"type": "timeseries", "title": title, "targets": targets,
		"options": m{"legend": m{"displayMode": "table", "placement": "right", "calcs": []string{"lastNotNull", "max"}},
			"tooltip": m{"mode": "multi", "sort": "desc"}}}
	for _, o := range opts {
		o(d, p)
	}
	p["fieldConfig"] = m{"defaults": d, "overrides": []m{}}
	return p
}

func bars(d, _ m) { d["custom"].(m)["drawStyle"] = "bars" }

func timeline(title, expr string, mappings []m, opts ...statOpt) m {
	d := m{"mappings": mappings, "color": m{"mode": "thresholds"}, "thresholds": steps("transparent", nil),
		"custom": m{"fillOpacity": 80, "lineWidth": 0}}
	p := m{"type": "state-timeline", "title": title, "targets": []m{target(expr, "{{host}} {{outbound}}", "A")},
		"options": m{"showValue": "never", "mergeValues": true, "rowHeight": 0.85, "alignValue": "left",
			"legend": m{"showLegend": false}, "tooltip": m{"mode": "single"}}}
	for _, o := range opts {
		o(d, p)
	}
	p["fieldConfig"] = m{"defaults": d, "overrides": []m{}}
	return p
}

func barGauge(title, expr string) m {
	return m{"type": "bargauge", "title": title, "targets": []m{instant(expr, "{{host}} {{outbound}}")},
		"fieldConfig": m{"defaults": m{"unit": "percentunit", "min": 0, "max": 1, "color": m{"mode": "continuous-GrYlRd"},
			"thresholds": steps("green", nil)}, "overrides": []m{}},
		"options": m{"orientation": "horizontal", "displayMode": "basic", "showUnfilled": true,
			"reduceOptions": m{"calcs": []string{"lastNotNull"}, "fields": "", "values": false}}}
}

func table(title, expr string) m {
	return m{"type": "table", "title": title, "targets": []m{instant(expr, "")},
		"fieldConfig": m{"defaults": m{"unit": "s", "color": m{"mode": "thresholds"},
			"thresholds": steps("green", nil, "orange", 30, "red", 120),
			"custom":     m{"cellOptions": m{"type": "color-background"}}}, "overrides": []m{}},
		"transformations": []m{{"id": "reduce", "options": m{"reducers": []string{"lastNotNull"}, "labelsToFields": true}}},
		"options":         m{"showHeader": true}}
}

func dsVar() m {
	return m{"name": "datasource", "label": "Data source", "type": "datasource", "query": cur.dsType,
		"current": m{}, "hide": 0, "refresh": 1}
}

func queryVar(name, label, query string) m {
	return m{"name": name, "label": label, "type": "query", "datasource": ds,
		"query":      m{"query": query, "refId": cur.varRef},
		"definition": query, "refresh": 2, "sort": 1, "multi": true, "includeAll": true, "current": m{}, "hide": 0}
}

func traffic(sel string) []m {
	return []m{
		target(`sum by (host, target) (rate(xray_traffic_downlink_bytes_total{`+sel+`}[$__rate_interval]))`, "{{host}} {{target}} down", "A"),
		target(`-sum by (host, target) (rate(xray_traffic_uplink_bytes_total{`+sel+`}[$__rate_interval]))`, "{{host}} {{target}} up", "B"),
	}
}

func balancerBoard() m {
	const h = `host=~"$host"`
	const hb = `host=~"$host",balancer=~"$balancer"`
	b := newBoard("xray-balancer", "Xray balancer", dsVar(),
		queryVar("host", "Host", "label_values(xray_balancer_switches_total, host)"),
		queryVar("balancer", "Balancer", `label_values(xray_balancer_switches_total{host=~"$host"}, balancer)`))

	b.add(stat("Xray API", "min(xray_up{"+h+"})", maps(upDown)), 4, 4)
	b.add(stat("Alive outbounds", "sum(xray_observatory_outbound_alive{"+h+"})", thresholds(steps("red", nil, "green", 1))), 4, 4)
	b.add(stat("Dead outbounds", "count(xray_observatory_outbound_alive{"+h+"} == 0) or vector(0)", thresholds(steps("green", nil, "orange", 1))), 4, 4)
	b.add(stat("Fallback", "max(xray_balancer_fallback{"+hb+"})",
		maps(valueMap(mapping{0, "no", "green"}, mapping{1, "ACTIVE", "red"})),
		describe("The strategy found no live outbound, traffic goes to the fallback outbound.")), 4, 4)
	b.add(stat("Switches in range", "sum(increase(xray_balancer_switches_total{"+hb+"}[$__range]))", decimals(0),
		describe("Pick changes seen by the exporter between scrapes.")), 4, 4)
	b.add(stat("Overridden", "count(xray_balancer_override{"+hb+"}) or vector(0)", thresholds(steps("green", nil, "orange", 1)),
		describe("Balancers forced to an outbound through the API.")), 4, 4)

	b.row("Choice")
	b.add(timeline("Picked outbound", "max by (host, outbound) (xray_balancer_selected{"+hb+"})",
		valueMap(mapping{1, "picked", "blue"}),
		describe("Outbound the strategy picks for new connections. Gaps mean another outbound was picked.")), 24, 7)
	b.add(timeline("Probe result", "max by (host, outbound) (xray_observatory_outbound_alive{"+h+"})", aliveDead,
		describe("Last observatory probe through each outbound.")), 24, 7)

	b.row("Quality")
	b.add(series("Probe delay", "s", []m{target("max by (host, outbound) (xray_observatory_outbound_delay_seconds{"+h+"})", "{{host}} {{outbound}}", "A")},
		describe("Full-path probe time; leastPing picks the lowest.")), 12, 8)
	b.add(series("Traffic per outbound", "Bps", traffic(h+`,dimension="outbound"`)), 12, 8)
	b.add(barGauge("Share of time picked",
		"sum by (host, outbound) (count_over_time(xray_balancer_selected{"+hb+"}[$__range]))"+
			" / on (host) group_left max by (host) (count_over_time(xray_up{"+h+"}[$__range]))"), 12, 8)
	b.add(table("Since last successful probe", "max by (host, outbound) (time() - xray_observatory_outbound_last_seen_timestamp_seconds{"+h+"})"), 12, 8)
	b.add(series("Switches", "none", []m{target("sum by (host, balancer) (increase(xray_balancer_switches_total{"+hb+"}[$__rate_interval]))", "{{host}} {{balancer}}", "A")}, bars), 12, 7)
	b.add(series("Exporter collectors", "none", []m{target("min by (host, collector) (xray_scrape_collector_success{"+h+"})", "{{host}} {{collector}}", "A")},
		describe("0 also when the Xray API service behind the collector is not enabled.")), 12, 7)
	return b.json()
}

func serverBoard() m {
	const h = `host=~"$host"`
	b := newBoard("xray-server", "Xray server", dsVar(), queryVar("host", "Host", "label_values(xray_up, host)"))

	b.add(stat("Xray API", "min(xray_up{"+h+"})", maps(upDown)), 4, 4)
	b.add(stat("Users online", "sum(xray_users_online{"+h+"})"), 4, 4)
	b.add(stat("Uptime", "min(xray_uptime_seconds{"+h+"})", unit("s")), 4, 4)
	b.add(stat("Xray memory", "max(xray_memstats_sys_bytes{"+h+"})", unit("bytes")), 4, 4)
	b.add(stat("Metrics lag", "max(time() - timestamp(xray_up{"+h+"}))", unit("s"),
		thresholds(steps("green", nil, "orange", 120, "red", 600)),
		describe("Age of the newest sample. Grows while the delivery path is down and drops after the backlog arrives.")), 4, 4)
	b.add(stat("Buffered metrics", "sum(vmagent_remotewrite_pending_data_bytes{"+h+"}) or vector(0)", unit("bytes"),
		thresholds(steps("green", nil, "orange", 1048576)), describe("vmagent queue on the host waiting for the delivery path.")), 4, 4)

	b.row("Users")
	b.add(series("Users online", "none", []m{target("sum by (host) (xray_users_online{"+h+"})", "{{host}}", "A")}), 12, 8)
	b.add(series("Top users by traffic", "Bps", []m{target(
		`topk(10, sum by (host, target) (rate(xray_traffic_downlink_bytes_total{`+h+`,dimension="user"}[$__rate_interval])`+
			` + rate(xray_traffic_uplink_bytes_total{`+h+`,dimension="user"}[$__rate_interval])))`, "{{host}} {{target}}", "A")}), 12, 8)

	b.row("Traffic")
	b.add(series("Inbound traffic", "Bps", traffic(h+`,dimension="inbound"`)), 12, 8)
	b.add(series("Outbound traffic", "Bps", traffic(h+`,dimension="outbound"`)), 12, 8)

	b.row("Upstream health")
	b.add(timeline("Upstream probe", "max by (host, outbound) (xray_observatory_outbound_alive{"+h+"})", aliveDead,
		describe("Observatory on the server probing its own outbounds.")), 12, 8)
	b.add(series("Upstream probe delay", "s", []m{target("max by (host, outbound) (xray_observatory_outbound_delay_seconds{"+h+"})", "{{host}} {{outbound}}", "A")}), 12, 8)

	b.row("Delivery")
	b.add(series("Metrics queue", "bytes", []m{
		target("sum by (host) (vmagent_remotewrite_pending_data_bytes{"+h+"})", "{{host}} pending", "A"),
		target("sum by (host) (increase(vmagent_remotewrite_packets_dropped_total{"+h+"}[$__rate_interval]))", "{{host}} dropped", "B"),
	}, describe("Pending grows while the path home is down; dropped means the disk cap was hit.")), 12, 7)
	b.add(series("Xray process", "short", []m{
		target("max by (host) (xray_memstats_alloc_bytes{"+h+"})", "{{host}} heap", "A"),
		target("max by (host) (xray_goroutines{"+h+"})", "{{host}} goroutines", "B"),
	}), 12, 7)
	return b.json()
}

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	for _, f := range flavors {
		cur, ds = f, m{"type": f.dsType, "uid": "${datasource}"}
		for name, d := range map[string]m{"xray-balancer": balancerBoard(), "xray-server": serverBoard()} {
			raw, err := json.MarshalIndent(d, "", "  ")
			if err != nil {
				panic(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name+f.file+".json"), append(raw, '\n'), 0o644); err != nil {
				panic(err)
			}
		}
	}
}
