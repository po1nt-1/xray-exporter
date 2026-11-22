package main

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/expfmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	routercmd "github.com/xtls/xray-core/app/router/command"
)

func compare(t *testing.T, c prometheus.Collector, want string, names ...string) {
	t.Helper()
	if err := testutil.CollectAndCompare(c, strings.NewReader(want), names...); err != nil {
		t.Error(err)
	}
}

func TestCollectAll(t *testing.T) {
	t.Parallel()
	e := newTestExporter(t, startFake(t, populated(), allServices), func(c *Config) {
		c.Balancers = []string{"pool", "down"}
	})

	compare(t, e, `
# HELP xray_up Whether the Xray API answered the last scrape.
# TYPE xray_up gauge
xray_up 1
# HELP xray_scrape_collector_success Whether a collector succeeded (0 also when the Xray service is disabled).
# TYPE xray_scrape_collector_success gauge
xray_scrape_collector_success{collector="balancer"} 1
xray_scrape_collector_success{collector="observatory"} 1
xray_scrape_collector_success{collector="stats"} 1
xray_scrape_collector_success{collector="sys"} 1
xray_scrape_collector_success{collector="users"} 1
# HELP xray_uptime_seconds Xray uptime.
# TYPE xray_uptime_seconds gauge
xray_uptime_seconds 3600
# HELP xray_goroutines Number of Xray goroutines.
# TYPE xray_goroutines gauge
xray_goroutines 42
# HELP xray_memstats_alloc_bytes_total Xray heap bytes allocated in total.
# TYPE xray_memstats_alloc_bytes_total counter
xray_memstats_alloc_bytes_total 5000
# HELP xray_memstats_gc_pause_seconds_total Total Xray GC pause time.
# TYPE xray_memstats_gc_pause_seconds_total counter
xray_memstats_gc_pause_seconds_total 1.5
# HELP xray_memstats_live_objects Xray live heap objects.
# TYPE xray_memstats_live_objects gauge
xray_memstats_live_objects 100
`, "xray_up", "xray_scrape_collector_success", "xray_uptime_seconds", "xray_goroutines",
		"xray_memstats_alloc_bytes_total", "xray_memstats_gc_pause_seconds_total", "xray_memstats_live_objects")

	compare(t, e, `
# HELP xray_traffic_uplink_bytes_total Bytes sent by the client side through the target.
# TYPE xray_traffic_uplink_bytes_total counter
xray_traffic_uplink_bytes_total{dimension="inbound",target="socks-in"} 10
xray_traffic_uplink_bytes_total{dimension="outbound",target="node-a"} 30
xray_traffic_uplink_bytes_total{dimension="user",target="alice@example.com"} 50
# HELP xray_traffic_downlink_bytes_total Bytes received by the client side through the target.
# TYPE xray_traffic_downlink_bytes_total counter
xray_traffic_downlink_bytes_total{dimension="inbound",target="socks-in"} 20
xray_traffic_downlink_bytes_total{dimension="outbound",target="node-a"} 40
xray_traffic_downlink_bytes_total{dimension="user",target="alice@example.com"} 60
`, "xray_traffic_uplink_bytes_total", "xray_traffic_downlink_bytes_total")

	compare(t, e, `
# HELP xray_users_online Users with at least one live connection.
# TYPE xray_users_online gauge
xray_users_online 1
# HELP xray_user_online_ips Distinct source IPs of a user with live connections.
# TYPE xray_user_online_ips gauge
xray_user_online_ips{user="alice@example.com"} 2
`, "xray_users_online", "xray_user_online_ips")

	compare(t, e, `
# HELP xray_observatory_outbound_alive Whether the last observatory probe through the outbound succeeded.
# TYPE xray_observatory_outbound_alive gauge
xray_observatory_outbound_alive{outbound="node-a"} 1
xray_observatory_outbound_alive{outbound="node-b"} 0
xray_observatory_outbound_alive{outbound="node-c"} 1
xray_observatory_outbound_alive{outbound="node-d"} 0
# HELP xray_observatory_outbound_delay_seconds Duration of the last successful observatory probe.
# TYPE xray_observatory_outbound_delay_seconds gauge
xray_observatory_outbound_delay_seconds{outbound="node-a"} 0.12
xray_observatory_outbound_delay_seconds{outbound="node-c"} 0.08
# HELP xray_observatory_outbound_last_seen_timestamp_seconds Unix time of the last successful probe.
# TYPE xray_observatory_outbound_last_seen_timestamp_seconds gauge
xray_observatory_outbound_last_seen_timestamp_seconds{outbound="node-a"} 1.7e+09
xray_observatory_outbound_last_seen_timestamp_seconds{outbound="node-b"} 1.69e+09
# HELP xray_observatory_outbound_last_try_timestamp_seconds Unix time of the last probe attempt.
# TYPE xray_observatory_outbound_last_try_timestamp_seconds gauge
xray_observatory_outbound_last_try_timestamp_seconds{outbound="node-a"} 1.7e+09
xray_observatory_outbound_last_try_timestamp_seconds{outbound="node-b"} 1.700000001e+09
# HELP xray_observatory_health_ping_samples Burst observatory pings in the sampling window.
# TYPE xray_observatory_health_ping_samples gauge
xray_observatory_health_ping_samples{outbound="node-c"} 10
xray_observatory_health_ping_samples{outbound="node-d"} 3
# HELP xray_observatory_health_ping_failures Failed burst observatory pings in the sampling window.
# TYPE xray_observatory_health_ping_failures gauge
xray_observatory_health_ping_failures{outbound="node-c"} 2
xray_observatory_health_ping_failures{outbound="node-d"} 3
# HELP xray_observatory_health_ping_rtt_seconds Burst observatory RTT statistics over the sampling window.
# TYPE xray_observatory_health_ping_rtt_seconds gauge
xray_observatory_health_ping_rtt_seconds{outbound="node-c",stat="avg"} 0.08
xray_observatory_health_ping_rtt_seconds{outbound="node-c",stat="max"} 0.2
xray_observatory_health_ping_rtt_seconds{outbound="node-c",stat="min"} 0.05
xray_observatory_health_ping_rtt_seconds{outbound="node-c",stat="stddev"} 0.01
`, "xray_observatory_outbound_alive", "xray_observatory_outbound_delay_seconds",
		"xray_observatory_outbound_last_seen_timestamp_seconds", "xray_observatory_outbound_last_try_timestamp_seconds",
		"xray_observatory_health_ping_samples", "xray_observatory_health_ping_failures", "xray_observatory_health_ping_rtt_seconds")

	compare(t, e, `
# HELP xray_balancer_selected Outbound the balancer strategy picks now (1 per picked outbound).
# TYPE xray_balancer_selected gauge
xray_balancer_selected{balancer="pool",outbound="node-a"} 1
# HELP xray_balancer_fallback 1 when the strategy picks nothing and the fallback outbound is used.
# TYPE xray_balancer_fallback gauge
xray_balancer_fallback{balancer="down"} 1
xray_balancer_fallback{balancer="pool"} 0
# HELP xray_balancer_override Outbound forced through the API override.
# TYPE xray_balancer_override gauge
xray_balancer_override{balancer="down",outbound="node-c"} 1
`, "xray_balancer_selected", "xray_balancer_fallback", "xray_balancer_override")
}

