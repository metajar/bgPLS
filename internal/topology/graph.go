package topology

import (
	"container/heap"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"google.golang.org/protobuf/proto"
)

var ErrNoPath = errors.New("no path satisfies the requested constraints")

type Graph struct {
	revision uint64
	nodes    map[string]*bgplsv1.Node
	links    map[string]*bgplsv1.Link
	adj      map[string][]*bgplsv1.Link
}

func NewGraph(revision uint64, nodes []*bgplsv1.Node, links []*bgplsv1.Link) *Graph {
	g := &Graph{revision: revision, nodes: map[string]*bgplsv1.Node{}, links: map[string]*bgplsv1.Link{}, adj: map[string][]*bgplsv1.Link{}}
	for _, n := range nodes {
		if n.GetMeta().GetFreshness() == bgplsv1.Freshness_FRESHNESS_ACTIVE {
			g.nodes[n.GetMeta().GetId()] = n
		}
	}
	for _, l := range links {
		if l.GetMeta().GetFreshness() != bgplsv1.Freshness_FRESHNESS_ACTIVE {
			continue
		}
		if _, ok := g.nodes[l.LocalNodeId]; !ok {
			continue
		}
		if _, ok := g.nodes[l.RemoteNodeId]; !ok {
			continue
		}
		g.links[l.GetMeta().GetId()] = l
		g.adj[l.LocalNodeId] = append(g.adj[l.LocalNodeId], l)
	}
	for id := range g.adj {
		sort.Slice(g.adj[id], func(i, j int) bool { return g.adj[id][i].GetMeta().GetId() < g.adj[id][j].GetMeta().GetId() })
	}
	return g
}

type queueItem struct {
	id    string
	cost  uint64
	index int
}
type priorityQueue []*queueItem

func (p priorityQueue) Len() int { return len(p) }
func (p priorityQueue) Less(i, j int) bool {
	if p[i].cost == p[j].cost {
		return p[i].id < p[j].id
	}
	return p[i].cost < p[j].cost
}
func (p priorityQueue) Swap(i, j int) { p[i], p[j] = p[j], p[i]; p[i].index = i; p[j].index = j }
func (p *priorityQueue) Push(x any) {
	item := x.(*queueItem)
	item.index = len(*p)
	*p = append(*p, item)
}
func (p *priorityQueue) Pop() any {
	old := *p
	n := len(old)
	item := old[n-1]
	*p = old[:n-1]
	return item
}

func metric(link *bgplsv1.Link, metric bgplsv1.PathMetric) (uint64, bool) {
	switch metric {
	case bgplsv1.PathMetric_PATH_METRIC_TE:
		if link.TeMetric == 0 {
			return 0, false
		}
		return link.TeMetric, true
	case bgplsv1.PathMetric_PATH_METRIC_DELAY:
		if link.DelayMicroseconds == 0 {
			return 0, false
		}
		return link.DelayMicroseconds, true
	default:
		if link.IgpMetric == 0 {
			return 1, true
		}
		return link.IgpMetric, true
	}
}

func eligible(l *bgplsv1.Link, c *bgplsv1.PathConstraints) bool {
	if c == nil {
		return true
	}
	if c.MultiTopologyId != 0 && l.MultiTopologyId != c.MultiTopologyId {
		return false
	}
	if c.MinimumReservableBandwidthBytesPerSecond > 0 && l.ReservableBandwidthBytesPerSecond < c.MinimumReservableBandwidthBytesPerSecond {
		return false
	}
	for _, id := range c.AvoidLinkIds {
		if id == l.GetMeta().GetId() {
			return false
		}
	}
	for _, id := range c.AvoidNodeIds {
		if id == l.LocalNodeId || id == l.RemoteNodeId {
			return false
		}
	}
	groups := map[uint32]bool{}
	for _, v := range l.AdminGroups {
		groups[v] = true
	}
	for _, v := range c.RequireAdminGroups {
		if !groups[v] {
			return false
		}
	}
	for _, v := range c.ExcludeAdminGroups {
		if groups[v] {
			return false
		}
	}
	return true
}

