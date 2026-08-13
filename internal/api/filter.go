package api

import (
	"encoding/base64"
	"strings"

	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
)

func pageBounds(page *bgplsv1.Page) (int, string) {
	if page == nil {
		return 100, ""
	}
	size := int(page.PageSize)
	if size <= 0 {
		size = 100
	}
	if size > 1000 {
		size = 1000
	}
	tokenBytes, _ := base64.RawURLEncoding.DecodeString(page.PageToken)
	return size, string(tokenBytes)
}
func nextToken(id string, more bool) string {
	if !more {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}
func containsString(values []string, value string) bool {
	if len(values) == 0 {
		return true
	}
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func containsFreshness(values []bgplsv1.Freshness, value bgplsv1.Freshness) bool {
	if len(values) == 0 {
		return value == bgplsv1.Freshness_FRESHNESS_ACTIVE
	}
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func sourcesMatch(filter []string, sources []string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, f := range filter {
		for _, s := range sources {
			if f == s {
				return true
			}
		}
	}
	return false
}
func nodeMatch(n *bgplsv1.Node, f *bgplsv1.TopologyFilter) bool {
	if f == nil {
		return n.GetMeta().GetFreshness() == bgplsv1.Freshness_FRESHNESS_ACTIVE
	}
	m := n.GetMeta()
	if !containsString(f.DomainIds, m.GetDomainId()) || !containsFreshness(f.Freshness, m.GetFreshness()) || !sourcesMatch(f.SourcePeerIds, m.GetSourcePeerIds()) {
		return false
	}
	if f.ConflictsOnly && len(m.Conflicts) == 0 {
		return false
	}
	if len(f.Protocols) > 0 {
		ok := false
		for _, p := range f.Protocols {
			if p == n.Protocol {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	q := strings.ToLower(f.Query)
	return q == "" || strings.Contains(strings.ToLower(m.Id), q) || strings.Contains(strings.ToLower(n.Name), q) || strings.Contains(strings.ToLower(n.IgpRouterId), q) || strings.Contains(strings.ToLower(n.BgpRouterId), q) || strings.Contains(strings.ToLower(n.Ipv4RouterId), q) || strings.Contains(strings.ToLower(n.Ipv6RouterId), q)
}
func linkMatch(l *bgplsv1.Link, f *bgplsv1.TopologyFilter) bool {
	if f == nil {
		return l.GetMeta().GetFreshness() == bgplsv1.Freshness_FRESHNESS_ACTIVE
	}
	m := l.GetMeta()
	if !containsString(f.DomainIds, m.GetDomainId()) || !containsFreshness(f.Freshness, m.GetFreshness()) || !sourcesMatch(f.SourcePeerIds, m.GetSourcePeerIds()) {
		return false
	}
	if f.ConflictsOnly && len(m.Conflicts) == 0 {
		return false
	}
	if len(f.MultiTopologyIds) > 0 {
		ok := false
		for _, id := range f.MultiTopologyIds {
			if id == l.MultiTopologyId {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	q := strings.ToLower(f.Query)
	return q == "" || strings.Contains(strings.ToLower(m.Id), q) || strings.Contains(strings.ToLower(l.LocalAddress), q) || strings.Contains(strings.ToLower(l.RemoteAddress), q)
}
func prefixMatch(p *bgplsv1.Prefix, f *bgplsv1.TopologyFilter) bool {
	if f == nil {
		return p.GetMeta().GetFreshness() == bgplsv1.Freshness_FRESHNESS_ACTIVE
	}
	m := p.GetMeta()
	if !containsString(f.DomainIds, m.GetDomainId()) || !containsFreshness(f.Freshness, m.GetFreshness()) || !sourcesMatch(f.SourcePeerIds, m.GetSourcePeerIds()) {
		return false
	}
	if f.ConflictsOnly && len(m.Conflicts) == 0 {
		return false
	}
	if len(f.MultiTopologyIds) > 0 {
		ok := false
		for _, id := range f.MultiTopologyIds {
			if id == p.MultiTopologyId {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	q := strings.ToLower(f.Query)
	return q == "" || strings.Contains(strings.ToLower(m.Id), q) || strings.Contains(strings.ToLower(p.Prefix), q) || strings.Contains(strings.ToLower(p.ForwardingAddress), q)
}