func TestNoPrivateDataInOutput(t *testing.T) {
	t.Parallel()
	e := newTestExporter(t, startFake(t, populated(), allServices), nil)
	out := scrapeText(t, e)
	for _, leak := range []string{"192.0.2.", "198.51.100.7", "timeout"} {
		if strings.Contains(out, leak) {
			t.Errorf("output contains %q", leak)
		}
	}
}

func TestUserLabelModes(t *testing.T) {
	t.Parallel()
	addr := startFake(t, populated(), allServices)

	hashed := newTestExporter(t, addr, func(c *Config) { c.UserLabel = UserLabelHash })
	out := scrapeText(t, hashed)
	if strings.Contains(out, "alice") {
		t.Error("hash mode leaks the email")
	}
	h := hashed.user("alice@example.com")
	if len(h) != 12 || h != hashed.user("alice@example.com") || h == hashed.user("bob@example.com") {
		t.Errorf("unstable or colliding hash %q", h)
	}
	if !strings.Contains(out, `target="`+h+`"`) || !strings.Contains(out, `user="`+h+`"`) {
		t.Error("hashed user missing from traffic or online metrics")
	}

	none := newTestExporter(t, addr, func(c *Config) { c.UserLabel = UserLabelNone })
	out = scrapeText(t, none)
	if strings.Contains(out, `dimension="user"`) || strings.Contains(out, "xray_user_online_ips{") {
		t.Error("none mode still exports per-user series")
	}
	if !strings.Contains(out, "xray_users_online 1") {
		t.Error("none mode must keep the online user count")
	}

	if _, err := NewExporter(Config{Endpoint: addr, Timeout: time.Second, UserLabel: "plain"}); err == nil {
		t.Error("unknown user label mode accepted")
	}
}

