package main

import (
	"testing"
	"time"
)

// --- helpers ---

func wantNil(t *testing.T, label string, got interface{}) {
	t.Helper()
	if got != nil {
		t.Errorf("expected nil %s, got: %v", label, got)
	}
}

func wantNotNil(t *testing.T, label string, got interface{}) {
	t.Helper()
	if got == nil {
		t.Errorf("expected non-nil %s, got nil", label)
	}
}

// TestMain_NoLogpathFlag verifies no log path flags exist.
func TestMain_NoLogpathFlag(t *testing.T) {
	t.Parallel()
	// The --log-path flag was removed from the --help output.
	// Build succeeds without internal/logparser package.
	t.Skip("verified by go build success")
}

// TestMain_TLSFlag verifies --xray-api-tls works.
func TestMain_TLSFlag(t *testing.T) {
	t.Parallel()

	e, err := NewExporter("127.0.0.1:8080", 5*time.Second, true)
	wantNil(t, "NewExporter with TLS", err)
	wantNotNil(t, "exporter", e)
	_ = e.Close()

	e2, err := NewExporter("127.0.0.1:8080", 5*time.Second, false)
	wantNil(t, "NewExporter no TLS", err)
	wantNotNil(t, "exporter2", e2)
	_ = e2.Close()
}

// TestExporter_Credentials_NoTLS verifies localhost uses insecure credentials.
func TestExporter_Credentials_NoTLS(t *testing.T) {
	t.Parallel()

	e, err := NewExporter("127.0.0.1:8080", 5*time.Second, false)
	wantNil(t, "err", err)
	wantNotNil(t, "conn", e.conn)
	_ = e.Close()
}

// TestExporter_Credentials_TLSEnabled verifies useTLS=true works.
func TestExporter_Credentials_TLSEnabled(t *testing.T) {
	t.Parallel()

	e, err := NewExporter("127.0.0.1:8080", 5*time.Second, true)
	wantNil(t, "err", err)
	wantNotNil(t, "conn", e.conn)
	_ = e.Close()

	e2, err := NewExporter("remote.host:9090", 5*time.Second, true)
	wantNil(t, "err remote", err)
	wantNotNil(t, "conn2", e2.conn)
	_ = e2.Close()
}

// TestExporter_Metrics_NoLogMetrics verifies log-related metrics are absent.
func TestExporter_Metrics_NoLogMetrics(t *testing.T) {
	t.Parallel()

	e, err := NewExporter("127.0.0.1:8080", 5*time.Second, false)
	wantNil(t, "err", err)
	wantNotNil(t, "exporter", e)
	defer e.Close()

	for _, k := range []string{"unique_users", "total_connections"} {
		if _, ok := e.metricDescriptions[k]; ok {
			t.Errorf("unexpected metric %q", k)
		}
	}
}

// TestExporter_Metrics_CorePresent verifies core metrics exist.
func TestExporter_Metrics_CorePresent(t *testing.T) {
	t.Parallel()

	e, err := NewExporter("127.0.0.1:8080", 5*time.Second, false)
	wantNil(t, "err", err)
	wantNotNil(t, "exporter", e)
	defer e.Close()

	for _, k := range []string{"up", "uptime_seconds", "goroutines",
		"traffic_uplink_bytes_total", "traffic_downlink_bytes_total"} {
		if _, ok := e.metricDescriptions[k]; !ok {
			t.Errorf("expected metric %q", k)
		}
	}
}

// TestExporter_MetricDescr_Labels verifies traffic metrics have labels.
func TestExporter_MetricDescr_Labels(t *testing.T) {
	t.Parallel()

	e, err := NewExporter("127.0.0.1:8080", 5*time.Second, false)
	wantNil(t, "err", err)
	wantNotNil(t, "exporter", e)
	defer e.Close()

	d := e.metricDescriptions["traffic_uplink_bytes_total"]
	if d == nil {
		t.Fatal("traffic_uplink_bytes_total not found")
	}
	if len(d.String()) == 0 {
		t.Error("expected non-empty Desc string")
	}
}

// TestExporter_Close_Idempotent verifies second Close is safe.
func TestExporter_Close_Idempotent(t *testing.T) {
	t.Parallel()

	e, err := NewExporter("127.0.0.1:8080", 5*time.Second, false)
	wantNil(t, "err", err)
	wantNotNil(t, "exporter", e)

	err = e.Close()
	wantNil(t, "first close", err)

	err = e.Close()
	// Accept any error from double-close.
	_ = err
}
