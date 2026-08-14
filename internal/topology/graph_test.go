package topology

import (
	"errors"
	"testing"
	"time"

	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func activeMeta(id string) *bgplsv1.EntityMeta {
	return &bgplsv1.EntityMeta{Id: id, DomainId: "d", Freshness: bgplsv1.Freshness_FRESHNESS_ACTIVE}
}
func TestConstrainedShortestPath(t *testing.T) {
	nodes := []*bgplsv1.Node{{Meta: activeMeta("a")}, {Meta: activeMeta("b")}, {Meta: activeMeta("c")}}
	links := []*bgplsv1.Link{{Meta: activeMeta("ab"), LocalNodeId: "a", RemoteNodeId: "b", IgpMetric: 1, ReservableBandwidthBytesPerSecond: 100}, {Meta: activeMeta("bc"), LocalNodeId: "b", RemoteNodeId: "c", IgpMetric: 1, ReservableBandwidthBytesPerSecond: 100}, {Meta: activeMeta("ac"), LocalNodeId: "a", RemoteNodeId: "c", IgpMetric: 10, ReservableBandwidthBytesPerSecond: 1000}}
	g := NewGraph(7, nodes, links)
	path, err := g.Compute("a", "c", bgplsv1.PathMetric_PATH_METRIC_IGP, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path.TotalMetric != 2 || len(path.Hops) != 3 {
		t.Fatalf("unexpected shortest path: %+v", path)
	}
	path, err = g.Compute("a", "c", bgplsv1.PathMetric_PATH_METRIC_IGP, &bgplsv1.PathConstraints{MinimumReservableBandwidthBytesPerSecond: 500})
	if err != nil {
		t.Fatal(err)
	}
	if path.TotalMetric != 10 || len(path.Hops) != 2 {
		t.Fatalf("unexpected constrained path: %+v", path)
	}
}

func TestImpactDetectsPartition(t *testing.T) {
	nodes := []*bgplsv1.Node{{Meta: activeMeta("a")}, {Meta: activeMeta("b")}, {Meta: activeMeta("c")}}
	links := []*bgplsv1.Link{{Meta: activeMeta("ab"), LocalNodeId: "a", RemoteNodeId: "b"}, {Meta: activeMeta("bc"), LocalNodeId: "b", RemoteNodeId: "c"}}
	result := NewGraph(1, nodes, links).Analyze([]string{"b"}, nil, []*bgplsv1.Prefix{{Meta: activeMeta("p"), OriginNodeId: "c"}})
	if len(result.DisconnectedComponents) != 2 || len(result.UnreachablePrefixIds) != 1 {
		t.Fatalf("unexpected impact: %+v", result)
	}
}

func TestMinAvailableBandwidthConstraint(t *testing.T) {
	now := time.Now().UTC()
	hot := &bgplsv1.LinkUtilization{SpeedBps: 1000, OutBps: 900, UtilizationKnown: true, AvailableBps: 100, StaleAt: timestamppb.New(now.Add(time.Minute))}
	cool := &bgplsv1.LinkUtilization{SpeedBps: 1000, OutBps: 100, UtilizationKnown: true, AvailableBps: 900, StaleAt: timestamppb.New(now.Add(time.Minute))}
	nodes := []*bgplsv1.Node{{Meta: activeMeta("a")}, {Meta: activeMeta("b")}, {Meta: activeMeta("c")}}
	links := []*bgplsv1.Link{
		{Meta: activeMeta("ab"), LocalNodeId: "a", RemoteNodeId: "b", IgpMetric: 1, Utilization: hot},
		{Meta: activeMeta("bc"), LocalNodeId: "b", RemoteNodeId: "c", IgpMetric: 1, Utilization: cool},
		{Meta: activeMeta("ac"), LocalNodeId: "a", RemoteNodeId: "c", IgpMetric: 10, Utilization: cool},
	}
	g := NewGraph(1, nodes, links)
	path, err := g.Compute("a", "c", bgplsv1.PathMetric_PATH_METRIC_IGP, nil)
	if err != nil || path.TotalMetric != 2 {
		t.Fatalf("unconstrained: %+v %v", path, err)
	}
	path, err = g.Compute("a", "c", bgplsv1.PathMetric_PATH_METRIC_IGP, &bgplsv1.PathConstraints{MinAvailableBps: 500})
	if err != nil || path.TotalMetric != 10 {
		t.Fatalf("constrained: %+v %v", path, err)
	}
}

func TestStaleFailLinkAndNoPath(t *testing.T) {
	nodes := []*bgplsv1.Node{{Meta: activeMeta("a")}, {Meta: activeMeta("c")}}
	links := []*bgplsv1.Link{{Meta: activeMeta("ac"), LocalNodeId: "a", RemoteNodeId: "c", IgpMetric: 1}}
	_, err := NewGraph(1, nodes, links).Compute("a", "c", bgplsv1.PathMetric_PATH_METRIC_IGP, &bgplsv1.PathConstraints{
		MinAvailableBps: 1,
		StalePolicy:     bgplsv1.StaleUtilizationPolicy_STALE_UTILIZATION_POLICY_FAIL_LINK,
	})
	if !errors.Is(err, ErrNoPath) {
		t.Fatalf("expected no path, got %v", err)
	}
}

func TestWidestAvailableBandwidth(t *testing.T) {
	now := time.Now().UTC()
	staleAt := timestamppb.New(now.Add(time.Minute))
	narrow := &bgplsv1.LinkUtilization{SpeedBps: 1000, OutBps: 800, UtilizationKnown: true, AvailableBps: 200, StaleAt: staleAt}
	wide := &bgplsv1.LinkUtilization{SpeedBps: 1000, OutBps: 100, UtilizationKnown: true, AvailableBps: 900, StaleAt: staleAt}
	nodes := []*bgplsv1.Node{{Meta: activeMeta("a")}, {Meta: activeMeta("b")}, {Meta: activeMeta("c")}}
	links := []*bgplsv1.Link{
		{Meta: activeMeta("ab"), LocalNodeId: "a", RemoteNodeId: "b", IgpMetric: 1, Utilization: narrow},
		{Meta: activeMeta("bc"), LocalNodeId: "b", RemoteNodeId: "c", IgpMetric: 1, Utilization: wide},
		{Meta: activeMeta("ac"), LocalNodeId: "a", RemoteNodeId: "c", IgpMetric: 50, Utilization: wide},
	}
	path, err := NewGraph(1, nodes, links).Compute("a", "c", bgplsv1.PathMetric_PATH_METRIC_AVAILABLE_BW, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path.TotalMetric != 50 || path.BottleneckAvailableBps != 900 {
		t.Fatalf("widest path = metric %d avail %d hops %d", path.TotalMetric, path.BottleneckAvailableBps, len(path.Hops))
	}
}

func TestEqualCostAlternatives(t *testing.T) {
	nodes := []*bgplsv1.Node{{Meta: activeMeta("a")}, {Meta: activeMeta("b")}, {Meta: activeMeta("c")}, {Meta: activeMeta("d")}}
	links := []*bgplsv1.Link{{Meta: activeMeta("ab"), LocalNodeId: "a", RemoteNodeId: "b", IgpMetric: 1}, {Meta: activeMeta("bd"), LocalNodeId: "b", RemoteNodeId: "d", IgpMetric: 1}, {Meta: activeMeta("ac"), LocalNodeId: "a", RemoteNodeId: "c", IgpMetric: 1}, {Meta: activeMeta("cd"), LocalNodeId: "c", RemoteNodeId: "d", IgpMetric: 1}}
	paths, err := NewGraph(1, nodes, links).ComputeMany("a", "d", bgplsv1.PathMetric_PATH_METRIC_IGP, nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("equal-cost path count = %d", len(paths))
	}
}
