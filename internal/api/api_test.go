package api

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestSANRoleMapping(t *testing.T) {
	a := NewAuthorizer([]config.RoleMapping{{Role: "operator", URISANs: []string{"spiffe://example.net/operators/*"}}, {Role: "reader", DNSSANs: []string{"*.readers.example.net"}}}, false)
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
