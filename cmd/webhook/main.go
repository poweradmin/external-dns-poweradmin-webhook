package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"

	"github.com/poweradmin/external-dns-poweradmin-webhook/internal/poweradmin"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/provider/webhook/api"
)

const banner = `
 ____                         _       _           _
|  _ \ _____      _____ _ __ / \   __| |_ __ ___ (_)_ __
| |_) / _ \ \ /\ / / _ \ '__/ _ \ / _' | '_ ' _ \| | '_ \
|  __/ (_) \ V  V /  __/ | / ___ \ (_| | | | | | | | | | |
|_|   \___/ \_/\_/ \___|_|/_/   \_\__,_|_| |_| |_|_|_| |_|

 external-dns-poweradmin-webhook
 version: %s

`

var Version = "dev"

// Config holds the configuration for the webhook
type Config struct {
	// Server configuration
	ServerHost string `env:"SERVER_HOST" envDefault:"localhost"`
	ServerPort int    `env:"SERVER_PORT" envDefault:"8888"`

	// Metrics/Health server configuration
	MetricsHost string `env:"METRICS_HOST" envDefault:"0.0.0.0"`
	MetricsPort int    `env:"METRICS_PORT" envDefault:"8080"`

	// Server timeouts
	ServerReadTimeout  time.Duration `env:"SERVER_READ_TIMEOUT" envDefault:"5s"`
	ServerWriteTimeout time.Duration `env:"SERVER_WRITE_TIMEOUT" envDefault:"10s"`

	// PowerAdmin configuration
	PowerAdminURL        string `env:"POWERADMIN_URL,required"`
	PowerAdminAPIKey     string `env:"POWERADMIN_API_KEY,required"`
	PowerAdminAPIVersion string `env:"POWERADMIN_API_VERSION" envDefault:"v2"`

	// Domain filter configuration
	DomainFilter        []string `env:"DOMAIN_FILTER" envSeparator:","`
	ExcludeDomainFilter []string `env:"EXCLUDE_DOMAIN_FILTER" envSeparator:","`
	RegexpDomainFilter  string   `env:"REGEXP_DOMAIN_FILTER"`

	// Logging
	LogLevel  string `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat string `env:"LOG_FORMAT" envDefault:"text"`

	// Dry run mode
	DryRun bool `env:"DRY_RUN" envDefault:"false"`
}

func main() {
	fmt.Printf(banner, Version)

	// Parse configuration from environment
	cfg := Config{}
	if err := env.Parse(&cfg); err != nil {
		log.Fatalf("Failed to parse configuration: %v", err)
	}

	// Configure logging
	setupLogging(cfg.LogLevel, cfg.LogFormat)

	log.Infof("Starting external-dns-poweradmin-webhook version %s", Version)

	// Create domain filter
	// Regex filter overrides regular domain filter (mutually exclusive)
	var domainFilter *endpoint.DomainFilter
	if cfg.RegexpDomainFilter != "" {
		log.Infof("Using regex domain filter: %s", cfg.RegexpDomainFilter)
		domainFilter = endpoint.NewRegexDomainFilter(
			regexp.MustCompile(cfg.RegexpDomainFilter),
			nil,
		)
	} else {
		domainFilter = endpoint.NewDomainFilterWithExclusions(cfg.DomainFilter, cfg.ExcludeDomainFilter)
		if len(cfg.DomainFilter) > 0 {
			log.Infof("Using domain filter: %v", cfg.DomainFilter)
		}
		if len(cfg.ExcludeDomainFilter) > 0 {
			log.Infof("Using exclude domain filter: %v", cfg.ExcludeDomainFilter)
		}
	}

	// Parse and validate API version
	apiVersion := poweradmin.APIVersion(cfg.PowerAdminAPIVersion)
	if apiVersion != poweradmin.APIVersionV1 && apiVersion != poweradmin.APIVersionV2 {
		log.Fatalf("Invalid API version %s, must be 'v1' or 'v2'", cfg.PowerAdminAPIVersion)
	}
	log.Infof("Using PowerAdmin API version: %s", apiVersion)

	// Create provider
	provider, err := poweradmin.NewProvider(
		cfg.PowerAdminURL,
		cfg.PowerAdminAPIKey,
		apiVersion,
		domainFilter,
		cfg.DryRun,
	)
	if err != nil {
		log.Fatalf("Failed to create provider: %v", err)
	}

	// Create webhook server
	webhookServer := api.WebhookServer{
		Provider: provider,
	}

	// Setup webhook router
	webhookRouter := chi.NewRouter()
	webhookRouter.Get("/", webhookServer.NegotiateHandler)
	webhookRouter.Get("/records", webhookServer.RecordsHandler)
	webhookRouter.Post("/records", webhookServer.RecordsHandler)
	webhookRouter.Post("/adjustendpoints", webhookServer.AdjustEndpointsHandler)

	// Setup metrics/health router
	metricsRouter := chi.NewRouter()
	metricsRouter.Get("/healthz", healthHandler)
	metricsRouter.Get("/readyz", healthHandler)
	metricsRouter.Handle("/metrics", promhttp.Handler())

	// Create HTTP servers
	webhookHTTPServer := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.ServerHost, cfg.ServerPort),
		Handler:      webhookRouter,
		ReadTimeout:  cfg.ServerReadTimeout,
		WriteTimeout: cfg.ServerWriteTimeout,
	}

	metricsHTTPServer := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.MetricsHost, cfg.MetricsPort),
		Handler:      metricsRouter,
		ReadTimeout:  cfg.ServerReadTimeout,
		WriteTimeout: cfg.ServerWriteTimeout,
	}

	// Start servers
	go func() {
		log.Infof("Starting webhook server on %s", webhookHTTPServer.Addr)
		if err := webhookHTTPServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Webhook server error: %v", err)
		}
	}()

	go func() {
		log.Infof("Starting metrics server on %s", metricsHTTPServer.Addr)
		if err := metricsHTTPServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Metrics server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	log.Infof("Received signal %v, shutting down", sig)

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := webhookHTTPServer.Shutdown(ctx); err != nil {
		log.Errorf("Error shutting down webhook server: %v", err)
	}
	if err := metricsHTTPServer.Shutdown(ctx); err != nil {
		log.Errorf("Error shutting down metrics server: %v", err)
	}

	log.Info("Shutdown complete")
}

func setupLogging(level, format string) {
	// Set log level
	logLevel, err := log.ParseLevel(level)
	if err != nil {
		log.Warnf("Invalid log level %s, using info", level)
		logLevel = log.InfoLevel
	}
	log.SetLevel(logLevel)

	// Set log format
	if format == "json" {
		log.SetFormatter(&log.JSONFormatter{})
	} else {
		log.SetFormatter(&log.TextFormatter{
			FullTimestamp: true,
		})
	}
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}
