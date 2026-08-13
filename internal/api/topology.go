package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"github.com/bgpls/bgpls/internal/store"
	"github.com/bgpls/bgpls/internal/topology"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type TopologyService struct{ Store *store.Store }

func apiError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}
func (s *TopologyService) snapshot(revision uint64) (store.Snapshot, error) {
	return s.Store.SnapshotAt(revision)
}

func (s *TopologyService) GetSummary(_ context.Context, req *connect.Request[bgplsv1.GetSummaryRequest]) (*connect.Response[bgplsv1.GetSummaryResponse], error) {
	snap := s.Store.Snapshot()
	out := &bgplsv1.GetSummaryResponse{Revision: snap.Revision, ObservedAt: timestamppb.New(snap.Observed)}
	domains := map[string]bool{}
	for _, n := range snap.Nodes {
		if nodeMatch(n, req.Msg.Filter) {
			out.NodeCount++
			domains[n.GetMeta().GetDomainId()] = true
			if len(n.GetMeta().Conflicts) > 0 {
				out.ConflictCount++
			}
			if n.GetMeta().Freshness != bgplsv1.Freshness_FRESHNESS_ACTIVE {
				out.StaleCount++
			}
		}
	}
	for _, l := range snap.Links {
		if linkMatch(l, req.Msg.Filter) {
			out.LinkCount++
			domains[l.GetMeta().GetDomainId()] = true
			if len(l.GetMeta().Conflicts) > 0 {
				out.ConflictCount++
			}
			if l.GetMeta().Freshness != bgplsv1.Freshness_FRESHNESS_ACTIVE {
				out.StaleCount++
			}
		}
	}
	for _, p := range snap.Prefixes {
		if prefixMatch(p, req.Msg.Filter) {
			out.PrefixCount++
			domains[p.GetMeta().GetDomainId()] = true
			if len(p.GetMeta().Conflicts) > 0 {
				out.ConflictCount++
			}
			if p.GetMeta().Freshness != bgplsv1.Freshness_FRESHNESS_ACTIVE {
				out.StaleCount++
			}
		}
	}
	out.DomainCount = uint64(len(domains))
	out.PeerCount = uint64(len(snap.Peers))
	return connect.NewResponse(out), nil
}
func (s *TopologyService) ListDomains(_ context.Context, req *connect.Request[bgplsv1.ListDomainsRequest]) (*connect.Response[bgplsv1.ListDomainsResponse], error) {
	snap := s.Store.Snapshot()
	size, after := pageBounds(req.Msg.Page)
	var out []*bgplsv1.Domain
	for _, v := range snap.Domains {
		if v.Id <= after {
			continue
		}
		out = append(out, v)
		if len(out) > size {
			break
		}
	}
	more := len(out) > size
	if more {
		out = out[:size]
	}
	token := ""
	if len(out) > 0 {
		token = nextToken(out[len(out)-1].Id, more)
	}
	return connect.NewResponse(&bgplsv1.ListDomainsResponse{Domains: out, Page: &bgplsv1.PageResult{NextPageToken: token, Revision: snap.Revision, ObservedAt: timestamppb.New(snap.Observed)}}), nil
}
func (s *TopologyService) GetNode(_ context.Context, req *connect.Request[bgplsv1.GetNodeRequest]) (*connect.Response[bgplsv1.GetNodeResponse], error) {
	snap, err := s.snapshot(req.Msg.Revision)
	if err != nil {
		return nil, apiError(err)
	}
	for _, v := range snap.Nodes {
		if v.GetMeta().GetId() == req.Msg.Id && (req.Msg.IncludeStale || v.GetMeta().Freshness == bgplsv1.Freshness_FRESHNESS_ACTIVE) {
			return connect.NewResponse(&bgplsv1.GetNodeResponse{Node: v, Revision: snap.Revision}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("node not found"))
}
func (s *TopologyService) ListNodes(_ context.Context, req *connect.Request[bgplsv1.ListNodesRequest]) (*connect.Response[bgplsv1.ListNodesResponse], error) {
	snap, err := s.snapshot(req.Msg.Revision)
	if err != nil {
		return nil, apiError(err)
	}
	size, after := pageBounds(req.Msg.Page)
	var out []*bgplsv1.Node
	for _, v := range snap.Nodes {
		if v.GetMeta().GetId() <= after || !nodeMatch(v, req.Msg.Filter) {
			continue
		}
		out = append(out, v)
		if len(out) > size {
			break
		}
	}
	more := len(out) > size
	if more {
		out = out[:size]
	}
	token := ""
	if len(out) > 0 {
		token = nextToken(out[len(out)-1].GetMeta().GetId(), more)
	}
	return connect.NewResponse(&bgplsv1.ListNodesResponse{Nodes: out, Page: &bgplsv1.PageResult{NextPageToken: token, Revision: snap.Revision, ObservedAt: timestamppb.New(snap.Observed)}}), nil
}
func (s *TopologyService) GetLink(_ context.Context, req *connect.Request[bgplsv1.GetLinkRequest]) (*connect.Response[bgplsv1.GetLinkResponse], error) {
	snap, err := s.snapshot(req.Msg.Revision)
	if err != nil {
		return nil, apiError(err)
	}
	for _, v := range snap.Links {
		if v.GetMeta().GetId() == req.Msg.Id && (req.Msg.IncludeStale || v.GetMeta().Freshness == bgplsv1.Freshness_FRESHNESS_ACTIVE) {
			return connect.NewResponse(&bgplsv1.GetLinkResponse{Link: v, Revision: snap.Revision}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("link not found"))
}
func (s *TopologyService) ListLinks(_ context.Context, req *connect.Request[bgplsv1.ListLinksRequest]) (*connect.Response[bgplsv1.ListLinksResponse], error) {
	snap, err := s.snapshot(req.Msg.Revision)
	if err != nil {
		return nil, apiError(err)
	}
	size, after := pageBounds(req.Msg.Page)
	var out []*bgplsv1.Link
	for _, v := range snap.Links {
		if v.GetMeta().GetId() <= after || !linkMatch(v, req.Msg.Filter) {
			continue
		}
		out = append(out, v)
		if len(out) > size {
			break
		}
	}
	more := len(out) > size
	if more {
		out = out[:size]
	}
	token := ""
	if len(out) > 0 {
		token = nextToken(out[len(out)-1].GetMeta().GetId(), more)
	}
	return connect.NewResponse(&bgplsv1.ListLinksResponse{Links: out, Page: &bgplsv1.PageResult{NextPageToken: token, Revision: snap.Revision, ObservedAt: timestamppb.New(snap.Observed)}}), nil
}
func (s *TopologyService) GetPrefix(_ context.Context, req *connect.Request[bgplsv1.GetPrefixRequest]) (*connect.Response[bgplsv1.GetPrefixResponse], error) {
	snap, err := s.snapshot(req.Msg.Revision)
	if err != nil {
		return nil, apiError(err)
	}
	for _, v := range snap.Prefixes {
		if v.GetMeta().GetId() == req.Msg.Id && (req.Msg.IncludeStale || v.GetMeta().Freshness == bgplsv1.Freshness_FRESHNESS_ACTIVE) {
			return connect.NewResponse(&bgplsv1.GetPrefixResponse{Prefix: v, Revision: snap.Revision}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("prefix not found"))
}
func (s *TopologyService) ListPrefixes(_ context.Context, req *connect.Request[bgplsv1.ListPrefixesRequest]) (*connect.Response[bgplsv1.ListPrefixesResponse], error) {
	snap, err := s.snapshot(req.Msg.Revision)
	if err != nil {
		return nil, apiError(err)
	}
	size, after := pageBounds(req.Msg.Page)
	var out []*bgplsv1.Prefix
	for _, v := range snap.Prefixes {
		if v.GetMeta().GetId() <= after || !prefixMatch(v, req.Msg.Filter) {
			continue
		}
		out = append(out, v)
		if len(out) > size {
			break
		}
	}
	more := len(out) > size
	if more {
		out = out[:size]
	}
	token := ""
	if len(out) > 0 {
		token = nextToken(out[len(out)-1].GetMeta().GetId(), more)
	}
	return connect.NewResponse(&bgplsv1.ListPrefixesResponse{Prefixes: out, Page: &bgplsv1.PageResult{NextPageToken: token, Revision: snap.Revision, ObservedAt: timestamppb.New(snap.Observed)}}), nil
}
func (s *TopologyService) GetNeighbors(_ context.Context, req *connect.Request[bgplsv1.GetNeighborsRequest]) (*connect.Response[bgplsv1.GetNeighborsResponse], error) {
	snap, err := s.snapshot(req.Msg.Revision)
	if err != nil {
		return nil, apiError(err)
	}
	nodes := map[string]*bgplsv1.Node{}
	for _, n := range snap.Nodes {
		nodes[n.GetMeta().GetId()] = n
	}
	out := &bgplsv1.GetNeighborsResponse{Revision: snap.Revision}
	seen := map[string]bool{}
	for _, l := range snap.Links {
		if !linkMatch(l, req.Msg.Filter) {
			continue
		}
		var other string
		if l.LocalNodeId == req.Msg.NodeId {
			other = l.RemoteNodeId
		} else if l.RemoteNodeId == req.Msg.NodeId {
			other = l.LocalNodeId
		} else {
			continue
		}
		out.Links = append(out.Links, l)
		if !seen[other] && nodes[other] != nil {
			seen[other] = true
			out.Nodes = append(out.Nodes, nodes[other])
		}
	}
	return connect.NewResponse(out), nil
}
func (s *TopologyService) Resolve(_ context.Context, req *connect.Request[bgplsv1.ResolveRequest]) (*connect.Response[bgplsv1.ResolveResponse], error) {
	snap := s.Store.Snapshot()
	limit := int(req.Msg.Limit)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := strings.ToLower(req.Msg.Query)
	out := &bgplsv1.ResolveResponse{Revision: snap.Revision}
	for _, n := range snap.Nodes {
		if req.Msg.DomainId != "" && n.GetMeta().DomainId != req.Msg.DomainId {
			continue
		}
		if strings.Contains(strings.ToLower(n.GetMeta().Id), q) || strings.Contains(strings.ToLower(n.Name), q) || strings.Contains(strings.ToLower(n.IgpRouterId), q) || strings.Contains(strings.ToLower(n.BgpRouterId), q) || strings.Contains(strings.ToLower(n.Ipv4RouterId), q) || strings.Contains(strings.ToLower(n.Ipv6RouterId), q) {
			out.Nodes = append(out.Nodes, n)
			if len(out.Nodes) >= limit {
				break
			}
		}
	}
	for _, l := range snap.Links {
		if req.Msg.DomainId != "" && l.GetMeta().DomainId != req.Msg.DomainId {
			continue
		}
		if strings.Contains(strings.ToLower(l.GetMeta().Id), q) || l.LocalAddress == req.Msg.Query || l.RemoteAddress == req.Msg.Query || l.LocalIpv4Address == req.Msg.Query || l.RemoteIpv4Address == req.Msg.Query || l.LocalIpv6Address == req.Msg.Query || l.RemoteIpv6Address == req.Msg.Query {
			out.Links = append(out.Links, l)
			if len(out.Links) >= limit {
				break
			}
		}
	}
	for _, p := range snap.Prefixes {
		if req.Msg.DomainId != "" && p.GetMeta().DomainId != req.Msg.DomainId {
			continue
		}
		if strings.Contains(strings.ToLower(p.GetMeta().Id), q) || strings.Contains(strings.ToLower(p.Prefix), q) {
			out.Prefixes = append(out.Prefixes, p)
			if len(out.Prefixes) >= limit {
				break
			}
		}
	}
	return connect.NewResponse(out), nil
}
func (s *TopologyService) StreamSnapshot(_ context.Context, req *connect.Request[bgplsv1.StreamSnapshotRequest], stream *connect.ServerStream[bgplsv1.StreamSnapshotResponse]) error {
	snap, err := s.snapshot(req.Msg.Revision)
	if err != nil {
		return apiError(err)
	}
	const chunkSize = 500
	first := true
	for ni, li, pi := 0, 0, 0; ni < len(snap.Nodes) || li < len(snap.Links) || pi < len(snap.Prefixes) || first; {
		msg := &bgplsv1.StreamSnapshotResponse{Revision: snap.Revision}
		if first {
			msg.Domains = snap.Domains
			first = false
		}
		for len(msg.Nodes)+len(msg.Links)+len(msg.Prefixes) < chunkSize && ni < len(snap.Nodes) {
			if nodeMatch(snap.Nodes[ni], req.Msg.Filter) {
				msg.Nodes = append(msg.Nodes, snap.Nodes[ni])
			}
			ni++
		}
		for len(msg.Nodes)+len(msg.Links)+len(msg.Prefixes) < chunkSize && li < len(snap.Links) {
			if linkMatch(snap.Links[li], req.Msg.Filter) {
				msg.Links = append(msg.Links, snap.Links[li])
			}
			li++
		}
		for len(msg.Nodes)+len(msg.Links)+len(msg.Prefixes) < chunkSize && pi < len(snap.Prefixes) {
			if prefixMatch(snap.Prefixes[pi], req.Msg.Filter) {
				msg.Prefixes = append(msg.Prefixes, snap.Prefixes[pi])
			}
			pi++
		}
		msg.Last = ni == len(snap.Nodes) && li == len(snap.Links) && pi == len(snap.Prefixes)
		if err := stream.Send(msg); err != nil {
			return err
		}
	}
	return nil
}
func (s *TopologyService) WatchTopology(ctx context.Context, req *connect.Request[bgplsv1.WatchTopologyRequest], stream *connect.ServerStream[bgplsv1.WatchTopologyResponse]) error {
	oldest := s.Store.OldestRevision()
	if oldest > 0 && req.Msg.AfterRevision+1 < oldest {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("revision expired; oldest available is %d", oldest))
	}
	events, err := s.Store.Subscribe(ctx, req.Msg.AfterRevision)
	if err != nil {
		return apiError(err)
	}
	for event := range events {
		if req.Msg.Filter != nil && !containsString(req.Msg.Filter.DomainIds, event.DomainId) {
			continue
		}
		if err := stream.Send(&bgplsv1.WatchTopologyResponse{Event: event}); err != nil {
			return err
		}
	}
	return ctx.Err()
}

type PathService struct{ Store *store.Store }

func (p *PathService) ComputePaths(_ context.Context, req *connect.Request[bgplsv1.ComputePathsRequest]) (*connect.Response[bgplsv1.ComputePathsResponse], error) {
	snap, err := p.Store.SnapshotAt(req.Msg.Revision)
	if err != nil {
		return nil, apiError(err)
	}
	source := resolveNodeID(snap, req.Msg.DomainId, req.Msg.Source)
	destination := resolveNodeID(snap, req.Msg.DomainId, req.Msg.Destination)
	if source == "" || destination == "" {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("source or destination could not be resolved in the requested domain"))
	}
	graph := topology.NewGraph(snap.Revision, filterDomainNodes(snap.Nodes, req.Msg.DomainId), filterDomainLinks(snap.Links, req.Msg.DomainId))
	paths, err := graph.ComputeMany(source, destination, req.Msg.Metric, req.Msg.Constraints, int(req.Msg.MaxPaths))
	if err != nil {
		return connect.NewResponse(&bgplsv1.ComputePathsResponse{Revision: snap.Revision, Explanation: err.Error()}), nil
	}
	return connect.NewResponse(&bgplsv1.ComputePathsResponse{Paths: paths, Revision: snap.Revision}), nil
}
func (p *PathService) AnalyzeImpact(_ context.Context, req *connect.Request[bgplsv1.AnalyzeImpactRequest]) (*connect.Response[bgplsv1.AnalyzeImpactResponse], error) {
	snap, err := p.Store.SnapshotAt(req.Msg.Revision)
	if err != nil {
		return nil, apiError(err)
	}
	graph := topology.NewGraph(snap.Revision, filterDomainNodes(snap.Nodes, req.Msg.DomainId), filterDomainLinks(snap.Links, req.Msg.DomainId))
	return connect.NewResponse(graph.Analyze(req.Msg.FailedNodeIds, req.Msg.FailedLinkIds, filterDomainPrefixes(snap.Prefixes, req.Msg.DomainId))), nil
}
func resolveNodeID(snap store.Snapshot, domain, q string) string {
	for _, n := range snap.Nodes {
		if n.GetMeta().DomainId != domain {
			continue
		}
		if n.GetMeta().Id == q || n.Name == q || n.IgpRouterId == q || n.BgpRouterId == q || n.Ipv4RouterId == q || n.Ipv6RouterId == q {
			return n.GetMeta().Id
		}
	}
	if addr, err := netip.ParseAddr(q); err == nil {
		bestBits := -1
		best := ""
		for _, p := range snap.Prefixes {
			if p.GetMeta().DomainId != domain {
				continue
			}
			prefix, err := netip.ParsePrefix(p.Prefix)
			if err == nil && prefix.Contains(addr) && prefix.Bits() > bestBits {
				bestBits = prefix.Bits()
				best = p.OriginNodeId
			}
		}
		return best
	}
	return ""
}
func filterDomainNodes(values []*bgplsv1.Node, domain string) []*bgplsv1.Node {
	var out []*bgplsv1.Node
	for _, v := range values {
		if v.GetMeta().DomainId == domain {
			out = append(out, v)
		}
	}
	return out
}
func filterDomainLinks(values []*bgplsv1.Link, domain string) []*bgplsv1.Link {
	var out []*bgplsv1.Link
	for _, v := range values {
		if v.GetMeta().DomainId == domain {
			out = append(out, v)
		}
	}
	return out
}
func filterDomainPrefixes(values []*bgplsv1.Prefix, domain string) []*bgplsv1.Prefix {
	var out []*bgplsv1.Prefix
	for _, v := range values {
		if v.GetMeta().DomainId == domain {
			out = append(out, v)
		}
	}
	return out
}

type HistoryService struct{ Store *store.Store }

func (h *HistoryService) ListChanges(_ context.Context, req *connect.Request[bgplsv1.ListChangesRequest]) (*connect.Response[bgplsv1.ListChangesResponse], error) {
	size, _ := pageBounds(req.Msg.Page)
	after := req.Msg.AfterRevision
	if req.Msg.Page != nil && req.Msg.Page.PageToken != "" {
		if decoded, err := base64.RawURLEncoding.DecodeString(req.Msg.Page.PageToken); err == nil {
			if cursor, err := strconv.ParseUint(string(decoded), 10, 64); err == nil && cursor > after {
				after = cursor
			}
		}
	}
	events, err := h.Store.Events(after, req.Msg.BeforeRevision, size+1)
	if err != nil {
		return nil, apiError(err)
	}
	if req.Msg.Filter != nil {
		filtered := events[:0]
		for _, e := range events {
			if containsString(req.Msg.Filter.DomainIds, e.DomainId) {
				filtered = append(filtered, e)
			}
		}
		events = filtered
	}
	more := len(events) > size
	if more {
		events = events[:size]
	}
	token := ""
	if len(events) > 0 {
		token = nextToken(fmt.Sprint(events[len(events)-1].Revision), more)
	}
	return connect.NewResponse(&bgplsv1.ListChangesResponse{Events: events, Page: &bgplsv1.PageResult{NextPageToken: token, Revision: h.Store.Revision()}, OldestAvailableRevision: h.Store.OldestRevision()}), nil
}
func (h *HistoryService) DiffTopology(_ context.Context, req *connect.Request[bgplsv1.DiffTopologyRequest]) (*connect.Response[bgplsv1.DiffTopologyResponse], error) {
	if req.Msg.FromRevision >= req.Msg.ToRevision {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("from_revision must be less than to_revision"))
	}
	var out []*bgplsv1.TopologyEvent
	cursor := req.Msg.FromRevision
	for cursor < req.Msg.ToRevision {
		events, err := h.Store.Events(cursor, req.Msg.ToRevision, 10000)
		if err != nil {
			return nil, apiError(err)
		}
		if len(events) == 0 {
			break
		}
		for _, e := range events {
			if req.Msg.Filter == nil || containsString(req.Msg.Filter.DomainIds, e.DomainId) {
				out = append(out, e)
			}
		}
		cursor = events[len(events)-1].Revision
	}
	return connect.NewResponse(&bgplsv1.DiffTopologyResponse{Changes: out, FromRevision: req.Msg.FromRevision, ToRevision: req.Msg.ToRevision}), nil
}
