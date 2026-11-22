package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	obscmd "github.com/xtls/xray-core/app/observatory/command"
	routercmd "github.com/xtls/xray-core/app/router/command"
	statscmd "github.com/xtls/xray-core/app/stats/command"
)

const namespace = "xray"

const (
	UserLabelEmail = "email"
	UserLabelHash  = "hash"
	UserLabelNone  = "none"
)

type Config struct {
	Endpoint  string
	Timeout   time.Duration
	TLS       bool
	Balancers []string
	UserLabel string
}

type Exporter struct {
	cfg     Config
	conn    *grpc.ClientConn
	stats   statscmd.StatsServiceClient
	obs     obscmd.ObservatoryServiceClient
	routing routercmd.RoutingServiceClient

	mu       sync.Mutex
	scrapes  prometheus.Counter
	failed   map[string]prometheus.Counter
	picked   map[string]string
	switches *prometheus.CounterVec
}

type collector struct {
	name string
	run  func(*Exporter, context.Context, chan<- prometheus.Metric) error
}

var sources = []collector{
	{"sys", (*Exporter).collectSys},
	{"stats", (*Exporter).collectStats},
	{"users", (*Exporter).collectUsers},
	{"observatory", (*Exporter).collectObservatory},
	{"balancer", (*Exporter).collectBalancers},
}

func desc(name, help string, labels ...string) *prometheus.Desc {
	return prometheus.NewDesc(prometheus.BuildFQName(namespace, "", name), help, labels, nil)
}

var (
	descUp             = desc("up", "Whether the Xray API answered the last scrape.")
	descScrapeDuration = desc("scrape_duration_seconds", "Time spent querying the Xray API.")
	descCollectorOK    = desc("scrape_collector_success", "Whether a collector succeeded (0 also when the Xray service is disabled).", "collector")

	descUptime     = desc("uptime_seconds", "Xray uptime.")
	descGoroutines = desc("goroutines", "Number of Xray goroutines.")
	descAlloc      = desc("memstats_alloc_bytes", "Xray heap bytes allocated and in use.")
	descAllocTotal = desc("memstats_alloc_bytes_total", "Xray heap bytes allocated in total.")
	descSys        = desc("memstats_sys_bytes", "Bytes obtained by Xray from the OS.")
	descMallocs    = desc("memstats_mallocs_total", "Xray heap allocations in total.")
	descFrees      = desc("memstats_frees_total", "Xray heap frees in total.")
	descLive       = desc("memstats_live_objects", "Xray live heap objects.")
	descNumGC      = desc("memstats_gc_total", "Completed Xray GC cycles.")
	descPauseTotal = desc("memstats_gc_pause_seconds_total", "Total Xray GC pause time.")

	descUplink   = desc("traffic_uplink_bytes_total", "Bytes sent by the client side through the target.", "dimension", "target")
	descDownlink = desc("traffic_downlink_bytes_total", "Bytes received by the client side through the target.", "dimension", "target")

	descUsersOnline = desc("users_online", "Users with at least one live connection.")
	descUserIPs     = desc("user_online_ips", "Distinct source IPs of a user with live connections.", "user")

	descObsAlive    = desc("observatory_outbound_alive", "Whether the last observatory probe through the outbound succeeded.", "outbound")
	descObsDelay    = desc("observatory_outbound_delay_seconds", "Duration of the last successful observatory probe.", "outbound")
	descObsLastSeen = desc("observatory_outbound_last_seen_timestamp_seconds", "Unix time of the last successful probe.", "outbound")
	descObsLastTry  = desc("observatory_outbound_last_try_timestamp_seconds", "Unix time of the last probe attempt.", "outbound")
	descHPTotal     = desc("observatory_health_ping_samples", "Burst observatory pings in the sampling window.", "outbound")
	descHPFail      = desc("observatory_health_ping_failures", "Failed burst observatory pings in the sampling window.", "outbound")
	descHPRTT       = desc("observatory_health_ping_rtt_seconds", "Burst observatory RTT statistics over the sampling window.", "outbound", "stat")

	descBalancerSelected = desc("balancer_selected", "Outbound the balancer strategy picks now (1 per picked outbound).", "balancer", "outbound")
	descBalancerFallback = desc("balancer_fallback", "1 when the strategy picks nothing and the fallback outbound is used.", "balancer")
	descBalancerOverride = desc("balancer_override", "Outbound forced through the API override.", "balancer", "outbound")
)

