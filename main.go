package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jessevdk/go-flags"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

var opts struct {
	Listen                 string `short:"l" long:"listen" description:"Listen address" value-name:"[ADDR]:PORT" default:"127.0.0.1:9550"`
	MetricsPath            string `short:"m" long:"metrics-path" description:"Metrics path" value-name:"PATH" default:"/scrape"`
	XRayEndpoint           string `short:"e" long:"xray-endpoint" description:"Xray API endpoint" value-name:"HOST:PORT" default:"127.0.0.1:8080"`
	ScrapeTimeoutInSeconds int64  `short:"t" long:"scrape-timeout" description:"The timeout in seconds for every individual scrape" value-name:"N" default:"5"`
	UseTLS                 bool   `long:"xray-api-tls" description:"Use TLS for the Xray gRPC connection"`
	Version                bool   `long:"version" description:"Display the version and exit"`
}

var (
	buildVersion = "dev"
	buildCommit  = "none"
	buildDate    = "unknown"
)

func scrapeHandler(exporter *Exporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		promhttp.HandlerFor(
			exporter.registry, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError},
		).ServeHTTP(w, r)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func main() {
	if _, err := flags.Parse(&opts); err != nil {
		if flagsErr, ok := err.(*flags.Error); ok && flagsErr.Type == flags.ErrHelp {
			return
		}
		logrus.WithError(err).Fatal("Failed to parse flags")
	}

	logrus.Infof("Xray Exporter %v-%v (built %v)", buildVersion, buildCommit, buildDate)

	if opts.Version {
		return
	}

	scrapeTimeout := time.Duration(opts.ScrapeTimeoutInSeconds) * time.Second

	exporter, err := NewExporter(opts.XRayEndpoint, scrapeTimeout, opts.UseTLS)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create exporter")
	}
	defer exporter.Close()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(
		exporter.registry, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError},
	))
	mux.Handle(opts.MetricsPath, scrapeHandler(exporter))
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html>
<head><title>Xray Exporter</title></head>
<body>
<h1>Xray Exporter %s</h1>
<p><a href='/metrics'>Exporter Metrics</a></p>
<p><a href='%s'>Scrape Xray Metrics</a></p>
<p><a href='/health'>Health Check</a></p>
</body>
</html>
`, buildVersion, opts.MetricsPath)
	})

	server := &http.Server{
		Addr:         opts.Listen,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		logrus.Infof("Server starting on %s", opts.Listen)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logrus.WithError(err).Fatal("Server failed to start")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logrus.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logrus.WithError(err).Error("Server forced to shutdown")
	}

	logrus.Info("Server exited")
}
