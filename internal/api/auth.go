package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
	"sync/atomic"

	"github.com/bgpls/bgpls/internal/config"
)

var errOverlayMissing = errors.New("utilization overlay is not configured")

type ctxKey int

const roleContextKey ctxKey = 1

func RoleFrom(ctx context.Context) Role {
	v, _ := ctx.Value(roleContextKey).(Role)
	return v
}

func roleLabel(role Role) string {
	switch role {
	case RoleAdmin:
		return "admin"
	case RoleOperator:
		return "operator"
	case RoleReader:
		return "reader"
	default:
		return "unknown"
	}
}

type Role int

const (
	RoleNone Role = iota
	RoleReader
	RoleOperator
	RoleAdmin
)

type Authorizer struct {
	mappings        atomic.Value
	insecure        bool
	anonymousReader atomic.Bool
}

func NewAuthorizer(mappings []config.RoleMapping, insecure, anonymousReader bool) *Authorizer {
	a := &Authorizer{insecure: insecure}
	a.mappings.Store(mappings)
	a.anonymousReader.Store(anonymousReader)
	return a
}
func (a *Authorizer) Reload(mappings []config.RoleMapping, anonymousReader bool) {
	a.mappings.Store(mappings)
	a.anonymousReader.Store(anonymousReader)
}
func roleValue(role string) Role {
	switch strings.ToLower(role) {
	case "reader":
		return RoleReader
	case "operator":
		return RoleOperator
	case "admin":
		return RoleAdmin
	default:
		return RoleNone
	}
}
func match(pattern, value string) bool {
	ok, err := path.Match(pattern, value)
	return err == nil && ok
}
func (a *Authorizer) role(cert *x509.Certificate) Role {
	best := RoleNone
	for _, m := range a.mappings.Load().([]config.RoleMapping) {
		role := roleValue(m.Role)
		for _, uri := range cert.URIs {
			for _, pattern := range m.URISANs {
				if match(pattern, uri.String()) && role > best {
					best = role
				}
			}
		}
		for _, dns := range cert.DNSNames {
			for _, pattern := range m.DNSSANs {
				if match(pattern, dns) && role > best {
					best = role
				}
			}
		}
	}
	return best
}
func requiredRole(procedure string) Role {
	if strings.Contains(procedure, "CreatePeer") || strings.Contains(procedure, "UpdatePeer") || strings.Contains(procedure, "DeletePeer") || strings.Contains(procedure, "ImportPeerConfig") || strings.Contains(procedure, "ExportPeerConfig") {
		return RoleAdmin
	}
	if strings.Contains(procedure, "SetPeerAdminState") || strings.Contains(procedure, "ResetPeer") || strings.Contains(procedure, "ReportInterfaceUtilization") {
		return RoleOperator
	}
	return RoleReader
}
func (a *Authorizer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := RoleNone
		if a.insecure && r.TLS == nil {
			role = RoleAdmin
		} else if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 {
			role = a.role(r.TLS.VerifiedChains[0][0])
		} else if a.anonymousReader.Load() {
			role = RoleReader
		}
		if role < requiredRole(r.URL.Path) {
			http.Error(w, "client certificate is not authorized for this procedure", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), roleContextKey, role)))
	})
}

func TLSConfig(cfg config.TLS) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.Certificate, cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("load API certificate: %w", err)
	}
	pool := x509.NewCertPool()
	for _, file := range cfg.ClientCAs {
		data, err := osReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read client CA %q: %w", file, err)
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("client CA %q contains no certificates", file)
		}
	}
	clientAuth := tls.RequireAndVerifyClientCert
	if cfg.AllowAnonymousReader {
		clientAuth = tls.VerifyClientCertIfGiven
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientCAs: pool, ClientAuth: clientAuth}, nil
}

var osReadFile = func(name string) ([]byte, error) { return os.ReadFile(name) }
