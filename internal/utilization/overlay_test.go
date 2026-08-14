package utilization

import (
	"context"
	"testing"
	"time"

	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"github.com/bgpls/bgpls/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestReportReadBackAndNoRevision(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	link := &bgplsv1.Link{
		Meta:              &bgplsv1.EntityMeta{Id: "ab", DomainId: "d"},
		LocalNodeId:       "a",
		RemoteNodeId:      "b",
		LocalIpv4Address:  "10.0.0.1",
		RemoteIpv4Address: "10.0.0.2",
	}
	if _, err := s.Apply(ctx, store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_LINK, ID: "ab", DomainID: "d", Value: link}); err != nil {
		t.Fatal(err)
	}
	before := s.Revision()
	o, err := Open(t.TempDir(), Options{StaleAfter: time.Second, SweepAfter: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	o.RebuildIndex(s.Snapshot().Links)
	now := time.Unix(1000, 0).UTC()
	stats := o.Report(now, "operator", []*bgplsv1.InterfaceUtilization{{
		Device:        "r1",
		InterfaceName: "eth1",
		Ipv4Addresses: []string{"10.0.0.1"},
		SpeedBps:      10_000_000,
		InBps:         1_000_000,
		OutBps:        2_000_000,
		ObservedAt:    timestamppb.New(now),
	}})
	if stats.Accepted != 1 || stats.Correlated != 1 || stats.Uncorrelated != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if s.Revision() != before {
		t.Fatalf("utilization write created topology revision %d -> %d", before, s.Revision())
	}
	got := o.Get("ab")
	if got == nil || !got.UtilizationKnown || got.OutBps != 2_000_000 || got.AvailableBps != 8_000_000 {
		t.Fatalf("read-back = %+v", got)
	}
	if got.StaleAt.AsTime() != now.Add(time.Second) {
		t.Fatalf("stale_at = %v", got.StaleAt.AsTime())
	}
}

func TestStalenessSweeperDeletesOldRecords(t *testing.T) {
	o, err := Open(t.TempDir(), Options{StaleAfter: time.Second, SweepAfter: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	o.RebuildIndex([]*bgplsv1.Link{{
		Meta:             &bgplsv1.EntityMeta{Id: "ab"},
		LocalNodeId:      "a",
		RemoteNodeId:     "b",
		LocalIpv4Address: "10.0.0.1",
	}})
	now := time.Unix(1000, 0).UTC()
	o.Report(now, "operator", []*bgplsv1.InterfaceUtilization{{
		Device: "r1", InterfaceName: "eth1", Ipv4Addresses: []string{"10.0.0.1"},
		SpeedBps: 1000, OutBps: 100, ObservedAt: timestamppb.New(now),
	}})
	if o.Get("ab") == nil {
		t.Fatal("expected stored sample")
	}
	if err := o.sweep(now.Add(6 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if o.Get("ab") != nil {
		t.Fatal("sweeper should delete samples older than sweep window")
	}
}

func TestCorrelationAndReverseLink(t *testing.T) {
	o, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	o.RebuildIndex([]*bgplsv1.Link{
		{Meta: &bgplsv1.EntityMeta{Id: "ab"}, LocalNodeId: "a", RemoteNodeId: "b", LocalIpv4Address: "10.1.0.1", RemoteIpv4Address: "10.1.0.2"},
		{Meta: &bgplsv1.EntityMeta{Id: "ba"}, LocalNodeId: "b", RemoteNodeId: "a", LocalIpv4Address: "10.1.0.2", RemoteIpv4Address: "10.1.0.1"},
	})
	now := time.Unix(50, 0).UTC()
	stats := o.Report(now, "operator", []*bgplsv1.InterfaceUtilization{{
		Device: "srl1", InterfaceName: "ethernet-1/1", Ipv4Addresses: []string{"10.1.0.1/30"},
		SpeedBps: 1_000_000_000, InBps: 10, OutBps: 20, ObservedAt: timestamppb.New(now),
	}})
	if stats.Correlated != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	fwd := o.Get("ab")
	rev := o.Get("ba")
	if fwd == nil || fwd.OutBps != 20 || fwd.InBps != 10 {
		t.Fatalf("forward = %+v", fwd)
	}
	if rev == nil || rev.OutBps != 10 || rev.InBps != 20 {
		t.Fatalf("reverse = %+v", rev)
	}
}

func TestAmbiguityGuard(t *testing.T) {
	o, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	o.RebuildIndex([]*bgplsv1.Link{
		{Meta: &bgplsv1.EntityMeta{Id: "a1"}, LocalNodeId: "a", RemoteNodeId: "x", LocalIpv4Address: "10.9.9.9"},
		{Meta: &bgplsv1.EntityMeta{Id: "b1"}, LocalNodeId: "b", RemoteNodeId: "y", LocalIpv4Address: "10.9.9.9"},
	})
	stats := o.Report(time.Now().UTC(), "operator", []*bgplsv1.InterfaceUtilization{{
		Device: "dup", InterfaceName: "eth0", Ipv4Addresses: []string{"10.9.9.9"}, SpeedBps: 1000, OutBps: 1,
	}})
	if stats.Uncorrelated != 1 || stats.Ambiguous != 1 || o.Get("a1") != nil || o.Get("b1") != nil {
		t.Fatalf("ambiguity should attach to none: %+v", stats)
	}
	if o.AmbiguityHits() != 1 {
		t.Fatalf("ambiguity hits = %d", o.AmbiguityHits())
	}
	unc := o.Uncorrelated()
	if len(unc) != 1 || unc[0].Reason != ReasonAmbiguous {
		t.Fatalf("uncorrelated = %+v", unc)
	}
}

func TestUnnumberedAndUnknownSpeed(t *testing.T) {
	o, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	o.RebuildIndex([]*bgplsv1.Link{{
		Meta: &bgplsv1.EntityMeta{Id: "ab"}, LocalNodeId: "a", RemoteNodeId: "b", LocalIpv4Address: "10.0.0.1",
	}})
	stats := o.Report(time.Now().UTC(), "operator", []*bgplsv1.InterfaceUtilization{
		{Device: "r1", InterfaceName: "eth9"},
		{Device: "r1", InterfaceName: "eth1", Ipv4Addresses: []string{"10.0.0.1"}, InBps: 100, OutBps: 200},
	})
	if stats.Uncorrelated != 1 || stats.Correlated != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	got := o.Get("ab")
	if got == nil || got.UtilizationKnown {
		t.Fatalf("unknown speed must stay unknown: %+v", got)
	}
}

func TestMalformedEntryDoesNotRejectBatch(t *testing.T) {
	o, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	o.RebuildIndex([]*bgplsv1.Link{{
		Meta: &bgplsv1.EntityMeta{Id: "ab"}, LocalNodeId: "a", RemoteNodeId: "b", LocalIpv4Address: "10.0.0.1",
	}})
	stats := o.Report(time.Now().UTC(), "operator", []*bgplsv1.InterfaceUtilization{
		nil,
		{Device: "", InterfaceName: "eth1"},
		{Device: "r1", InterfaceName: "eth1", Ipv4Addresses: []string{"10.0.0.1"}, SpeedBps: 1000},
	})
	if stats.Rejected != 2 || stats.Accepted != 1 || stats.Correlated != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestParseBitsPerSecond(t *testing.T) {
	cases := map[string]uint64{"500M": 500_000_000, "1G": 1_000_000_000, "1.5G": 1_500_000_000, "1000": 1000, "2K": 2000}
	for in, want := range cases {
		got, err := ParseBitsPerSecond(in)
		if err != nil || got != want {
			t.Fatalf("%q = %d, %v want %d", in, got, err, want)
		}
	}
}
