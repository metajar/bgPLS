package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"connectrpc.com/connect"
	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"github.com/bgpls/bgpls/gen/bgpls/v1/bgplsv1connect"
	apiServer "github.com/bgpls/bgpls/internal/api"
	bgpcollector "github.com/bgpls/bgpls/internal/bgp"
	"github.com/bgpls/bgpls/internal/config"
	"github.com/bgpls/bgpls/internal/store"
	"github.com/bgpls/bgpls/internal/utilization"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("bgPLS failed", "error", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "topology":
		return topologyCommand(args[1:])
	case "path":
		return pathCommand(args[1:])
	case "history":
		return historyCommand(args[1:])
	case "peers":
		return peersCommand(args[1:])
	case "health":
		return healthCommand(args[1:])
	case "version":
		fmt.Println(version)
		return nil
	default:
		return usage()
	}
}
func usage() error {
	fmt.Fprintln(os.Stderr, "usage: bgpls <serve|topology|path|history|peers|health|version> [options]")
	return errors.New("command is required")
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	path := fs.String("config", "bgpls.yaml", "configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return err
	}
	s, err := store.Open(filepath.Join(cfg.DataDir, "topology"))
	if err != nil {
		return err
	}
	defer s.Close()
	overlay, err := utilization.Open(filepath.Join(cfg.DataDir, "utilization"), utilization.Options{StaleAfter: cfg.Utilization.StaleAfter, SweepAfter: cfg.Utilization.SweepAfter})
	if err != nil {
		return err
	}
	defer overlay.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if s.Revision() == 0 {
		for _, d := range cfg.Domains {
			if _, err := s.Apply(ctx, store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_UNSPECIFIED, ID: d.ID, Value: d.Proto(), Reason: "domain bootstrapped from configuration"}); err != nil {
				return err
			}
		}
		for _, p := range cfg.Peers {
			peer := p.Proto()
			if _, err := s.Apply(ctx, store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_PEER, ID: peer.Id, DomainID: peer.DomainId, Value: peer, Reason: "peer bootstrapped from configuration"}); err != nil {
				return err
			}
		}
	}
	collector := bgpcollector.New(cfg.BGP, s)
	if err := collector.Start(ctx, s.Snapshot().Peers); err != nil {
		return err
	}
	defer collector.Close(context.Background())
	authorizer := apiServer.NewAuthorizer(cfg.API.RBAC, cfg.API.TLS.DevelopmentInsecure, cfg.API.TLS.AllowAnonymousReader)
	handler := apiServer.NewHandlerWithOverlay(s, collector, version, time.Now().UTC(), authorizer, overlay)
	server := &http.Server{Addr: cfg.API.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	errCh := make(chan error, 3)
	if cfg.API.MetricsListen != "" {
		metrics := &http.Server{Addr: cfg.API.MetricsListen, Handler: promhttp.Handler(), ReadHeaderTimeout: 5 * time.Second}
		go func() { errCh <- metrics.ListenAndServe() }()
		defer metrics.Shutdown(context.Background())
	}
	if cfg.API.UIListen != "" {
		uiAuthorizer := apiServer.NewAuthorizer(cfg.API.RBAC, false, true)
		uiHandler := apiServer.NewHandlerWithOverlay(s, collector, version, time.Now().UTC(), uiAuthorizer, overlay)
		uiServer := &http.Server{Addr: cfg.API.UIListen, Handler: uiHandler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
		go func() {
			slog.Info("topology UI listening without client certificates", "address", cfg.API.UIListen, "ui", "http://"+cfg.API.UIListen+"/ui/")
			errCh <- uiServer.ListenAndServe()
		}()
		defer uiServer.Shutdown(context.Background())
	}
	if cfg.API.TLS.DevelopmentInsecure {
		go func() {
			slog.Info("API listening without TLS for development", "address", cfg.API.Listen, "ui", "http://"+cfg.API.Listen+"/ui/")
			errCh <- server.ListenAndServe()
		}()
	} else {
		tlsCfg, err := apiServer.TLSConfig(cfg.API.TLS)
		if err != nil {
			return err
		}
		var active atomic.Pointer[tls.Config]
		active.Store(tlsCfg)
		outer := tlsCfg.Clone()
		outer.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) { return active.Load(), nil }
		listener, err := net.Listen("tcp", cfg.API.Listen)
		if err != nil {
			return err
		}
		tlsListener := tls.NewListener(listener, outer)
		go func() {
			if cfg.API.TLS.AllowAnonymousReader {
				slog.Info("TLS API listening with optional client certificates", "address", cfg.API.Listen, "ui", "https://"+cfg.API.Listen+"/ui/")
			} else {
				slog.Info("mTLS API listening", "address", cfg.API.Listen, "ui", "https://"+cfg.API.Listen+"/ui/")
			}
			errCh <- server.Serve(tlsListener)
		}()
		hup := make(chan os.Signal, 1)
		signal.Notify(hup, syscall.SIGHUP)
		defer signal.Stop(hup)
		go func() {
			for range hup {
				reloaded, err := config.Load(*path)
				if err != nil {
					slog.Error("configuration reload failed", "error", err)
					continue
				}
				nextTLS, err := apiServer.TLSConfig(reloaded.API.TLS)
				if err != nil {
					slog.Error("TLS reload failed", "error", err)
					continue
				}
				active.Store(nextTLS)
				authorizer.Reload(reloaded.API.RBAC, reloaded.API.TLS.AllowAnonymousReader)
				slog.Info("TLS certificates and RBAC mappings reloaded")
			}
		}()
	}
	go overlay.Serve(ctx)
	go watchUtilizationIndex(ctx, overlay, s)
	go retentionLoop(ctx, s, cfg.Retention)
	select {
	case <-ctx.Done():
		shutdownCtx, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
func watchUtilizationIndex(ctx context.Context, overlay *utilization.Overlay, s *store.Store) {
	overlay.RebuildIndex(s.Snapshot().Links)
	events, err := s.Subscribe(ctx, s.Revision())
	if err != nil {
		slog.Error("utilization index subscription failed", "error", err)
		return
	}
	for event := range events {
		if event.EntityKind != bgplsv1.EntityKind_ENTITY_KIND_LINK {
			continue
		}
		if event.Operation == "DELETE" {
			overlay.RemoveLink(event.EntityId)
			continue
		}
		var link bgplsv1.Link
		if json.Unmarshal(event.AfterJson, &link) != nil {
			continue
		}
		overlay.IndexLink(&link)
	}
}

func retentionLoop(ctx context.Context, s *store.Store, r config.Retention) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.CompactHistory(ctx, time.Now().Add(-r.Duration), r.MaxBytes); err != nil {
				slog.Error("history retention failed", "error", err)
			}
		}
	}
}