func (g *Graph) shortest(source, destination string, metricType bgplsv1.PathMetric, c *bgplsv1.PathConstraints) ([]*bgplsv1.Link, uint64, error) {
	if g.nodes[source] == nil || g.nodes[destination] == nil {
		return nil, 0, fmt.Errorf("%w: source or destination does not exist in the active graph", ErrNoPath)
	}
	dist := map[string]uint64{source: 0}
	prev := map[string]*bgplsv1.Link{}
	q := priorityQueue{&queueItem{id: source, cost: 0}}
	heap.Init(&q)
	for q.Len() > 0 {
		item := heap.Pop(&q).(*queueItem)
		if known := dist[item.id]; item.cost != known {
			continue
		}
		if item.id == destination {
			break
		}
		for _, link := range g.adj[item.id] {
			if !eligible(link, c) {
				continue
			}
			weight, ok := metric(link, metricType)
			if !ok {
				continue
			}
			if item.cost > math.MaxUint64-weight {
				continue
			}
			next := item.cost + weight
			old, seen := dist[link.RemoteNodeId]
			if !seen || next < old || (next == old && link.GetMeta().GetId() < prev[link.RemoteNodeId].GetMeta().GetId()) {
				dist[link.RemoteNodeId] = next
				prev[link.RemoteNodeId] = link
				heap.Push(&q, &queueItem{id: link.RemoteNodeId, cost: next})
			}
		}
	}
	total, ok := dist[destination]
	if !ok {
		return nil, 0, ErrNoPath
	}
	var reversed []*bgplsv1.Link
	for current := destination; current != source; {
		link := prev[current]
		if link == nil {
			return nil, 0, ErrNoPath
		}
		reversed = append(reversed, link)
		current = link.LocalNodeId
	}
	path := make([]*bgplsv1.Link, len(reversed))
	for i := range reversed {
		path[len(reversed)-1-i] = reversed[i]
	}
	return path, total, nil
}

func (g *Graph) Compute(source, destination string, metricType bgplsv1.PathMetric, c *bgplsv1.PathConstraints) (*bgplsv1.ComputedPath, error) {
	waypoints := []string{source}
	if c != nil {
		waypoints = append(waypoints, c.IncludeNodeIds...)
	}
	waypoints = append(waypoints, destination)
	var links []*bgplsv1.Link
	var total uint64
	for i := 0; i < len(waypoints)-1; i++ {
		segment, cost, err := g.shortest(waypoints[i], waypoints[i+1], metricType, c)
		if err != nil {
			return nil, err
		}
		links = append(links, segment...)
		total += cost
	}
	result := &bgplsv1.ComputedPath{TotalMetric: total, BottleneckBandwidthBytesPerSecond: math.MaxFloat64}
	current := source
	for i, link := range links {
		result.Hops = append(result.Hops, &bgplsv1.PathHop{Index: uint32(i), Node: g.nodes[current], OutgoingLink: link})
		result.TotalDelayMicroseconds += link.DelayMicroseconds
		if link.ReservableBandwidthBytesPerSecond < result.BottleneckBandwidthBytesPerSecond {
			result.BottleneckBandwidthBytesPerSecond = link.ReservableBandwidthBytesPerSecond
		}
		current = link.RemoteNodeId
	}
	result.Hops = append(result.Hops, &bgplsv1.PathHop{Index: uint32(len(links)), Node: g.nodes[current]})
	if len(links) == 0 {
		result.BottleneckBandwidthBytesPerSecond = 0
	} else if result.BottleneckBandwidthBytesPerSecond == math.MaxFloat64 {
		result.BottleneckBandwidthBytesPerSecond = 0
	}
	return result, nil
}

