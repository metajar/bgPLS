package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"github.com/bgpls/bgpls/internal/store"
	"github.com/bgpls/bgpls/internal/utilization"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestEnrichmentReportAndReadBack(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.Apply(ctx, store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_LINK, ID: "ab", DomainID: "d1", Value: &bgplsv1.Link{Meta: &bgplsv1.EntityMeta{Id: "ab", DomainId: "d1", Freshness: bgplsv1.Freshness_FRESHNESS_ACTIVE}, LocalNodeId: "n1", RemoteNodeId: "n2", LocalIpv4Address: "10.1.1.1"}}); err != nil {
		t.Fatal(err)
	}
	before := s.Revision()
	overlay, err := utilization.Open(t.TempDir(), utilization.Options{StaleAfter: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer overlay.Close()
	overlay.RebuildIndex(s.Snapshot().Links)
	handler := NewHandlerWithOverlay(s, NoopPeerManager{}, "test", time.Now(), nil, overlay)
	body, _ := json.Marshal(map[string]any{
		"interfaces": []map[string]any{{
			"device": "r1", "interfaceName": "eth1", "ipv4Addresses": []string{"10.1.1.1"},
			"speedBps": "10000000", "inBps": "1000000", "outBps": "2000000",
			"observedAt": timestamppb.Now().AsTime().Format(time.RFC3339Nano),
		}},
	})
	req := httptest.NewRequest(http.MethodPost, "/bgpls.v1.EnrichmentService/ReportInterfaceUtilization", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("report status = %d body = %s", rec.Code, rec.Body.String())
	}
	if s.Revision() != before {
		t.Fatalf("report created a topology revision")
	}
	req = httptest.NewRequest(http.MethodPost, "/bgpls.v1.EnrichmentService/GetLinkUtilization", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"ab"`)) {
		t.Fatalf("get utilization = %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/bgpls.v1.TopologyService/ListLinks", bytes.NewBufferString(`{"page":{"pageSize":1000}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"utilization"`)) {
		t.Fatalf("list links missing utilization: %s", rec.Body.String())
	}
}