type clientFlags struct{ server, ca, cert, key string }

func addClientFlags(fs *flag.FlagSet) *clientFlags {
	c := &clientFlags{}
	fs.StringVar(&c.server, "server", "https://127.0.0.1:7443", "bgPLS API base URL")
	fs.StringVar(&c.ca, "ca", "", "server CA PEM")
	fs.StringVar(&c.cert, "cert", "", "client certificate PEM")
	fs.StringVar(&c.key, "key", "", "client private key PEM")
	return c
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func (c *clientFlags) applyLabDefaults() {
	if c.ca == "" {
		if v := os.Getenv("BGPLS_CA"); v != "" {
			c.ca = v
		}
	}
	if c.cert == "" {
		if v := os.Getenv("BGPLS_CERT"); v != "" {
			c.cert = v
		}
	}
	if c.key == "" {
		if v := os.Getenv("BGPLS_KEY"); v != "" {
			c.key = v
		}
	}
	if c.ca != "" || c.cert != "" || c.key != "" {
		return
	}
	role := os.Getenv("BGPLS_ROLE")
	if role == "" {
		role = "admin"
	}
	for _, base := range []string{"clab/pki", filepath.Join("clab", "pki")} {
		ca, cert, key := filepath.Join(base, "ca.crt"), filepath.Join(base, role+".crt"), filepath.Join(base, role+".key")
		if fileExists(ca) && fileExists(cert) && fileExists(key) {
			c.ca, c.cert, c.key = ca, cert, key
			return
		}
	}
}

func (c clientFlags) httpClient() (*http.Client, error) {
	c.applyLabDefaults()
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, err
	}
	if c.ca != "" {
		data, err := os.ReadFile(c.ca)
		if err != nil {
			return nil, err
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, errors.New("CA file has no certificates")
		}
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool}
	if c.cert != "" || c.key != "" {
		cert, err := tls.LoadX509KeyPair(c.cert, c.key)
		if err != nil {
			return nil, err
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}, Timeout: 30 * time.Second}, nil
}
func printProto(v proto.Message) error {
	data, err := protojson.MarshalOptions{Indent: "  ", UseProtoNames: true}.Marshal(v)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func topologyCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: bgpls topology <summary|nodes|links|prefixes>")
	}
	fs := flag.NewFlagSet("topology "+args[0], flag.ContinueOnError)
	cf := addClientFlags(fs)
	domain := fs.String("domain", "", "domain filter")
	revision := fs.Uint64("revision", 0, "topology revision")
	showUtil := fs.Bool("show-utilization", false, "include live utilization overlay on links")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	client, err := cf.httpClient()
	if err != nil {
		return err
	}
	svc := bgplsv1connect.NewTopologyServiceClient(client, cf.server)
	ctx := context.Background()
	filter := &bgplsv1.TopologyFilter{}
	if *domain != "" {
		filter.DomainIds = []string{*domain}
	}
	switch args[0] {
	case "summary":
		res, err := svc.GetSummary(ctx, connect.NewRequest(&bgplsv1.GetSummaryRequest{Filter: filter}))
		if err != nil {
			return err
		}
		return printProto(res.Msg)
	case "nodes":
		res, err := svc.ListNodes(ctx, connect.NewRequest(&bgplsv1.ListNodesRequest{Filter: filter, Revision: *revision, Page: &bgplsv1.Page{PageSize: 1000}}))
		if err != nil {
			return err
		}
		return printProto(res.Msg)
	case "links":
		res, err := svc.ListLinks(ctx, connect.NewRequest(&bgplsv1.ListLinksRequest{Filter: filter, Revision: *revision, Page: &bgplsv1.Page{PageSize: 1000}}))
		if err != nil {
			return err
		}
		if !*showUtil {
			for _, l := range res.Msg.Links {
				l.Utilization = nil
			}
		}
		return printProto(res.Msg)
	case "prefixes":
		res, err := svc.ListPrefixes(ctx, connect.NewRequest(&bgplsv1.ListPrefixesRequest{Filter: filter, Revision: *revision, Page: &bgplsv1.Page{PageSize: 1000}}))
		if err != nil {
			return err
		}
		return printProto(res.Msg)
	default:
		return errors.New("unknown topology command")
	}
}

