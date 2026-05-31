package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/xtls/xray-core/app/stats/command"
)

type Exporter struct {
	sync.Mutex
	endpoint           string
	scrapeTimeout      time.Duration
	registry           *prometheus.Registry
	totalScrapes       prometheus.Counter
	metricDescriptions map[string]*prometheus.Desc
	conn               *grpc.ClientConn
}

func NewExporter(endpoint string, scrapeTimeout time.Duration, useTLS bool) (*Exporter, error) {
	e := Exporter{
		endpoint:      endpoint,
		scrapeTimeout: scrapeTimeout,
		registry:      prometheus.NewRegistry(),

		totalScrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "xray",
			Name:      "scrapes_total",
			Help:      "Total number of scrapes performed",
		}),
	}

	e.metricDescriptions = map[string]*prometheus.Desc{}

	for k, desc := range map[string]struct {
		txt  string
		lbls []string
	}{
		"up":                           {txt: "Indicate scrape succeeded or not"},
		"uptime_seconds":               {txt: "Xray uptime in seconds"},
		"goroutines":                   {txt: "Number of goroutines currently running"},
		"traffic_uplink_bytes_total":   {txt: "Number of transmitted bytes", lbls: []string{"dimension", "target"}},
		"traffic_downlink_bytes_total": {txt: "Number of received bytes", lbls: []string{"dimension", "target"}},
	} {
		e.metricDescriptions[k] = e.newMetricDescr(k, desc.txt, desc.lbls)
	}

	e.registry.MustRegister(&e)

	var creds credentials.TransportCredentials
	if useTLS {
		creds = credentials.NewTLS(nil)
	} else {
		creds = insecure.NewCredentials()
	}

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client: %w", err)
	}

	e.conn = conn
	return &e, nil
}

func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.totalScrapes.Inc()
	start := time.Now()

	var up float64 = 1
	if err := e.scrapeXray(ch); err != nil {
		up = 0
		logrus.WithError(err).Warn("Scrape failed")
	}

	e.registerConstMetricGauge(ch, "up", up)
	e.registerConstMetricGauge(ch, "scrape_duration_seconds", time.Since(start).Seconds())

	ch <- e.totalScrapes
}

func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range e.metricDescriptions {
		ch <- desc
	}
	ch <- e.totalScrapes.Desc()
}

func (e *Exporter) scrapeXray(ch chan<- prometheus.Metric) error {
	ctx, cancel := context.WithTimeout(context.Background(), e.scrapeTimeout)
	defer cancel()

	client := command.NewStatsServiceClient(e.conn)

	if err := e.scrapeXraySysMetrics(ctx, ch, client); err != nil {
		return err
	}

	if err := e.scrapeXrayMetrics(ctx, ch, client); err != nil {
		return err
	}

	return nil
}

func (e *Exporter) scrapeXrayMetrics(ctx context.Context, ch chan<- prometheus.Metric, client command.StatsServiceClient) error {
	resp, err := e.callWithRetry(func() (interface{}, error) {
		return client.QueryStats(ctx, &command.QueryStatsRequest{Reset_: false})
	})
	if err != nil {
		return fmt.Errorf("failed to get stats: %w", err)
	}

	statsResp := resp.(*command.QueryStatsResponse)
	for _, s := range statsResp.GetStat() {
		p := strings.Split(s.GetName(), ">>>")

		if p[0] == "user" {
			continue
		}

		metric := p[2] + "_" + p[3] + "_bytes_total"
		dimension := p[0]
		target := p[1]

		e.registerConstMetricCounter(ch, metric, float64(s.GetValue()), dimension, target)
	}

	return nil
}

func (e *Exporter) scrapeXraySysMetrics(ctx context.Context, ch chan<- prometheus.Metric, client command.StatsServiceClient) error {
	resp, err := e.callWithRetry(func() (interface{}, error) {
		return client.GetSysStats(ctx, &command.SysStatsRequest{})
	})
	if err != nil {
		return fmt.Errorf("failed to get sys stats: %w", err)
	}

	sysResp := resp.(*command.SysStatsResponse)
	e.registerConstMetricGauge(ch, "uptime_seconds", float64(sysResp.GetUptime()))
	e.registerConstMetricGauge(ch, "goroutines", float64(sysResp.GetNumGoroutine()))
	e.registerConstMetricGauge(ch, "memstats_alloc_bytes", float64(sysResp.GetAlloc()))
	e.registerConstMetricGauge(ch, "memstats_alloc_bytes_total", float64(sysResp.GetTotalAlloc()))
	e.registerConstMetricGauge(ch, "memstats_sys_bytes", float64(sysResp.GetSys()))
	e.registerConstMetricGauge(ch, "memstats_mallocs_total", float64(sysResp.GetMallocs()))
	e.registerConstMetricGauge(ch, "memstats_frees_total", float64(sysResp.GetFrees()))
	e.registerConstMetricGauge(ch, "memstats_num_gc", float64(sysResp.GetNumGC()))
	e.registerConstMetricGauge(ch, "memstats_pause_total_ns", float64(sysResp.GetPauseTotalNs()))

	return nil
}

func (e *Exporter) callWithRetry(fn func() (interface{}, error)) (interface{}, error) {
	maxRetries := 3
	baseDelay := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err := fn()
		if err == nil {
			return resp, nil
		}

		if attempt == maxRetries-1 {
			return nil, err
		}

		delay := baseDelay * time.Duration(1<<attempt)
		logrus.WithError(err).WithField("attempt", attempt+1).WithField("delay", delay).Debug("gRPC call failed, retrying")
		time.Sleep(delay)
	}

	return nil, fmt.Errorf("max retries exceeded")
}

func (e *Exporter) registerConstMetricGauge(ch chan<- prometheus.Metric, metric string, val float64, labels ...string) {
	e.registerConstMetric(ch, metric, val, prometheus.GaugeValue, labels...)
}

func (e *Exporter) registerConstMetricCounter(ch chan<- prometheus.Metric, metric string, val float64, labels ...string) {
	e.registerConstMetric(ch, metric, val, prometheus.CounterValue, labels...)
}

func (e *Exporter) registerConstMetric(ch chan<- prometheus.Metric, metric string, val float64, valType prometheus.ValueType, labelValues ...string) {
	descr := e.metricDescriptions[metric]
	if descr == nil {
		descr = e.newMetricDescr(metric, metric+" metric", nil)
	}

	if m, err := prometheus.NewConstMetric(descr, valType, val, labelValues...); err == nil {
		ch <- m
	} else {
		logrus.Debugf("NewConstMetric() err: %s", err)
	}
}

func (e *Exporter) newMetricDescr(metricName string, docString string, labels []string) *prometheus.Desc {
	return prometheus.NewDesc(prometheus.BuildFQName("xray", "", metricName), docString, labels, nil)
}

func (e *Exporter) Close() error {
	if e.conn != nil {
		return e.conn.Close()
	}
	return nil
}
