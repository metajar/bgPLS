package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"
	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"github.com/bgpls/bgpls/gen/bgpls/v1/bgplsv1connect"
	"github.com/bgpls/bgpls/internal/utilization"
	gnmicol "github.com/bgpls/bgpls/internal/utilization/gnmi"
	snmpcol "github.com/bgpls/bgpls/internal/utilization/snmp"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server             Server   `yaml:"server"`
	SampleIntervalText string   `yaml:"sample_interval"`
	ReportIntervalText string   `yaml:"report_interval"`
	MetricsListen      string   `yaml:"metrics_listen"`
	Targets            []Target `yaml:"targets"`
	SampleInterval     time.Duration
	ReportInterval     time.Duration
}

type Server struct {
	URL  string `yaml:"url"`
	CA   string `yaml:"ca"`
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

type Target struct {
	Name      string `yaml:"name"`
	Driver    string `yaml:"driver"`
	Address   string `yaml:"address"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	Community string `yaml:"community"`
	TLS       TLS    `yaml:"tls"`
}

type TLS struct {
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
	Insecure           bool `yaml:"insecure"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("utilcol failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("utilcol", flag.ContinueOnError)
	path := fs.String("config", "utilcol.yaml", "configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*path)
	if err != nil {
		return err
	}
	collectors, err := buildCollectors(cfg)
	if err != nil {
		return err
	}
	httpClient, err := tlsClient(cfg.Server)
	if err != nil {
		return err
	}
	client := bgplsv1connect.NewEnrichmentServiceClient(httpClient, cfg.Server.URL)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if cfg.MetricsListen != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		metrics := &http.Server{Addr: cfg.MetricsListen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := metrics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("utilcol metrics server failed", "error", err)
			}
		}()
		defer metrics.Shutdown(context.Background())
	}
	samples := make(chan utilization.InterfaceSample, 1024)
	reg := utilization.NewRegistry(collectors...)
	go func() {
		if err := reg.Run(ctx, samples); err != nil {
			slog.Error("collector registry stopped", "error", err)
		}
	}()
	return reportLoop(ctx, cfg, client, samples)
}

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read utilcol config: %w", err)
	}
	cfg := Config{SampleInterval: 10 * time.Second, ReportInterval: 10 * time.Second, MetricsListen: "0.0.0.0:9091"}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse utilcol config: %w", err)
	}
	if cfg.SampleIntervalText != "" {
		cfg.SampleInterval, err = time.ParseDuration(cfg.SampleIntervalText)
		if err != nil {
			return Config{}, fmt.Errorf("sample_interval: %w", err)
		}
	}
	if cfg.ReportIntervalText != "" {
		cfg.ReportInterval, err = time.ParseDuration(cfg.ReportIntervalText)
		if err != nil {
			return Config{}, fmt.Errorf("report_interval: %w", err)
		}
	}
	if cfg.Server.URL == "" {
		return Config{}, errors.New("server.url is required")
	}
	if len(cfg.Targets) == 0 {
		return Config{}, errors.New("at least one target is required")
	}
	return cfg, nil
}

func buildCollectors(cfg Config) ([]utilization.Collector, error) {
	out := make([]utilization.Collector, 0, len(cfg.Targets))
	for _, t := range cfg.Targets {
		switch t.Driver {
		case "gnmi":
			c, err := gnmicol.New(gnmicol.Config{
				Name: t.Name, Address: t.Address, Username: t.Username, Password: t.Password,
				SampleInterval: cfg.SampleInterval, Insecure: t.TLS.Insecure, InsecureSkipVerify: t.TLS.InsecureSkipVerify,
			})
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		case "snmp":
			c, err := snmpcol.New(snmpcol.Config{Name: t.Name, Address: t.Address, Community: t.Community, SampleInterval: cfg.SampleInterval})
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		default:
			return nil, fmt.Errorf("target %q has unknown driver %q", t.Name, t.Driver)
		}
	}
	return out, nil
}

func tlsClient(cfg Server) (*http.Client, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, err
	}
	if cfg.CA != "" {
		data, err := os.ReadFile(cfg.CA)
		if err != nil {
			return nil, err
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, errors.New("server CA has no certificates")
		}
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool}
	if cfg.Cert != "" || cfg.Key != "" {
		cert, err := tls.LoadX509KeyPair(cfg.Cert, cfg.Key)
		if err != nil {
			return nil, err
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}, Timeout: 20 * time.Second}, nil
}

func reportLoop(ctx context.Context, cfg Config, client bgplsv1connect.EnrichmentServiceClient, samples <-chan utilization.InterfaceSample) error {
	tracker := utilization.NewTracker()
	latest := map[utilization.SampleKey]*bgplsv1.InterfaceUtilization{}
	flush := time.NewTicker(cfg.ReportInterval)
	defer flush.Stop()
	send := func() {
		if len(latest) == 0 {
			return
		}
		req := &bgplsv1.ReportInterfaceUtilizationRequest{}
		for _, v := range latest {
			req.Interfaces = append(req.Interfaces, v)
		}
		_, err := client.ReportInterfaceUtilization(ctx, connect.NewRequest(req))
		if err != nil {
			slog.Warn("utilization report failed", "error", err, "batch", len(req.Interfaces))
			utilization.ObserveBatch("error")
			return
		}
		utilization.ObserveBatch("ok")
		latest = map[utilization.SampleKey]*bgplsv1.InterfaceUtilization{}
	}
	for {
		select {
		case <-ctx.Done():
			send()
			return nil
		case <-flush.C:
			send()
		case sample, ok := <-samples:
			if !ok {
				send()
				return nil
			}
			utilization.ObserveSample(sample.Device)
			result := tracker.Observe(sample)
			if !result.OK {
				utilization.ObserveDiscard(result.Reason)
				continue
			}
			key := utilization.SampleKey{Device: sample.Device, InterfaceName: sample.InterfaceName}
			latest[key] = &bgplsv1.InterfaceUtilization{
				Device:        sample.Device,
				InterfaceName: sample.InterfaceName,
				Ipv4Addresses: append([]string(nil), sample.IPv4Addrs...),
				Ipv6Addresses: append([]string(nil), sample.IPv6Addrs...),
				SpeedBps:      sample.SpeedBps,
				InBps:         result.InBps,
				OutBps:        result.OutBps,
				ObservedAt:    timestamppb.New(sample.Timestamp),
			}
		}
	}
}
