package api

import (
	"bytes"
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"github.com/bgpls/bgpls/gen/bgpls/v1/bgplsv1connect"
	"github.com/bgpls/bgpls/internal/config"
	"github.com/bgpls/bgpls/internal/store"
)

func TestConnectTopologySummary(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Apply(context.Background(), store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_NODE, ID: "n1", DomainID: "d1", Value: &bgplsv1.Node{Meta: &bgplsv1.EntityMeta{Id: "n1", DomainId: "d1"}}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(s, NoopPeerManager{}, "test", time.Now(), nil)
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Result(), nil
	})}
	client := bgplsv1connect.NewTopologyServiceClient(httpClient, "http://bgpls.test")
	response, err := client.GetSummary(context.Background(), connect.NewRequest(&bgplsv1.GetSummaryRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Msg.NodeCount != 1 || response.Msg.Revision != 1 {
		t.Fatalf("unexpected summary: %+v", response.Msg)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestUIGraphJSON(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	_, err = s.Apply(ctx, store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_NODE, ID: "n1", DomainID: "d1", Value: &bgplsv1.Node{Meta: &bgplsv1.EntityMeta{Id: "n1", DomainId: "d1", Freshness: bgplsv1.Freshness_FRESHNESS_ACTIVE}, Name: "r1"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Apply(ctx, store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_NODE, ID: "n2", DomainID: "d1", Value: &bgplsv1.Node{Meta: &bgplsv1.EntityMeta{Id: "n2", DomainId: "d1", Freshness: bgplsv1.Freshness_FRESHNESS_ACTIVE}, Name: "r2"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Apply(ctx, store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_LINK, ID: "l1", DomainID: "d1", Value: &bgplsv1.Link{Meta: &bgplsv1.EntityMeta{Id: "l1", DomainId: "d1", Freshness: bgplsv1.Freshness_FRESHNESS_ACTIVE}, LocalNodeId: "n1", RemoteNodeId: "n2", IgpMetric: 10}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(s, NoopPeerManager{}, "test", time.Now(), nil)
	req := httptest.NewRequest(http.MethodPost, "/bgpls.v1.TopologyService/ListNodes", bytes.NewBufferString(`{"page":{"pageSize":1000}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ListNodes status = %d body = %s", rec.Code, rec.Body.String())
	}
	var nodes struct {
		Nodes []struct {
			Name string `json:"name"`
			Meta struct {
				ID string `json:"id"`
			} `json:"meta"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &nodes); err != nil {
		t.Fatal(err)
	}
	if len(nodes.Nodes) != 2 || nodes.Nodes[0].Meta.ID == "" {
		t.Fatalf("unexpected nodes: %+v", nodes.Nodes)
	}
	req = httptest.NewRequest(http.MethodPost, "/bgpls.v1.TopologyService/ListLinks", bytes.NewBufferString(`{"page":{"pageSize":1000}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ListLinks status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"localNodeId":"n1"`) || !strings.Contains(rec.Body.String(), `"remoteNodeId":"n2"`) {
		t.Fatalf("unexpected links: %s", rec.Body.String())
	}
}

func TestUIServedFromHandler(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	handler := NewHandler(s, NoopPeerManager{}, "test", time.Now(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/" {
		t.Fatalf("root redirect = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "cytoscape") {
		t.Fatalf("ui status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestUIRequiresReader(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	handler := NewHandler(s, NoopPeerManager{}, "test", time.Now(), NewAuthorizer(nil, false, false))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated ui status = %d", rec.Code)
	}
	open := NewHandler(s, NoopPeerManager{}, "test", time.Now(), NewAuthorizer(nil, true, false))
	rec = httptest.NewRecorder()
	open.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("insecure ui status = %d", rec.Code)
	}
}

func TestAnonymousReaderCanOpenUI(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	handler := NewHandler(s, NoopPeerManager{}, "test", time.Now(), NewAuthorizer(nil, false, true))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous ui status = %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/bgpls.v1.TopologyService/GetSummary", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous summary status = %d body = %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/bgpls.v1.CollectorService/CreatePeer", bytes.NewBufferString(`{"peer":{"id":"x"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous create peer status = %d", rec.Code)
	}
}

func TestSANRoleMapping(t *testing.T) {
	a := NewAuthorizer([]config.RoleMapping{{Role: "operator", URISANs: []string{"spiffe://example.net/operators/*"}}, {Role: "reader", DNSSANs: []string{"*.readers.example.net"}}}, false, false)
	identity, _ := url.Parse("spiffe://example.net/operators/router-team")
	if got := a.role(&x509.Certificate{URIs: []*url.URL{identity}}); got != RoleOperator {
		t.Fatalf("URI SAN role = %v", got)
	}
	if got := a.role(&x509.Certificate{DNSNames: []string{"west.readers.example.net"}}); got != RoleReader {
		t.Fatalf("DNS SAN role = %v", got)
	}
	if got := a.role(&x509.Certificate{Subject: pkix.Name{CommonName: "admin"}}); got != RoleNone {
		t.Fatalf("CN unexpectedly authorized as %v", got)
	}
}
