//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

const (
	testUserID    = "00000000-0000-4000-8000-000000000001"
	testUserEmail = "user@example.com"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

type target struct {
	srv   *httptest.Server
	name  string
	delay time.Duration
}

func newTarget(name string, delay time.Duration) *target {
	tg := &target{name: name, delay: delay}
	tg.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(tg.delay)
		if r.URL.Path == "/probe" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = io.WriteString(w, tg.name)
	}))
	return tg
}

func (tg *target) addr() string { return strings.TrimPrefix(tg.srv.URL, "http://") }

type xrayEnv struct {
	api, socks int
}

func xrayConfig(env xrayEnv, a, b *target) map[string]any {
	vlessIn := func(tag string, port int) map[string]any {
		return map[string]any{
			"tag": tag, "listen": "127.0.0.1", "port": port, "protocol": "vless",
			"settings": map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": testUserID, "email": testUserEmail}}},
		}
	}
	vlessOut := func(tag string, port int) map[string]any {
		return map[string]any{
			"tag": tag, "protocol": "vless",
			"settings": map[string]any{"vnext": []any{map[string]any{
				"address": "127.0.0.1", "port": port,
				"users": []any{map[string]any{"id": testUserID, "encryption": "none"}},
			}}},
		}
	}
	exit := func(tag string, t *target) map[string]any {
		return map[string]any{
			"tag": tag, "protocol": "freedom",
			"settings": map[string]any{"redirect": t.addr(), "finalRules": []any{map[string]any{"action": "allow"}}},
		}
	}
	portA, portB := env.socks+1, env.socks+2
	return map[string]any{
		"log":   map[string]any{"loglevel": "warning"},
		"api":   map[string]any{"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", env.api), "services": []string{"StatsService", "ObservatoryService", "RoutingService"}},
		"stats": map[string]any{},
		"policy": map[string]any{
			"levels": map[string]any{"0": map[string]any{"statsUserUplink": true, "statsUserDownlink": true, "statsUserOnline": true}},
			"system": map[string]any{"statsInboundUplink": true, "statsInboundDownlink": true, "statsOutboundUplink": true, "statsOutboundDownlink": true},
		},
		"inbounds": []any{
			map[string]any{"tag": "socks-in", "listen": "127.0.0.1", "port": env.socks, "protocol": "socks", "settings": map[string]any{"udp": false}},
			vlessIn("server-a", portA),
			vlessIn("server-b", portB),
		},
		"outbounds": []any{
			vlessOut("node-a", portA),
			vlessOut("node-b", portB),
			exit("exit-a", a),
			exit("exit-b", b),
			map[string]any{"tag": "fallback", "protocol": "blackhole"},
		},
		"observatory": map[string]any{
			"subjectSelector": []string{"node-"}, "probeURL": "http://probe.test/probe",
			"probeInterval": "500ms", "enableConcurrency": true,
		},
		"routing": map[string]any{
			"balancers": []any{map[string]any{
				"tag": "pool", "selector": []string{"node-"}, "strategy": map[string]any{"type": "leastPing"}, "fallbackTag": "fallback",
			}},
			"rules": []any{
				map[string]any{"inboundTag": []string{"server-a"}, "outboundTag": "exit-a"},
				map[string]any{"inboundTag": []string{"server-b"}, "outboundTag": "exit-b"},
				map[string]any{"inboundTag": []string{"socks-in"}, "balancerTag": "pool"},
			},
		},
	}
}

func startXray(t *testing.T, cfg map[string]any, apiPort int) {
	t.Helper()
	bin := os.Getenv("XRAY_BIN")
	if bin == "" {
		t.Skip("XRAY_BIN is not set")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "xray.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "run", "-c", path)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			out, _ := os.ReadFile(log.Name())
			t.Logf("xray log:\n%s", out)
		}
	})
	waitFor(t, 10*time.Second, "xray API", func() bool {
		c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", apiPort))
		if err == nil {
			_ = c.Close()
		}
		return err == nil
	})
}

func waitFor(t *testing.T, limit time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

type snapshot map[string]float64

func gather(t *testing.T, e *Exporter) snapshot {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(e)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	s := snapshot{}
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			s[seriesKey(mf.GetName(), m)] = value(m)
		}
	}
	return s
}

func seriesKey(name string, m *dto.Metric) string {
	var parts []string
	for _, l := range m.GetLabel() {
		parts = append(parts, l.GetName()+"="+l.GetValue())
	}
	return name + "{" + strings.Join(parts, ",") + "}"
}

func value(m *dto.Metric) float64 {
	if m.GetGauge() != nil {
		return m.GetGauge().GetValue()
	}
	return m.GetCounter().GetValue()
}

func fetchVia(socks int, timeout time.Duration) (string, error) {
	proxy, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", socks))
	c := &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}}
	r, err := c.Get("http://target.test/")
	if err != nil {
		return "", err
	}
	defer func() { _ = r.Body.Close() }()
	b, err := io.ReadAll(r.Body)
	return string(b), err
}

