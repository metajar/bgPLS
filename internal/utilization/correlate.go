package utilization

import (
	"net/netip"
	"strings"

	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
)

const (
	ReasonNoMatch    = "no_match"
	ReasonAmbiguous  = "ambiguous"
	ReasonUnnumbered = "unnumbered"
)

type linkRef struct {
	linkID       string
	localNodeID  string
	remoteNodeID string
	remoteIPv4   string
	remoteIPv6   string
	remoteAddr   string
}

type addrIndex struct {
	byAddr map[string][]linkRef
	byID   map[string]linkRef
}

func newAddrIndex(links []*bgplsv1.Link) *addrIndex {
	idx := &addrIndex{byAddr: map[string][]linkRef{}, byID: map[string]linkRef{}}
	for _, l := range links {
		idx.add(l)
	}
	return idx
}

func (idx *addrIndex) add(l *bgplsv1.Link) {
	if l == nil || l.GetMeta().GetId() == "" {
		return
	}
	idx.remove(l.GetMeta().GetId())
	ref := linkRef{
		linkID:       l.GetMeta().GetId(),
		localNodeID:  l.LocalNodeId,
		remoteNodeID: l.RemoteNodeId,
		remoteIPv4:   l.RemoteIpv4Address,
		remoteIPv6:   l.RemoteIpv6Address,
		remoteAddr:   l.RemoteAddress,
	}
	idx.byID[ref.linkID] = ref
	for _, addr := range []string{l.LocalAddress, l.LocalIpv4Address, l.LocalIpv6Address} {
		key := canonicalIP(addr)
		if key == "" {
			continue
		}
		idx.byAddr[key] = append(idx.byAddr[key], ref)
	}
}

func (idx *addrIndex) remove(id string) {
	ref, ok := idx.byID[id]
	if !ok {
		return
	}
	delete(idx.byID, id)
	for key, refs := range idx.byAddr {
		filtered := refs[:0]
		for _, r := range refs {
			if r.linkID != id {
				filtered = append(filtered, r)
			}
		}
		if len(filtered) == 0 {
			delete(idx.byAddr, key)
		} else {
			idx.byAddr[key] = filtered
		}
	}
	_ = ref
}

func canonicalIP(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if prefix, err := netip.ParsePrefix(s); err == nil {
		return prefix.Addr().String()
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return ""
	}
	return addr.String()
}

type lookupResult struct {
	forward    linkRef
	ok         bool
	ambiguous  bool
	unnumbered bool
}

func (idx *addrIndex) lookup(ipv4, ipv6 []string) lookupResult {
	addrs := make([]string, 0, len(ipv4)+len(ipv6))
	addrs = append(addrs, ipv4...)
	addrs = append(addrs, ipv6...)
	if len(addrs) == 0 {
		return lookupResult{unnumbered: true}
	}
	var hits []linkRef
	seen := map[string]bool{}
	nodes := map[string]bool{}
	for _, raw := range addrs {
		key := canonicalIP(raw)
		if key == "" {
			continue
		}
		for _, ref := range idx.byAddr[key] {
			if seen[ref.linkID] {
				continue
			}
			seen[ref.linkID] = true
			hits = append(hits, ref)
			nodes[ref.localNodeID] = true
		}
	}
	if len(hits) == 0 {
		return lookupResult{}
	}
	if len(nodes) > 1 {
		return lookupResult{ambiguous: true}
	}
	return lookupResult{forward: hits[0], ok: true}
}

func (idx *addrIndex) reverseOf(forward linkRef) (linkRef, bool) {
	for _, addr := range []string{forward.remoteAddr, forward.remoteIPv4, forward.remoteIPv6} {
		key := canonicalIP(addr)
		if key == "" {
			continue
		}
		for _, ref := range idx.byAddr[key] {
			if ref.localNodeID == forward.remoteNodeID && ref.remoteNodeID == forward.localNodeID {
				return ref, true
			}
		}
	}
	for _, ref := range idx.byID {
		if ref.localNodeID == forward.remoteNodeID && ref.remoteNodeID == forward.localNodeID {
			return ref, true
		}
	}
	return linkRef{}, false
}
