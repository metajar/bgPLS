package topology

import (
	"testing"

	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
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