func pathCommand(args []string) error {
	if len(args) == 0 || args[0] != "compute" {
		return errors.New("usage: bgpls path compute --domain ID --source NODE --destination NODE")
	}
	fs := flag.NewFlagSet("path compute", flag.ContinueOnError)
	cf := addClientFlags(fs)
	domain := fs.String("domain", "", "topology domain")
	source := fs.String("source", "", "source node ID, name, router ID, or IP")
	destination := fs.String("destination", "", "destination node ID, name, router ID, or IP")
	metric := fs.String("metric", "igp", "igp, te, delay, or available-bw")
	revision := fs.Uint64("revision", 0, "topology revision")
	minAvail := fs.String("min-available-bw", "", "minimum live available bandwidth (K/M/G suffixes)")
	excludeSRLG := fs.String("exclude-srlg", "", "comma-separated SRLGs to exclude")
	stalePolicy := fs.String("stale-policy", "unknown", "unknown (treat as unconstrained) or fail")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *domain == "" || *source == "" || *destination == "" {
		return errors.New("domain, source, and destination are required")
	}
	metricType := bgplsv1.PathMetric_PATH_METRIC_IGP
	switch *metric {
	case "te":
		metricType = bgplsv1.PathMetric_PATH_METRIC_TE
	case "delay":
		metricType = bgplsv1.PathMetric_PATH_METRIC_DELAY
	case "available-bw", "available_bw":
		metricType = bgplsv1.PathMetric_PATH_METRIC_AVAILABLE_BW
	}
	constraints := &bgplsv1.PathConstraints{}
	if *minAvail != "" {
		bps, err := utilization.ParseBitsPerSecond(*minAvail)
		if err != nil {
			return err
		}
		constraints.MinAvailableBps = bps
	}
	if *excludeSRLG != "" {
		for _, part := range strings.Split(*excludeSRLG, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			n, err := strconv.ParseUint(part, 10, 32)
			if err != nil {
				return fmt.Errorf("exclude-srlg: %w", err)
			}
			constraints.ExcludeSrlgs = append(constraints.ExcludeSrlgs, uint32(n))
		}
	}
	switch strings.ToLower(*stalePolicy) {
	case "fail":
		constraints.StalePolicy = bgplsv1.StaleUtilizationPolicy_STALE_UTILIZATION_POLICY_FAIL_LINK
	default:
		constraints.StalePolicy = bgplsv1.StaleUtilizationPolicy_STALE_UTILIZATION_POLICY_TREAT_AS_UNKNOWN
	}
	client, err := cf.httpClient()
	if err != nil {
		return err
	}
	svc := bgplsv1connect.NewPathServiceClient(client, cf.server)
	res, err := svc.ComputePaths(context.Background(), connect.NewRequest(&bgplsv1.ComputePathsRequest{DomainId: *domain, Source: *source, Destination: *destination, Metric: metricType, Constraints: constraints, Revision: *revision, MaxPaths: 1}))
	if err != nil {
		return err
	}
	return printProto(res.Msg)
}

func historyCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: bgpls history <changes|diff>")
	}
	fs := flag.NewFlagSet("history "+args[0], flag.ContinueOnError)
	cf := addClientFlags(fs)
	after := fs.Uint64("after", 0, "exclusive starting revision")
	before := fs.Uint64("before", 0, "inclusive ending revision")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	client, err := cf.httpClient()
	if err != nil {
		return err
	}
	svc := bgplsv1connect.NewHistoryServiceClient(client, cf.server)
	if args[0] == "changes" {
		res, err := svc.ListChanges(context.Background(), connect.NewRequest(&bgplsv1.ListChangesRequest{AfterRevision: *after, BeforeRevision: *before, Page: &bgplsv1.Page{PageSize: 1000}}))
		if err != nil {
			return err
		}
		return printProto(res.Msg)
	}
	if args[0] == "diff" {
		if *before == 0 {
			return errors.New("--before is required for diff")
		}
		res, err := svc.DiffTopology(context.Background(), connect.NewRequest(&bgplsv1.DiffTopologyRequest{FromRevision: *after, ToRevision: *before}))
		if err != nil {
			return err
		}
		return printProto(res.Msg)
	}
	return errors.New("unknown history command")
}

func peersCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: bgpls peers <list|get|enable|disable|reset|add|update|delete|export|import>")
	}
	fs := flag.NewFlagSet("peers "+args[0], flag.ContinueOnError)
	cf := addClientFlags(fs)
	id := fs.String("id", "", "peer ID")
	resourceVersion := fs.Uint64("resource-version", 0, "expected peer resource version")
	file := fs.String("file", "", "JSON input or YAML export/import file")
	replace := fs.Bool("replace", false, "replace peers during import")
	soft := fs.Bool("soft", false, "perform a soft reset")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	client, err := cf.httpClient()
	if err != nil {
		return err
	}
	svc := bgplsv1connect.NewCollectorServiceClient(client, cf.server)
	ctx := context.Background()
	switch args[0] {
	case "list":
		res, err := svc.ListPeers(ctx, connect.NewRequest(&bgplsv1.ListPeersRequest{Page: &bgplsv1.Page{PageSize: 1000}}))
		if err != nil {
			return err
		}
		return printProto(res.Msg)
	case "get":
		res, err := svc.GetPeer(ctx, connect.NewRequest(&bgplsv1.GetPeerRequest{Id: *id}))
		if err != nil {
			return err
		}
		return printProto(res.Msg)
	case "enable", "disable":
		res, err := svc.SetPeerAdminState(ctx, connect.NewRequest(&bgplsv1.SetPeerAdminStateRequest{Id: *id, Enabled: args[0] == "enable", ExpectedResourceVersion: *resourceVersion}))
		if err != nil {
			return err
		}
		return printProto(res.Msg)
	case "reset":
		res, err := svc.ResetPeer(ctx, connect.NewRequest(&bgplsv1.ResetPeerRequest{Id: *id, Soft: *soft}))
		if err != nil {
			return err
		}
		return printProto(res.Msg)
	case "delete":
		res, err := svc.DeletePeer(ctx, connect.NewRequest(&bgplsv1.DeletePeerRequest{Id: *id, ExpectedResourceVersion: *resourceVersion}))
		if err != nil {
			return err
		}
		return printProto(res.Msg)
	case "add", "update":
		if *file == "" {
			return errors.New("--file peer.json is required")
		}
		data, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		peer := &bgplsv1.Peer{}
		if err := protojson.Unmarshal(data, peer); err != nil {
			return err
		}
		if args[0] == "add" {
			res, err := svc.CreatePeer(ctx, connect.NewRequest(&bgplsv1.CreatePeerRequest{Peer: peer}))
			if err != nil {
				return err
			}
			return printProto(res.Msg)
		}
		res, err := svc.UpdatePeer(ctx, connect.NewRequest(&bgplsv1.UpdatePeerRequest{Peer: peer, ExpectedResourceVersion: *resourceVersion}))
		if err != nil {
			return err
		}
		return printProto(res.Msg)
	case "export":
		res, err := svc.ExportPeerConfig(ctx, connect.NewRequest(&bgplsv1.ExportPeerConfigRequest{}))
		if err != nil {
			return err
		}
		if *file != "" {
			return os.WriteFile(*file, res.Msg.Yaml, 0o600)
		}
		_, err = os.Stdout.Write(res.Msg.Yaml)
		return err
	case "import":
		if *file == "" {
			return errors.New("--file peers.yaml is required")
		}
		data, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		res, err := svc.ImportPeerConfig(ctx, connect.NewRequest(&bgplsv1.ImportPeerConfigRequest{Yaml: data, Replace: *replace}))
		if err != nil {
			return err
		}
		return printProto(res.Msg)
	default:
		return errors.New("unknown peers command")
	}
}

func healthCommand(args []string) error {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	cf := addClientFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := cf.httpClient()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, cf.server+"/healthz", nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned %s", res.Status)
	}
	fmt.Println("SERVING")
	return nil
}