func NewExporter(cfg Config) (*Exporter, error) {
	switch cfg.UserLabel {
	case "":
		cfg.UserLabel = UserLabelEmail
	case UserLabelEmail, UserLabelHash, UserLabelNone:
	default:
		return nil, fmt.Errorf("unknown user label mode %q", cfg.UserLabel)
	}

	creds := insecure.NewCredentials()
	if cfg.TLS {
		creds = credentials.NewTLS(nil)
	}
	conn, err := grpc.NewClient(cfg.Endpoint, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("create gRPC client: %w", err)
	}

	e := &Exporter{
		cfg:     cfg,
		conn:    conn,
		stats:   statscmd.NewStatsServiceClient(conn),
		obs:     obscmd.NewObservatoryServiceClient(conn),
		routing: routercmd.NewRoutingServiceClient(conn),
		scrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "scrapes_total", Help: "Scrapes of the Xray API.",
		}),
		failed: map[string]prometheus.Counter{},
		picked: map[string]string{},
		switches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "balancer_switches_total",
			Help: "Changes of the balancer pick (strategy target, fallback or override) seen between scrapes.",
		}, []string{"balancer"}),
	}
	for _, b := range cfg.Balancers {
		e.switches.WithLabelValues(b)
	}
	for _, c := range sources {
		e.failed[c.name] = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "scrape_collector_errors_total",
			Help:        "Failed collector runs, excluding disabled Xray services.",
			ConstLabels: prometheus.Labels{"collector": c.name},
		})
	}
	return e, nil
}

func (e *Exporter) Close() error {
	if e.conn == nil {
		return nil
	}
	err := e.conn.Close()
	e.conn = nil
	return err
}

func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		descUp, descScrapeDuration, descCollectorOK,
		descUptime, descGoroutines, descAlloc, descAllocTotal, descSys, descMallocs, descFrees, descLive, descNumGC, descPauseTotal,
		descUplink, descDownlink, descUsersOnline, descUserIPs,
		descObsAlive, descObsDelay, descObsLastSeen, descObsLastTry, descHPTotal, descHPFail, descHPRTT,
		descBalancerSelected, descBalancerFallback, descBalancerOverride,
	} {
		ch <- d
	}
	ch <- e.scrapes.Desc()
	for _, c := range e.failed {
		ch <- c.Desc()
	}
	e.switches.Describe(ch)
}

func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.scrapes.Inc()
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.Timeout)
	defer cancel()

	up := 0.0
	for _, c := range sources {
		err := c.run(e, ctx, ch)
		ok := 0.0
		switch {
		case err == nil:
			ok = 1
		case disabled(err):
			slog.Debug("xray service disabled", "collector", c.name, "err", err)
		default:
			e.failed[c.name].Inc()
			slog.Warn("collector failed", "collector", c.name, "err", err)
		}
		if c.name == "sys" {
			up = ok
		}
		ch <- prometheus.MustNewConstMetric(descCollectorOK, prometheus.GaugeValue, ok, c.name)
		if c.name == "sys" && up == 0 {
			for _, rest := range sources[1:] {
				ch <- prometheus.MustNewConstMetric(descCollectorOK, prometheus.GaugeValue, 0, rest.name)
			}
			break
		}
	}

	ch <- prometheus.MustNewConstMetric(descUp, prometheus.GaugeValue, up)
	ch <- prometheus.MustNewConstMetric(descScrapeDuration, prometheus.GaugeValue, time.Since(start).Seconds())
	ch <- e.scrapes
	for _, c := range e.failed {
		ch <- c
	}
	e.switches.Collect(ch)
}

func disabled(err error) bool {
	return status.Code(err) == codes.Unimplemented
}

func gauge(ch chan<- prometheus.Metric, d *prometheus.Desc, v float64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
}

func counter(ch chan<- prometheus.Metric, d *prometheus.Desc, v float64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v, labels...)
}

func (e *Exporter) collectSys(ctx context.Context, ch chan<- prometheus.Metric) error {
	r, err := e.stats.GetSysStats(ctx, &statscmd.SysStatsRequest{})
	if err != nil {
		return err
	}
	gauge(ch, descUptime, float64(r.GetUptime()))
	gauge(ch, descGoroutines, float64(r.GetNumGoroutine()))
	gauge(ch, descAlloc, float64(r.GetAlloc()))
	counter(ch, descAllocTotal, float64(r.GetTotalAlloc()))
	gauge(ch, descSys, float64(r.GetSys()))
	counter(ch, descMallocs, float64(r.GetMallocs()))
	counter(ch, descFrees, float64(r.GetFrees()))
	gauge(ch, descLive, float64(r.GetLiveObjects()))
	counter(ch, descNumGC, float64(r.GetNumGC()))
	counter(ch, descPauseTotal, float64(r.GetPauseTotalNs())/1e9)
	return nil
}