func TestDisabledServices(t *testing.T) {
	t.Parallel()
	f := populated()
	f.usersErr = status.Error(codes.Unimplemented, "old xray")
	e := newTestExporter(t, startFake(t, f, fakeServices{stats: true}), func(c *Config) { c.Balancers = []string{"pool"} })

	compare(t, e, `
# HELP xray_up Whether the Xray API answered the last scrape.
# TYPE xray_up gauge
xray_up 1
# HELP xray_scrape_collector_success Whether a collector succeeded (0 also when the Xray service is disabled).
# TYPE xray_scrape_collector_success gauge
xray_scrape_collector_success{collector="balancer"} 0
xray_scrape_collector_success{collector="observatory"} 0
xray_scrape_collector_success{collector="stats"} 1
xray_scrape_collector_success{collector="sys"} 1
xray_scrape_collector_success{collector="users"} 0
`, "xray_up", "xray_scrape_collector_success")

	for name, c := range e.failed {
		if v := testutil.ToFloat64(c); v != 0 {
			t.Errorf("disabled service counted as error: %s=%v", name, v)
		}
	}
}

func TestXrayDown(t *testing.T) {
	t.Parallel()
	f := populated()
	f.sysErr = status.Error(codes.Unavailable, "down")
	e := newTestExporter(t, startFake(t, f, allServices), nil)

	compare(t, e, `
# HELP xray_up Whether the Xray API answered the last scrape.
# TYPE xray_up gauge
xray_up 0
`, "xray_up")
	out := scrapeText(t, e)
	for _, absent := range []string{"xray_traffic_", "xray_observatory_", "xray_uptime_seconds"} {
		if strings.Contains(out, absent) {
			t.Errorf("%s exported while xray is down", absent)
		}
	}
	if v := testutil.ToFloat64(e.failed["sys"]); v != 2 {
		t.Errorf("sys errors = %v, want 2 after two scrapes", v)
	}
}

func TestNothingListening(t *testing.T) {
	t.Parallel()
	e := newTestExporter(t, "127.0.0.1:1", func(c *Config) { c.Timeout = 500 * time.Millisecond })
	start := time.Now()
	compare(t, e, `
# HELP xray_up Whether the Xray API answered the last scrape.
# TYPE xray_up gauge
xray_up 0
`, "xray_up")
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("scrape took %v", d)
	}
}

func TestScrapeTimeout(t *testing.T) {
	t.Parallel()
	f := populated()
	f.delay = 5 * time.Second
	e := newTestExporter(t, startFake(t, f, allServices), func(c *Config) { c.Timeout = 300 * time.Millisecond })
	start := time.Now()
	compare(t, e, `
# HELP xray_up Whether the Xray API answered the last scrape.
# TYPE xray_up gauge
xray_up 0
`, "xray_up")
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("timeout not honoured: %v", d)
	}
}

