package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"gopkg.in/yaml.v3"
)

type Config struct {
	DataDir     string      `yaml:"data_dir"`
	API         API         `yaml:"api"`
	BGP         BGP         `yaml:"bgp"`
	Retention   Retention   `yaml:"retention"`
	Utilization Utilization `yaml:"utilization"`
	Domains     []Domain    `yaml:"domains"`
	Peers       []Peer      `yaml:"peers"`
}
type API struct {
	Listen        string        `yaml:"listen"`
	UIListen      string        `yaml:"ui_listen"`
	TLS           TLS           `yaml:"tls"`
	RBAC          []RoleMapping `yaml:"rbac"`
	MetricsListen string        `yaml:"metrics_listen"`
}
type TLS struct {
	Certificate          string   `yaml:"certificate"`
	PrivateKey           string   `yaml:"private_key"`
	ClientCAs            []string `yaml:"client_cas"`
	DevelopmentInsecure  bool     `yaml:"development_insecure"`
	AllowAnonymousReader bool     `yaml:"allow_anonymous_reader"`
}
type RoleMapping struct {
	Role    string   `yaml:"role"`
	URISANs []string `yaml:"uri_sans"`
	DNSSANs []string `yaml:"dns_sans"`
}
type BGP struct {
	RouterID        string   `yaml:"router_id"`
	ListenPort      int32    `yaml:"listen_port"`
	ListenAddresses []string `yaml:"listen_addresses"`
}
type Retention struct {
	Duration     time.Duration `yaml:"-"`
	DurationText string        `yaml:"duration"`
	MaxBytes     uint64        `yaml:"max_bytes"`
}
type Utilization struct {
	StaleAfter     time.Duration `yaml:"-"`
	StaleAfterText string        `yaml:"stale_after"`
	SweepAfter     time.Duration `yaml:"-"`
	SweepAfterText string        `yaml:"sweep_after"`
}
type Domain struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}
type Peer struct {
	ID               string `yaml:"id"`
	DomainID         string `yaml:"domain_id"`
	Name             string `yaml:"name"`
	RemoteAddress    string `yaml:"remote_address"`
	LocalAddress     string `yaml:"local_address"`
	LocalAS          uint32 `yaml:"local_as"`
	RemoteAS         uint32 `yaml:"remote_as"`
	SourcePreference uint32 `yaml:"source_preference"`
	Enabled          *bool  `yaml:"enabled"`
	Passive          bool   `yaml:"passive"`
	EBGPMultihop     bool   `yaml:"ebgp_multihop"`
	MultihopTTL      uint32 `yaml:"multihop_ttl"`
	GTSM             bool   `yaml:"gtsm"`
	TCPMD5Secret     string `yaml:"tcp_md5_secret"`
}

func Default() Config {
	return Config{DataDir: "./data", API: API{Listen: "127.0.0.1:7443", MetricsListen: "127.0.0.1:9090"}, BGP: BGP{RouterID: "127.0.0.1", ListenPort: -1}, Retention: Retention{Duration: 30 * 24 * time.Hour, DurationText: "720h", MaxBytes: 50 << 30}, Utilization: Utilization{StaleAfter: 45 * time.Second, StaleAfterText: "45s", SweepAfter: 10 * time.Minute, SweepAfterText: "10m"}}
}

func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse configuration: %w", err)
	}
	if cfg.Retention.DurationText != "" {
		cfg.Retention.Duration, err = time.ParseDuration(cfg.Retention.DurationText)
		if err != nil {
			return Config{}, fmt.Errorf("retention.duration: %w", err)
		}
	}
	if cfg.Utilization.StaleAfterText != "" {
		cfg.Utilization.StaleAfter, err = time.ParseDuration(cfg.Utilization.StaleAfterText)
		if err != nil {
			return Config{}, fmt.Errorf("utilization.stale_after: %w", err)
		}
	}
	if cfg.Utilization.SweepAfterText != "" {
		cfg.Utilization.SweepAfter, err = time.ParseDuration(cfg.Utilization.SweepAfterText)
		if err != nil {
			return Config{}, fmt.Errorf("utilization.sweep_after: %w", err)
		}
	}
	if !filepath.IsAbs(cfg.DataDir) {
		base, _ := filepath.Abs(filepath.Dir(path))
		cfg.DataDir = filepath.Join(base, cfg.DataDir)
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	var errs []error
	if c.DataDir == "" {
		errs = append(errs, errors.New("data_dir is required"))
	}
	if _, _, err := net.SplitHostPort(c.API.Listen); err != nil {
		errs = append(errs, fmt.Errorf("api.listen: %w", err))
	}
	if c.API.UIListen != "" {
		if _, _, err := net.SplitHostPort(c.API.UIListen); err != nil {
			errs = append(errs, fmt.Errorf("api.ui_listen: %w", err))
		}
	}
	if c.API.TLS.DevelopmentInsecure {
		host, _, _ := net.SplitHostPort(c.API.Listen)
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			errs = append(errs, errors.New("development_insecure API must bind to loopback"))
		}
	} else {
		if c.API.TLS.Certificate == "" || c.API.TLS.PrivateKey == "" || len(c.API.TLS.ClientCAs) == 0 {
			errs = append(errs, errors.New("API certificate, private key, and at least one client CA are required"))
		}
	}
	if c.Retention.Duration <= 0 {
		errs = append(errs, errors.New("retention duration must be positive"))
	}
	if c.Retention.MaxBytes == 0 {
		errs = append(errs, errors.New("retention max_bytes must be positive"))
	}
	domains := map[string]bool{}
	for _, d := range c.Domains {
		if d.ID == "" {
			errs = append(errs, errors.New("domain id is required"))
		}
		if domains[d.ID] {
			errs = append(errs, fmt.Errorf("duplicate domain %q", d.ID))
		}
		domains[d.ID] = true
	}
	peers := map[string]bool{}
	for _, p := range c.Peers {
		if p.ID == "" {
			errs = append(errs, errors.New("peer id is required"))
		}
		if peers[p.ID] {
			errs = append(errs, fmt.Errorf("duplicate peer %q", p.ID))
		}
		peers[p.ID] = true
		if !domains[p.DomainID] {
			errs = append(errs, fmt.Errorf("peer %q references unknown domain %q", p.ID, p.DomainID))
		}
		if net.ParseIP(p.RemoteAddress) == nil {
			errs = append(errs, fmt.Errorf("peer %q has invalid remote address", p.ID))
		}
		if p.LocalAS == 0 || p.RemoteAS == 0 {
			errs = append(errs, fmt.Errorf("peer %q AS numbers must be non-zero", p.ID))
		}
	}
	return errors.Join(errs...)
}

func (d Domain) Proto() *bgplsv1.Domain {
	return &bgplsv1.Domain{Id: d.ID, Name: d.Name, Description: d.Description}
}
func (p Peer) Proto() *bgplsv1.Peer {
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	return &bgplsv1.Peer{Id: p.ID, DomainId: p.DomainID, Name: p.Name, RemoteAddress: p.RemoteAddress, LocalAddress: p.LocalAddress, LocalAs: p.LocalAS, RemoteAs: p.RemoteAS, SourcePreference: p.SourcePreference, Enabled: enabled, Passive: p.Passive, EbgpMultihop: p.EBGPMultihop, MultihopTtl: p.MultihopTTL, Gtsm: p.GTSM, TcpMd5Secret: p.TCPMD5Secret, SessionState: bgplsv1.PeerSessionState_PEER_SESSION_STATE_IDLE}
}