func (e *Exporter) collectStats(ctx context.Context, ch chan<- prometheus.Metric) error {
	r, err := e.stats.QueryStats(ctx, &statscmd.QueryStatsRequest{})
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, s := range r.GetStat() {
		p := strings.Split(s.GetName(), ">>>")
		if len(p) != 4 || p[2] != "traffic" {
			continue
		}
		dim, target := p[0], p[1]
		switch dim {
		case "inbound", "outbound":
		case "user":
			if e.cfg.UserLabel == UserLabelNone {
				continue
			}
			target = e.user(target)
		default:
			continue
		}
		var d *prometheus.Desc
		switch p[3] {
		case "uplink":
			d = descUplink
		case "downlink":
			d = descDownlink
		default:
			continue
		}
		key := dim + "\x00" + target + "\x00" + p[3]
		if seen[key] {
			continue
		}
		seen[key] = true
		counter(ch, d, float64(s.GetValue()), dim, target)
	}
	return nil
}

func (e *Exporter) collectUsers(ctx context.Context, ch chan<- prometheus.Metric) error {
	r, err := e.stats.GetUsersStats(ctx, &statscmd.GetUsersStatsRequest{})
	if err != nil {
		return err
	}
	online := 0
	perUser := map[string]int{}
	for _, u := range r.GetUsers() {
		if len(u.GetIps()) == 0 {
			continue
		}
		online++
		if e.cfg.UserLabel != UserLabelNone {
			perUser[e.user(u.GetEmail())] += len(u.GetIps())
		}
	}
	gauge(ch, descUsersOnline, float64(online))
	for u, n := range perUser {
		gauge(ch, descUserIPs, float64(n), u)
	}
	return nil
}

func (e *Exporter) collectObservatory(ctx context.Context, ch chan<- prometheus.Metric) error {
	r, err := e.obs.GetOutboundStatus(ctx, &obscmd.GetOutboundStatusRequest{})
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, s := range r.GetStatus().GetStatus() {
		tag := s.GetOutboundTag()
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		alive := 0.0
		if s.GetAlive() {
			alive = 1
			gauge(ch, descObsDelay, float64(s.GetDelay())/1e3, tag)
		}
		gauge(ch, descObsAlive, alive, tag)
		if t := s.GetLastSeenTime(); t > 0 {
			gauge(ch, descObsLastSeen, float64(t), tag)
		}
		if t := s.GetLastTryTime(); t > 0 {
			gauge(ch, descObsLastTry, float64(t), tag)
		}
		if hp := s.GetHealthPing(); hp != nil && hp.GetAll() > 0 {
			gauge(ch, descHPTotal, float64(hp.GetAll()), tag)
			gauge(ch, descHPFail, float64(hp.GetFail()), tag)
			if hp.GetAll() > hp.GetFail() {
				gauge(ch, descHPRTT, float64(hp.GetAverage())/1e9, tag, "avg")
				gauge(ch, descHPRTT, float64(hp.GetMin())/1e9, tag, "min")
				gauge(ch, descHPRTT, float64(hp.GetMax())/1e9, tag, "max")
				gauge(ch, descHPRTT, float64(hp.GetDeviation())/1e9, tag, "stddev")
			}
		}
	}
	return nil
}

func (e *Exporter) collectBalancers(ctx context.Context, ch chan<- prometheus.Metric) error {
	var errs []error
	for _, tag := range e.cfg.Balancers {
		r, err := e.routing.GetBalancerInfo(ctx, &routercmd.GetBalancerInfoRequest{Tag: tag})
		if err != nil {
			if disabled(err) {
				return err
			}
			errs = append(errs, fmt.Errorf("balancer %q: %w", tag, err))
			continue
		}
		b := r.GetBalancer()
		override := b.GetOverride().GetTarget()
		if override != "" {
			gauge(ch, descBalancerOverride, 1, tag, override)
		}
		var picked []string
		seen := map[string]bool{}
		for _, t := range b.GetPrincipleTarget().GetTag() {
			if t == "" || seen[t] {
				continue
			}
			seen[t] = true
			picked = append(picked, t)
			gauge(ch, descBalancerSelected, 1, tag, t)
		}
		fallback := 0.0
		if b.GetPrincipleTarget() != nil && len(picked) == 0 {
			fallback = 1
		}
		gauge(ch, descBalancerFallback, fallback, tag)

		slices.Sort(picked)
		state := "pick:" + strings.Join(picked, ",")
		if override != "" {
			state = "override:" + override
		}
		if prev, ok := e.picked[tag]; ok && prev != state {
			e.switches.WithLabelValues(tag).Inc()
		}
		e.picked[tag] = state
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d of %d balancers failed: %w", len(errs), len(e.cfg.Balancers), errs[0])
	}
	return nil
}

func (e *Exporter) user(email string) string {
	if e.cfg.UserLabel == UserLabelHash {
		sum := sha256.Sum256([]byte(email))
		return hex.EncodeToString(sum[:6])
	}
	return email
}
