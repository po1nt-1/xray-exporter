package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jessevdk/go-flags"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	buildVersion = "dev"
	buildCommit  = "none"
	buildDate    = "unknown"
)

type options struct {
	Listen        string   `short:"l" long:"listen" description:"Listen address" value-name:"[ADDR]:PORT" default:"127.0.0.1:9550"`
	MetricsPath   string   `short:"m" long:"metrics-path" description:"Extra path serving the same metrics as /metrics" value-name:"PATH" default:"/scrape"`
	XrayEndpoint  string   `short:"e" long:"xray-endpoint" description:"Xray API endpoint (HOST:PORT or unix:///path)" value-name:"ENDPOINT" default:"127.0.0.1:8080"`
	ScrapeTimeout timeout  `short:"t" long:"scrape-timeout" description:"Timeout for one scrape of the Xray API" value-name:"DURATION" default:"5s"`
	TLS           bool     `long:"xray-api-tls" description:"Use TLS for the Xray gRPC connection"`
	Balancers     []string `short:"b" long:"balancer" description:"Balancer tag to report (repeatable)" value-name:"TAG"`
	UserLabel     string   `long:"user-label" description:"How users appear in labels" choice:"email" choice:"hash" choice:"none" default:"email"`
	LogLevel      string   `long:"log-level" description:"Log level" choice:"debug" choice:"info" choice:"warn" choice:"error" default:"info"`
	Version       bool     `long:"version" description:"Print the version and exit"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], nil)
	stop()
	if err != nil {
		if errors.Is(err, errHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "xray-exporter:", err)
		os.Exit(1)
	}
}

var errHelp = errors.New("help requested")

type timeout time.Duration

func (t *timeout) UnmarshalFlag(v string) error {
	if n, err := strconv.ParseUint(v, 10, 32); err == nil {
		*t = timeout(time.Duration(n) * time.Second)
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return err
	}
	*t = timeout(d)
	return nil
}

func parseArgs(args []string) (*options, error) {
	var opts options
	parser := flags.NewNamedParser("xray-exporter", flags.HelpFlag|flags.PassDoubleDash)
	if _, err := parser.AddGroup("Application Options", "", &opts); err != nil {
		return nil, err
	}
	if _, err := parser.ParseArgs(args); err != nil {
		var fe *flags.Error
		if errors.As(err, &fe) && fe.Type == flags.ErrHelp {
			fmt.Println(fe.Message)
			return nil, errHelp
		}
		return nil, err
	}
	if opts.ScrapeTimeout <= 0 {
		return nil, fmt.Errorf("scrape timeout must be positive")
	}
	if opts.MetricsPath == "" || opts.MetricsPath[0] != '/' {
		return nil, fmt.Errorf("metrics path must start with /")
	}
	return &opts, nil
}

func run(ctx context.Context, args []string, ready func(net.Addr)) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}
	if opts.Version {
		fmt.Printf("xray-exporter %s (commit %s, built %s)\n", buildVersion, buildCommit, buildDate)
		return nil
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(opts.LogLevel)); err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	exporter, err := NewExporter(Config{
		Endpoint:  opts.XrayEndpoint,
		Timeout:   time.Duration(opts.ScrapeTimeout),
		TLS:       opts.TLS,
		Balancers: opts.Balancers,
		UserLabel: opts.UserLabel,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := exporter.Close(); err != nil {
			slog.Warn("close gRPC connection", "err", err)
		}
	}()

	lis, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           newMux(exporter, opts.MetricsPath),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      time.Duration(opts.ScrapeTimeout) + 10*time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	errc := make(chan error, 1)
	go func() { errc <- server.Serve(lis) }()
	slog.Info("started", "version", buildVersion, "listen", lis.Addr().String(), "xray", opts.XrayEndpoint)
	if ready != nil {
		ready(lis.Addr())
	}

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}

func newRegistry(exporter prometheus.Collector) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		exporter,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        "exporter_build_info",
			Help:        "Exporter build information.",
			ConstLabels: prometheus.Labels{"version": buildVersion, "commit": buildCommit},
		}, func() float64 { return 1 }),
	)
	return reg
}

func newMux(exporter prometheus.Collector, metricsPath string) *http.ServeMux {
	metrics := promhttp.HandlerFor(newRegistry(exporter), promhttp.HandlerOpts{
		ErrorHandling:       promhttp.ContinueOnError,
		MaxRequestsInFlight: 2,
	})
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics)
	if metricsPath != "/metrics" {
		mux.Handle(metricsPath, metrics)
	}
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "OK\n")
	})
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, "<html><head><title>Xray Exporter</title></head><body><h1>Xray Exporter %s</h1><p><a href=\"%s\">Metrics</a></p></body></html>\n",
			html.EscapeString(buildVersion), html.EscapeString(metricsPath))
	})
	return mux
}
