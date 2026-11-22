package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseArgs(t *testing.T) {
	t.Parallel()
	o, err := parseArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := options{
		Listen: "127.0.0.1:9550", MetricsPath: "/scrape", XrayEndpoint: "127.0.0.1:8080",
		ScrapeTimeout: timeout(5 * time.Second), UserLabel: "email", LogLevel: "info",
	}
	if !reflect.DeepEqual(*o, want) {
		t.Errorf("defaults = %+v, want %+v", *o, want)
	}

	o, err = parseArgs([]string{"-t", "7", "-b", "pool", "--balancer", "alt", "-e", "unix:///run/xray/api.sock", "--user-label", "hash"})
	if err != nil {
		t.Fatal(err)
	}
	if time.Duration(o.ScrapeTimeout) != 7*time.Second || !reflect.DeepEqual(o.Balancers, []string{"pool", "alt"}) ||
		o.XrayEndpoint != "unix:///run/xray/api.sock" || o.UserLabel != "hash" {
		t.Errorf("parsed = %+v", *o)
	}

	o, err = parseArgs([]string{"--scrape-timeout", "1500ms"})
	if err != nil || time.Duration(o.ScrapeTimeout) != 1500*time.Millisecond {
		t.Errorf("duration timeout: %v %v", o, err)
	}

	for _, bad := range [][]string{
		{"-t", "0"}, {"-t", "soon"}, {"--user-label", "plain"}, {"-m", "scrape"}, {"--log-level", "loud"}, {"--nope"},
	} {
		if _, err := parseArgs(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}

	if _, err := parseArgs([]string{"-h"}); !errors.Is(err, errHelp) {
		t.Errorf("help: %v", err)
	}
}

func TestHTTP(t *testing.T) {
	t.Parallel()
	e := newTestExporter(t, startFake(t, populated(), allServices), func(c *Config) { c.Balancers = []string{"pool"} })
	srv := httptest.NewServer(newMux(e, "/scrape"))
	t.Cleanup(srv.Close)

	get := func(path string) (int, string) {
		t.Helper()
		r, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Body.Close() }()
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		return r.StatusCode, string(b)
	}

	for _, p := range []string{"/metrics", "/scrape"} {
		code, body := get(p)
		if code != 200 {
			t.Fatalf("%s: %d", p, code)
		}
		for _, m := range []string{
			"xray_up 1", `xray_balancer_selected{balancer="pool",outbound="node-a"} 1`,
			"xray_exporter_build_info{", "go_goroutines ", "xray_scrapes_total ",
		} {
			if !strings.Contains(body, m) {
				t.Errorf("%s lacks %q", p, m)
			}
		}
	}
	if code, body := get("/health"); code != 200 || body != "OK\n" {
		t.Errorf("/health: %d %q", code, body)
	}
	if code, body := get("/"); code != 200 || !strings.Contains(body, `href="/scrape"`) {
		t.Errorf("/: %d %q", code, body)
	}
	if code, _ := get("/nope"); code != 404 {
		t.Errorf("/nope: %d", code)
	}
}

func TestHTTPMetricsPathIsMetrics(t *testing.T) {
	t.Parallel()
	e := newTestExporter(t, "127.0.0.1:1", nil)
	newMux(e, "/metrics")
}

func TestRunLifecycle(t *testing.T) {
	t.Parallel()
	addr := startFake(t, populated(), allServices)
	ctx, cancel := context.WithCancel(context.Background())
	listen := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"-l", "127.0.0.1:0", "-e", addr, "-b", "pool", "--log-level", "error"},
			func(a net.Addr) { listen <- a.String() })
	}()

	var url string
	select {
	case a := <-listen:
		url = "http://" + a + "/metrics"
	case err := <-done:
		t.Fatalf("run exited early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("run did not start")
	}
	r, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if !strings.Contains(string(b), "xray_up 1") {
		t.Error("xray_up 1 missing")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not stop")
	}
}

func TestRunErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if err := run(ctx, []string{"--version"}, nil); err != nil {
		t.Errorf("--version: %v", err)
	}
	if err := run(ctx, []string{"-l", "256.0.0.1:1"}, nil); err == nil {
		t.Error("bad listen address accepted")
	}
	if err := run(ctx, []string{"-t", "0"}, nil); err == nil {
		t.Error("zero timeout accepted")
	}
}
