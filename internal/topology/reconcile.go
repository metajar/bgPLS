package topology

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"github.com/bgpls/bgpls/internal/store"
	"google.golang.org/protobuf/proto"
)

type Advertisement struct {
	Kind       bgplsv1.EntityKind
	ID         string
	DomainID   string
	PeerID     string
	Preference uint32
	Withdraw   bool
	Value      proto.Message
}

type Reconciler struct {
	mu      sync.Mutex
	store   *store.Store
	sources map[string]map[string]Advertisement
}

func NewReconciler(s *store.Store) *Reconciler {
	return &Reconciler{store: s, sources: map[string]map[string]Advertisement{}}
}
func sourceKey(kind bgplsv1.EntityKind, id string) string { return fmt.Sprintf("%d/%s", kind, id) }

func (r *Reconciler) Apply(ctx context.Context, a Advertisement) (*bgplsv1.TopologyEvent, error) {
	if a.ID == "" || a.PeerID == "" {
		return nil, errors.New("advertisement id and peer id are required")
	}
	if !a.Withdraw && a.Value == nil {
		return nil, errors.New("advertisement value is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := sourceKey(a.Kind, a.ID)
	sources := r.sources[key]
	if sources == nil {
		sources = map[string]Advertisement{}
		r.sources[key] = sources
	}
	if a.Withdraw {
		delete(sources, a.PeerID)
	} else {
		a.Value = proto.Clone(a.Value)
		sources[a.PeerID] = a
	}
	if len(sources) == 0 {
		delete(r.sources, key)
		return r.store.Apply(ctx, store.Mutation{Kind: a.Kind, ID: a.ID, DomainID: a.DomainID, PeerID: a.PeerID, Delete: true, Reason: "last source withdrawn"})
	}
	ordered := make([]Advertisement, 0, len(sources))
	for _, source := range sources {
		ordered = append(ordered, source)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Preference == ordered[j].Preference {
			return ordered[i].PeerID < ordered[j].PeerID
		}
		return ordered[i].Preference > ordered[j].Preference
	})
	canonical := proto.Clone(ordered[0].Value)
	sourceIDs := make([]string, 0, len(ordered))
	for _, source := range ordered {
		sourceIDs = append(sourceIDs, source.PeerID)
	}
	for _, source := range ordered[1:] {
		mergeEntity(canonical, source.Value, ordered[0].PeerID, source.PeerID)
	}
	setSources(canonical, sourceIDs)
	return r.store.Apply(ctx, store.Mutation{Kind: a.Kind, ID: a.ID, DomainID: a.DomainID, PeerID: a.PeerID, Value: canonical, Reason: "canonical topology reconciled"})
}

func setSources(v proto.Message, sources []string) {
	switch x := v.(type) {
	case *bgplsv1.Node:
		if x.Meta == nil {
			x.Meta = &bgplsv1.EntityMeta{}
		}
		x.Meta.SourcePeerIds = sources
	case *bgplsv1.Link:
		if x.Meta == nil {
			x.Meta = &bgplsv1.EntityMeta{}
		}
		x.Meta.SourcePeerIds = sources
	case *bgplsv1.Prefix:
		if x.Meta == nil {
			x.Meta = &bgplsv1.EntityMeta{}
		}
		x.Meta.SourcePeerIds = sources
	}
}

func mergeEntity(selected, other proto.Message, selectedPeer, otherPeer string) {
	switch a := selected.(type) {
	case *bgplsv1.Node:
		b, ok := other.(*bgplsv1.Node)
		if !ok {
			return
		}
		if a.Meta == nil {
			a.Meta = &bgplsv1.EntityMeta{}
		}
		mergeString("name", &a.Name, b.Name, a.Meta, selectedPeer, otherPeer)
		mergeString("igp_router_id", &a.IgpRouterId, b.IgpRouterId, a.Meta, selectedPeer, otherPeer)
		mergeString("bgp_router_id", &a.BgpRouterId, b.BgpRouterId, a.Meta, selectedPeer, otherPeer)
		mergeString("ipv4_router_id", &a.Ipv4RouterId, b.Ipv4RouterId, a.Meta, selectedPeer, otherPeer)
		mergeString("ipv6_router_id", &a.Ipv6RouterId, b.Ipv6RouterId, a.Meta, selectedPeer, otherPeer)
		mergeString("area_id", &a.AreaId, b.AreaId, a.Meta, selectedPeer, otherPeer)
		a.Meta.DecodedTlvs = mergeTLVs(a.Meta.DecodedTlvs, b.GetMeta().GetDecodedTlvs())
	case *bgplsv1.Link:
		b, ok := other.(*bgplsv1.Link)
		if !ok {
			return
		}
		if a.Meta == nil {
			a.Meta = &bgplsv1.EntityMeta{}
		}
		mergeString("local_address", &a.LocalAddress, b.LocalAddress, a.Meta, selectedPeer, otherPeer)
		mergeString("remote_address", &a.RemoteAddress, b.RemoteAddress, a.Meta, selectedPeer, otherPeer)
		mergeUint64("igp_metric", &a.IgpMetric, b.IgpMetric, a.Meta, selectedPeer, otherPeer)
		mergeUint64("te_metric", &a.TeMetric, b.TeMetric, a.Meta, selectedPeer, otherPeer)
		mergeUint64("delay_microseconds", &a.DelayMicroseconds, b.DelayMicroseconds, a.Meta, selectedPeer, otherPeer)
		a.Meta.DecodedTlvs = mergeTLVs(a.Meta.DecodedTlvs, b.GetMeta().GetDecodedTlvs())
	case *bgplsv1.Prefix:
		b, ok := other.(*bgplsv1.Prefix)
		if !ok {
			return
		}
		if a.Meta == nil {
			a.Meta = &bgplsv1.EntityMeta{}
		}
		mergeUint64("metric", &a.Metric, b.Metric, a.Meta, selectedPeer, otherPeer)
		mergeString("forwarding_address", &a.ForwardingAddress, b.ForwardingAddress, a.Meta, selectedPeer, otherPeer)
		a.Meta.DecodedTlvs = mergeTLVs(a.Meta.DecodedTlvs, b.GetMeta().GetDecodedTlvs())
	}
}
func mergeString(field string, selected *string, other string, meta *bgplsv1.EntityMeta, selectedPeer, otherPeer string) {
	if *selected == "" {
		*selected = other
		return
	}
	if other != "" && *selected != other {
		meta.Conflicts = append(meta.Conflicts, &bgplsv1.Conflict{Field: field, SelectedValue: *selected, SelectedPeerId: selectedPeer, RejectedValue: other, RejectedPeerId: otherPeer})
	}
}
func mergeUint64(field string, selected *uint64, other uint64, meta *bgplsv1.EntityMeta, selectedPeer, otherPeer string) {
	if *selected == 0 {
		*selected = other
		return
	}
	if other != 0 && *selected != other {
		meta.Conflicts = append(meta.Conflicts, &bgplsv1.Conflict{Field: field, SelectedValue: fmt.Sprint(*selected), SelectedPeerId: selectedPeer, RejectedValue: fmt.Sprint(other), RejectedPeerId: otherPeer})
	}
}
func mergeTLVs(selected, other []*bgplsv1.RawTlv) []*bgplsv1.RawTlv {
	seen := map[string]bool{}
	for _, v := range selected {
		seen[fmt.Sprintf("%s/%d/%s", v.Registry, v.Type, v.Value)] = true
	}
	for _, v := range other {
		key := fmt.Sprintf("%s/%d/%s", v.Registry, v.Type, v.Value)
		if !seen[key] {
			selected = append(selected, proto.Clone(v).(*bgplsv1.RawTlv))
			seen[key] = true
		}
	}
	return selected
}

func (r *Reconciler) MarkPeerStale(ctx context.Context, peerID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sources := range r.sources {
		a, ok := sources[peerID]
		if !ok {
			continue
		}
		switch x := a.Value.(type) {
		case *bgplsv1.Node:
			x = proto.Clone(x).(*bgplsv1.Node)
			x.Meta.Freshness = bgplsv1.Freshness_FRESHNESS_STALE_SOURCE_LOST
			a.Value = x
		case *bgplsv1.Link:
			x = proto.Clone(x).(*bgplsv1.Link)
			x.Meta.Freshness = bgplsv1.Freshness_FRESHNESS_STALE_SOURCE_LOST
			a.Value = x
		case *bgplsv1.Prefix:
			x = proto.Clone(x).(*bgplsv1.Prefix)
			x.Meta.Freshness = bgplsv1.Freshness_FRESHNESS_STALE_SOURCE_LOST
			a.Value = x
		}
		sources[peerID] = a
	}
	return nil
}