// ComputeMany returns the selected shortest path and deterministic equal-cost
// alternatives discovered by excluding each selected edge in turn.
func (g *Graph) ComputeMany(source, destination string, metricType bgplsv1.PathMetric, c *bgplsv1.PathConstraints, limit int) ([]*bgplsv1.ComputedPath, error) {
	if limit <= 0 {
		limit = 1
	}
	if limit > 16 {
		limit = 16
	}
	first, err := g.Compute(source, destination, metricType, c)
	if err != nil {
		return nil, err
	}
	out := []*bgplsv1.ComputedPath{first}
	seen := map[string]bool{pathKey(first): true}
	for _, hop := range first.Hops {
		if len(out) >= limit || hop.OutgoingLink == nil {
			break
		}
		next := &bgplsv1.PathConstraints{}
		if c != nil {
			next = proto.Clone(c).(*bgplsv1.PathConstraints)
		}
		next.AvoidLinkIds = append(next.AvoidLinkIds, hop.OutgoingLink.GetMeta().GetId())
		candidate, err := g.Compute(source, destination, metricType, next)
		if err != nil || candidate.TotalMetric != first.TotalMetric {
			continue
		}
		key := pathKey(candidate)
		if !seen[key] {
			seen[key] = true
			out = append(out, candidate)
		}
	}
	return out, nil
}
func pathKey(path *bgplsv1.ComputedPath) string {
	parts := make([]string, 0, len(path.Hops))
	for _, hop := range path.Hops {
		parts = append(parts, hop.GetNode().GetMeta().GetId())
	}
	return strings.Join(parts, "/")
}

func (g *Graph) Analyze(failedNodes, failedLinks []string, prefixes []*bgplsv1.Prefix) *bgplsv1.AnalyzeImpactResponse {
	blockedNode := map[string]bool{}
	for _, id := range failedNodes {
		blockedNode[id] = true
	}
	blockedLink := map[string]bool{}
	for _, id := range failedLinks {
		blockedLink[id] = true
	}
	undirected := map[string][]string{}
	for id, l := range g.links {
		if blockedLink[id] || blockedNode[l.LocalNodeId] || blockedNode[l.RemoteNodeId] {
			continue
		}
		undirected[l.LocalNodeId] = append(undirected[l.LocalNodeId], l.RemoteNodeId)
		undirected[l.RemoteNodeId] = append(undirected[l.RemoteNodeId], l.LocalNodeId)
	}
	seen := map[string]bool{}
	var comps []*bgplsv1.ImpactComponent
	for id := range g.nodes {
		if blockedNode[id] || seen[id] {
			continue
		}
		queue := []string{id}
		seen[id] = true
		comp := &bgplsv1.ImpactComponent{}
		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]
			comp.NodeIds = append(comp.NodeIds, n)
			for _, next := range undirected[n] {
				if !seen[next] {
					seen[next] = true
					queue = append(queue, next)
				}
			}
		}
		sort.Strings(comp.NodeIds)
		comps = append(comps, comp)
	}
	sort.Slice(comps, func(i, j int) bool {
		if len(comps[i].NodeIds) == len(comps[j].NodeIds) {
			return comps[i].NodeIds[0] < comps[j].NodeIds[0]
		}
		return len(comps[i].NodeIds) > len(comps[j].NodeIds)
	})
	result := &bgplsv1.AnalyzeImpactResponse{Revision: g.revision}
	if len(comps) > 1 {
		result.DisconnectedComponents = comps
		for _, comp := range comps[1:] {
			result.UnreachableNodeIds = append(result.UnreachableNodeIds, comp.NodeIds...)
		}
	}
	for _, p := range prefixes {
		if blockedNode[p.OriginNodeId] {
			result.UnreachablePrefixIds = append(result.UnreachablePrefixIds, p.GetMeta().GetId())
			continue
		}
		for _, id := range result.UnreachableNodeIds {
			if p.OriginNodeId == id {
				result.UnreachablePrefixIds = append(result.UnreachablePrefixIds, p.GetMeta().GetId())
				break
			}
		}
	}
	sort.Strings(result.UnreachableNodeIds)
	sort.Strings(result.UnreachablePrefixIds)
	return result
}