func TestIntegrationBalancerFailover(t *testing.T) {
	a := newTarget("a", 150*time.Millisecond)
	b := newTarget("b", 0)
	defer a.srv.Close()
	defer b.srv.Close()

	env := xrayEnv{api: freePort(t), socks: freePort(t)}
	for _, p := range []int{env.socks + 1, env.socks + 2} {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			t.Skipf("port %d busy", p)
		}
		_ = l.Close()
	}
	startXray(t, xrayConfig(env, a, b), env.api)

	e := newTestExporter(t, fmt.Sprintf("127.0.0.1:%d", env.api), func(c *Config) { c.Balancers = []string{"pool"} })

	var s snapshot
	waitFor(t, 20*time.Second, "both outbounds alive and node-b picked", func() bool {
		s = gather(t, e)
		return s["xray_observatory_outbound_alive{outbound=node-a}"] == 1 &&
			s["xray_observatory_outbound_alive{outbound=node-b}"] == 1 &&
			s["xray_balancer_selected{balancer=pool,outbound=node-b}"] == 1
	})
	for _, c := range []string{"sys", "stats", "users", "observatory", "balancer"} {
		if s["xray_scrape_collector_success{collector="+c+"}"] != 1 {
			t.Errorf("collector %s failed", c)
		}
	}
	if da, db := s["xray_observatory_outbound_delay_seconds{outbound=node-a}"], s["xray_observatory_outbound_delay_seconds{outbound=node-b}"]; da <= db || da < 0.1 {
		t.Errorf("delays a=%v b=%v, want a slower and >= 100ms", da, db)
	}
	if s["xray_balancer_fallback{balancer=pool}"] != 0 {
		t.Error("fallback reported while outbounds are alive")
	}

	body, err := fetchVia(env.socks, 5*time.Second)
	if err != nil || body != "b" {
		t.Fatalf("request via pool: %q %v", body, err)
	}
	s = gather(t, e)
	for _, k := range []string{
		"xray_traffic_downlink_bytes_total{dimension=outbound,target=node-b}",
		"xray_traffic_downlink_bytes_total{dimension=inbound,target=socks-in}",
		"xray_traffic_downlink_bytes_total{dimension=user,target=" + testUserEmail + "}",
	} {
		if s[k] <= 0 {
			t.Errorf("%s = %v, want > 0", k, s[k])
		}
	}

	b.srv.Close()
	waitFor(t, 20*time.Second, "node-b dead and node-a picked", func() bool {
		s = gather(t, e)
		return s["xray_observatory_outbound_alive{outbound=node-b}"] == 0 &&
			s["xray_balancer_selected{balancer=pool,outbound=node-a}"] == 1
	})
	if s["xray_balancer_switches_total{balancer=pool}"] < 1 {
		t.Error("failover not counted as a balancer switch")
	}
	if _, ok := s["xray_observatory_outbound_delay_seconds{outbound=node-b}"]; ok {
		t.Error("delay exported for a dead outbound")
	}
	if seen, try := s["xray_observatory_outbound_last_seen_timestamp_seconds{outbound=node-b}"], s["xray_observatory_outbound_last_try_timestamp_seconds{outbound=node-b}"]; seen == 0 || try <= seen {
		t.Errorf("last_seen=%v last_try=%v, want try after seen", seen, try)
	}
	body, err = fetchVia(env.socks, 5*time.Second)
	if err != nil || body != "a" {
		t.Fatalf("request after failover: %q %v", body, err)
	}

	a.srv.Close()
	waitFor(t, 20*time.Second, "fallback", func() bool {
		s = gather(t, e)
		return s["xray_balancer_fallback{balancer=pool}"] == 1
	})
	for k := range s {
		if strings.HasPrefix(k, "xray_balancer_selected{") {
			t.Errorf("%s still exported with no live outbound", k)
		}
	}
	if s["xray_up"+"{}"] != 1 {
		t.Error("xray_up must stay 1 while xray itself runs")
	}
}

func TestIntegrationUnknownBalancerAndDisabledServices(t *testing.T) {
	env := xrayEnv{api: freePort(t), socks: freePort(t)}
	cfg := map[string]any{
		"api":       map[string]any{"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", env.api), "services": []string{"StatsService"}},
		"stats":     map[string]any{},
		"outbounds": []any{map[string]any{"protocol": "freedom"}},
	}
	startXray(t, cfg, env.api)
	e := newTestExporter(t, fmt.Sprintf("127.0.0.1:%d", env.api), func(c *Config) { c.Balancers = []string{"pool"} })
	s := gather(t, e)
	want := map[string]float64{
		"xray_up{}": 1,
		"xray_scrape_collector_success{collector=sys}":         1,
		"xray_scrape_collector_success{collector=stats}":       1,
		"xray_scrape_collector_success{collector=observatory}": 0,
		"xray_scrape_collector_success{collector=balancer}":    0,
	}
	for k, v := range want {
		if s[k] != v {
			t.Errorf("%s = %v, want %v", k, s[k], v)
		}
	}
	if s["xray_scrape_collector_errors_total{collector=observatory}"] != 0 {
		t.Error("disabled observatory service counted as an error")
	}
}