func TestBalancerPartialFailure(t *testing.T) {
	t.Parallel()
	e := newTestExporter(t, startFake(t, populated(), allServices), func(c *Config) {
		c.Balancers = []string{"missing", "pool", "nostrategy"}
	})
	compare(t, e, `
# HELP xray_balancer_selected Outbound the balancer strategy picks now (1 per picked outbound).
# TYPE xray_balancer_selected gauge
xray_balancer_selected{balancer="pool",outbound="node-a"} 1
# HELP xray_balancer_fallback 1 when the strategy picks nothing and the fallback outbound is used.
# TYPE xray_balancer_fallback gauge
xray_balancer_fallback{balancer="nostrategy"} 0
xray_balancer_fallback{balancer="pool"} 0
# HELP xray_scrape_collector_success Whether a collector succeeded (0 also when the Xray service is disabled).
# TYPE xray_scrape_collector_success gauge
xray_scrape_collector_success{collector="balancer"} 0
xray_scrape_collector_success{collector="observatory"} 1
xray_scrape_collector_success{collector="stats"} 1
xray_scrape_collector_success{collector="sys"} 1
xray_scrape_collector_success{collector="users"} 1
`, "xray_balancer_selected", "xray_balancer_fallback", "xray_scrape_collector_success")
	if v := testutil.ToFloat64(e.failed["balancer"]); v != 1 {
		t.Errorf("balancer errors = %v, want 1", v)
	}
}

func TestBalancerSwitches(t *testing.T) {
	t.Parallel()
	f := populated()
	e := newTestExporter(t, startFake(t, f, allServices), func(c *Config) { c.Balancers = []string{"pool", "down"} })
	set := func(target, override string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.balancers["pool"] = &routercmd.BalancerMsg{
			Override:        &routercmd.OverrideInfo{Target: override},
			PrincipleTarget: &routercmd.PrincipleTargetInfo{Tag: []string{target}},
		}
	}
	switches := func() float64 {
		t.Helper()
		scrapeText(t, e)
		return testutil.ToFloat64(e.switches.WithLabelValues("pool"))
	}

	for i, step := range []struct {
		target, override string
		want             float64
	}{
		{"node-a", "", 0},
		{"node-a", "", 0},
		{"node-b", "", 1},
		{"node-b", "node-c", 2},
		{"node-a", "node-c", 2},
		{"", "", 3},
		{"", "", 3},
		{"node-a", "", 4},
	} {
		set(step.target, step.override)
		if got := switches(); got != step.want {
			t.Fatalf("step %d: switches = %v, want %v", i, got, step.want)
		}
	}
	if v := testutil.ToFloat64(e.switches.WithLabelValues("down")); v != 0 {
		t.Errorf("unchanged balancer switched %v times", v)
	}
}

func TestObservatoryError(t *testing.T) {
	t.Parallel()
	f := populated()
	f.obsErr = errors.New("observatory not configured")
	e := newTestExporter(t, startFake(t, f, allServices), nil)
	out := scrapeText(t, e)
	if strings.Contains(out, "xray_observatory_outbound_alive") {
		t.Error("observatory metrics exported after an error")
	}
	if !strings.Contains(out, `xray_scrape_collector_success{collector="observatory"} 0`) {
		t.Error("observatory failure not reported")
	}
}

func TestConcurrentScrapes(t *testing.T) {
	t.Parallel()
	e := newTestExporter(t, startFake(t, populated(), allServices), func(c *Config) { c.Balancers = []string{"pool"} })
	reg := newRegistry(e)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := reg.Gather(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if v := testutil.ToFloat64(e.scrapes); v != 16 {
		t.Errorf("scrapes = %v, want 16", v)
	}
}

func TestLint(t *testing.T) {
	t.Parallel()
	e := newTestExporter(t, startFake(t, populated(), allServices), func(c *Config) { c.Balancers = []string{"pool", "down"} })
	problems, err := testutil.CollectAndLint(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Errorf("%s: %s", p.Metric, p.Text)
	}
}

func TestCloseTwice(t *testing.T) {
	t.Parallel()
	e, err := NewExporter(Config{Endpoint: "127.0.0.1:1", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
}

func scrapeText(t *testing.T, c prometheus.Collector) string {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(c)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, mf := range mfs {
		if _, err := expfmt.MetricFamilyToText(&b, mf); err != nil {
			t.Fatal(err)
		}
	}
	return b.String()
}
