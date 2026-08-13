package bgp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"github.com/bgpls/bgpls/internal/config"
	"github.com/bgpls/bgpls/internal/store"
	"github.com/bgpls/bgpls/internal/topology"
	api "github.com/osrg/gobgp/v4/api"
	"github.com/osrg/gobgp/v4/pkg/apiutil"
	packet "github.com/osrg/gobgp/v4/pkg/packet/bgp"
	"github.com/osrg/gobgp/v4/pkg/server"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	pathsTotal        = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "bgpls", Subsystem: "bgp", Name: "ls_paths_total", Help: "BGP-LS paths handled by the collector."}, []string{"peer", "operation", "freshness", "outcome", "kind"})
	decodeErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "bgpls", Subsystem: "bgp", Name: "ls_decode_errors_total", Help: "BGP-LS paths that could not be normalized."}, []string{"peer", "class"})
)

var errUnsupportedNLRI = errors.New("unsupported BGP-LS NLRI")

func init() { prometheus.MustRegister(pathsTotal, decodeErrorsTotal) }

// Collector embeds GoBGP but exposes only bgPLS peer operations to the rest of
// the application. This keeps the public API independent of the BGP engine.
type Collector struct {
	mu         sync.RWMutex
	server     *server.BgpServer
	store      *store.Store
	reconciler *topology.Reconciler
	peers      map[string]*bgplsv1.Peer
	byAddress  map[string]string
	cfg        config.BGP
	started    bool
}

func New(cfg config.BGP, s *store.Store) *Collector {
	return &Collector{store: s, reconciler: topology.NewReconciler(s), peers: map[string]*bgplsv1.Peer{}, byAddress: map[string]string{}, cfg: cfg}
}

func (c *Collector) Start(ctx context.Context, peers []*bgplsv1.Peer) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return errors.New("BGP collector is already started")
	}
	if c.cfg.RouterID == "" {
		return errors.New("bgp.router_id is required when peers are configured")
	}
	globalAS := uint32(64512)
	if len(peers) > 0 && peers[0].LocalAs != 0 {
		globalAS = peers[0].LocalAs
	}
	c.server = server.NewBgpServer()
	go c.server.Serve()
	listenPort := c.cfg.ListenPort
	if listenPort == 0 {
		listenPort = 179
	}
	if err := c.server.StartBgp(ctx, &api.StartBgpRequest{Global: &api.Global{Asn: globalAS, RouterId: c.cfg.RouterID, ListenPort: listenPort, ListenAddresses: c.cfg.ListenAddresses}}); err != nil {
		return fmt.Errorf("start GoBGP: %w", err)
	}
	callbacks := server.WatchEventMessageCallbacks{OnPathUpdate: c.handlePaths, OnPeerUpdate: c.handlePeer}
	if err := c.server.WatchEvent(ctx, callbacks, server.WatchUpdate(true, "", ""), server.WatchPeer()); err != nil {
		return fmt.Errorf("watch GoBGP events: %w", err)
	}
	c.started = true
	for _, peer := range peers {
		if err := c.upsertLocked(ctx, peer); err != nil {
			return err
		}
	}
	return nil
}

func (c *Collector) Close(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return nil
	}
	err := c.server.StopBgp(ctx, &api.StopBgpRequest{})
	c.server.Stop()
	c.started = false
	return err
}

func toGoBGPPeer(p *bgplsv1.Peer) *api.Peer {
	return &api.Peer{Conf: &api.PeerConf{AuthPassword: p.TcpMd5Secret, Description: p.Name, LocalAsn: p.LocalAs, NeighborAddress: p.RemoteAddress, PeerAsn: p.RemoteAs, AdminDown: !p.Enabled}, Transport: &api.Transport{LocalAddress: p.LocalAddress, PassiveMode: p.Passive}, EbgpMultihop: &api.EbgpMultihop{Enabled: p.EbgpMultihop, MultihopTtl: p.MultihopTtl}, TtlSecurity: &api.TtlSecurity{Enabled: p.Gtsm, TtlMin: 255}, ApplyPolicy: &api.ApplyPolicy{ImportPolicy: &api.PolicyAssignment{DefaultAction: api.RouteAction_ROUTE_ACTION_ACCEPT}, ExportPolicy: &api.PolicyAssignment{DefaultAction: api.RouteAction_ROUTE_ACTION_REJECT}}, AfiSafis: []*api.AfiSafi{{Config: &api.AfiSafiConfig{Family: &api.Family{Afi: api.Family_AFI_LS, Safi: api.Family_SAFI_LS}, Enabled: true}}}}
}

func (c *Collector) upsertLocked(ctx context.Context, p *bgplsv1.Peer) error {
	if !c.started {
		return errors.New("BGP collector is not started")
	}
	if old := c.peers[p.Id]; old != nil {
		if err := c.server.DeletePeer(ctx, &api.DeletePeerRequest{Address: old.RemoteAddress}); err != nil {
			slog.Warn("delete peer before update", "peer", p.Id, "error", err)
		}
	}
	if err := c.server.AddPeer(ctx, &api.AddPeerRequest{Peer: toGoBGPPeer(p)}); err != nil {
		return fmt.Errorf("add BGP peer %s: %w", p.Id, err)
	}
	cp := proto.Clone(p).(*bgplsv1.Peer)
	c.peers[p.Id] = cp
	c.byAddress[p.RemoteAddress] = p.Id
	return nil
}
func (c *Collector) Upsert(ctx context.Context, p *bgplsv1.Peer) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.upsertLocked(ctx, p)
}
func (c *Collector) Delete(ctx context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.peers[id]
	if p == nil {
		return store.ErrNotFound
	}
	if err := c.server.DeletePeer(ctx, &api.DeletePeerRequest{Address: p.RemoteAddress}); err != nil {
		return err
	}
	delete(c.byAddress, p.RemoteAddress)
	delete(c.peers, id)
	return nil
}
func (c *Collector) Reset(ctx context.Context, id string, soft bool) error {
	c.mu.RLock()
	p := c.peers[id]
	c.mu.RUnlock()
	if p == nil {
		return store.ErrNotFound
	}
	direction := api.ResetPeerRequest_DIRECTION_UNSPECIFIED
	if soft {
		direction = api.ResetPeerRequest_DIRECTION_IN
	}
	return c.server.ResetPeer(ctx, &api.ResetPeerRequest{Address: p.RemoteAddress, Soft: soft, Direction: direction})
}

func (c *Collector) peerForAddress(address string) (*bgplsv1.Peer, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id, ok := c.byAddress[address]
	if !ok {
		return nil, false
	}
	p := c.peers[id]
	return proto.Clone(p).(*bgplsv1.Peer), true
}

func (c *Collector) handlePeer(event *apiutil.WatchEventMessage_PeerEvent, observed time.Time) {
	if event == nil || event.Type != apiutil.PEER_EVENT_STATE {
		return
	}
	address := event.Peer.State.NeighborAddress.String()
	p, ok := c.peerForAddress(address)
	if !ok {
		return
	}
	p.SessionState = mapFSM(event.Peer.State.SessionState)
	p.RouterId = formatAddr(event.Peer.State.RouterID)
	p.LastStateChange = timestamppb.New(observed)
	p.LastError = event.Peer.State.DisconnectMessage
	if p.SessionState == bgplsv1.PeerSessionState_PEER_SESSION_STATE_ESTABLISHED {
		p.EstablishedAt = timestamppb.New(observed)
	}
	_, err := c.store.Apply(context.Background(), store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_PEER, ID: p.Id, DomainID: p.DomainId, Value: p, Reason: "BGP session state changed"})
	if err != nil {
		slog.Error("persist peer state", "peer", p.Id, "error", err)
	}
}
func mapFSM(state packet.FSMState) bgplsv1.PeerSessionState {
	switch state {
	case packet.BGP_FSM_IDLE:
		return bgplsv1.PeerSessionState_PEER_SESSION_STATE_IDLE
	case packet.BGP_FSM_CONNECT:
		return bgplsv1.PeerSessionState_PEER_SESSION_STATE_CONNECT
	case packet.BGP_FSM_ACTIVE:
		return bgplsv1.PeerSessionState_PEER_SESSION_STATE_ACTIVE
	case packet.BGP_FSM_OPENSENT:
		return bgplsv1.PeerSessionState_PEER_SESSION_STATE_OPEN_SENT
	case packet.BGP_FSM_OPENCONFIRM:
		return bgplsv1.PeerSessionState_PEER_SESSION_STATE_OPEN_CONFIRM
	case packet.BGP_FSM_ESTABLISHED:
		return bgplsv1.PeerSessionState_PEER_SESSION_STATE_ESTABLISHED
	default:
		return bgplsv1.PeerSessionState_PEER_SESSION_STATE_UNSPECIFIED
	}
}

func (c *Collector) handlePaths(paths []*apiutil.Path, _ time.Time) {
	for _, path := range paths {
		if path.Family != packet.RF_LS {
			continue
		}
		peer, ok := c.peerForAddress(path.PeerAddress.String())
		if !ok {
			continue
		}
		err := c.safeHandlePath(context.Background(), peer, path)
		operation := "announce"
		if path.Withdrawal {
			operation = "withdraw"
		}
		freshness := "active"
		if path.Stale {
			freshness = "stale"
		}
		if errors.Is(err, errUnsupportedNLRI) {
			pathsTotal.WithLabelValues(peer.Id, operation, freshness, "ignored", "unsupported").Inc()
			decodeErrorsTotal.WithLabelValues(peer.Id, "unsupported_nlri").Inc()
			slog.Warn("ignore unsupported BGP-LS NLRI", "peer", peer.Id, "nlri", fmt.Sprintf("%T", path.Nlri))
			continue
		}
		if err != nil {
			pathsTotal.WithLabelValues(peer.Id, operation, freshness, "rejected", nlriKind(path)).Inc()
			decodeErrorsTotal.WithLabelValues(peer.Id, classifyDecodeError(err)).Inc()
			slog.Error("process BGP-LS path", "peer", peer.Id, "error", err)
			c.incrementRejected(peer)
			continue
		}
		pathsTotal.WithLabelValues(peer.Id, operation, freshness, "accepted", nlriKind(path)).Inc()
	}
}

func (c *Collector) safeHandlePath(ctx context.Context, peer *bgplsv1.Peer, path *apiutil.Path) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("malformed decoded BGP-LS path: %v", recovered)
		}
	}()
	return c.handlePath(ctx, peer, path)
}
func classifyDecodeError(err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "descriptor"):
		return "missing_descriptor"
	case strings.Contains(message, "prefix"):
		return "invalid_prefix"
	case strings.Contains(message, "unexpected NLRI"):
		return "unexpected_nlri"
	case strings.Contains(message, "malformed"):
		return "malformed_decoded_path"
	default:
		return "normalization_error"
	}
}
func nlriKind(path *apiutil.Path) string {
	addr, ok := path.Nlri.(*packet.LsAddrPrefix)
	if !ok || addr.NLRI == nil {
		return "unexpected"
	}
	switch addr.NLRI.(type) {
	case *packet.LsNodeNLRI:
		return "node"
	case *packet.LsLinkNLRI:
		return "link"
	case *packet.LsPrefixV4NLRI:
		return "prefix_v4"
	case *packet.LsPrefixV6NLRI:
		return "prefix_v6"
	default:
		return "unsupported"
	}
}
func (c *Collector) incrementRejected(peer *bgplsv1.Peer) {
	raw, err := c.store.Get(bgplsv1.EntityKind_ENTITY_KIND_PEER, peer.Id)
	if err != nil {
		return
	}
	p := raw.(*bgplsv1.Peer)
	p.RejectedUpdates++
	_, _ = c.store.Apply(context.Background(), store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_PEER, ID: p.Id, DomainID: p.DomainId, Value: p, Reason: "BGP-LS update rejected"})
}

func (c *Collector) handlePath(ctx context.Context, peer *bgplsv1.Peer, path *apiutil.Path) error {
	addr, ok := path.Nlri.(*packet.LsAddrPrefix)
	if !ok || addr.NLRI == nil {
		return fmt.Errorf("unexpected NLRI %T", path.Nlri)
	}
	attrs := extractAttributes(path.Attrs)
	protocol := bgplsv1.Protocol(addrProtocol(addr))
	raw := decodedAttributeTLVs(path.Attrs)
	switch nlri := addr.NLRI.(type) {
	case *packet.LsNodeNLRI:
		descriptor, ok := nlri.LocalNodeDesc.(*packet.LsTLVNodeDescriptor)
		if !ok || descriptor == nil {
			return errors.New("node local descriptor is missing")
		}
		desc := descriptor.Extract()
		id := nodeID(peer.DomainId, nlri.ProtocolID, nlri.Identifier, desc)
		node := nodeFrom(desc, id, peer.DomainId, protocol, attrs, raw)
		setFreshness(node, path.Stale)
		return c.apply(ctx, peer, path.Withdrawal, bgplsv1.EntityKind_ENTITY_KIND_NODE, id, node)
	case *packet.LsLinkNLRI:
		localDescriptor, localOK := nlri.LocalNodeDesc.(*packet.LsTLVNodeDescriptor)
		remoteDescriptor, remoteOK := nlri.RemoteNodeDesc.(*packet.LsTLVNodeDescriptor)
		if !localOK || !remoteOK || localDescriptor == nil || remoteDescriptor == nil {
			return errors.New("link local or remote descriptor is missing")
		}
		local := localDescriptor.Extract()
		remote := remoteDescriptor.Extract()
		localID := nodeID(peer.DomainId, nlri.ProtocolID, nlri.Identifier, local)
		remoteID := nodeID(peer.DomainId, nlri.ProtocolID, nlri.Identifier, remote)
		desc := &packet.LsLinkDescriptor{}
		desc.ParseTLVs(nlri.LinkDesc)
		id := stableID("link", peer.DomainId, fmt.Sprint(nlri.ProtocolID), fmt.Sprint(nlri.Identifier), localID, remoteID, fmt.Sprint(desc))
		link := linkFrom(id, peer.DomainId, localID, remoteID, desc, attrs, raw)
		setFreshness(link, path.Stale)
		return c.apply(ctx, peer, path.Withdrawal, bgplsv1.EntityKind_ENTITY_KIND_LINK, id, link)
	case *packet.LsPrefixV4NLRI:
		return c.applyPrefixes(ctx, peer, path.Withdrawal, path.Stale, nlri.ProtocolID, nlri.Identifier, nlri.LocalNodeDesc, nlri.PrefixDesc, false, attrs, raw)
	case *packet.LsPrefixV6NLRI:
		return c.applyPrefixes(ctx, peer, path.Withdrawal, path.Stale, nlri.ProtocolID, nlri.Identifier, nlri.LocalNodeDesc, nlri.PrefixDesc, true, attrs, raw)
	default:
		return fmt.Errorf("%w: %T", errUnsupportedNLRI, addr.NLRI)
	}
}
func addrProtocol(addr *packet.LsAddrPrefix) packet.LsProtocolID {
	switch n := addr.NLRI.(type) {
	case *packet.LsNodeNLRI:
		return n.ProtocolID
	case *packet.LsLinkNLRI:
		return n.ProtocolID
	case *packet.LsPrefixV4NLRI:
		return n.ProtocolID
	case *packet.LsPrefixV6NLRI:
		return n.ProtocolID
	}
	return 0
}
func (c *Collector) apply(ctx context.Context, peer *bgplsv1.Peer, withdraw bool, kind bgplsv1.EntityKind, id string, value proto.Message) error {
	_, err := c.reconciler.Apply(ctx, topology.Advertisement{Kind: kind, ID: id, DomainID: peer.DomainId, PeerID: peer.Id, Preference: peer.SourcePreference, Withdraw: withdraw, Value: value})
	return err
}
func (c *Collector) applyPrefixes(ctx context.Context, peer *bgplsv1.Peer, withdraw, stale bool, protocol packet.LsProtocolID, identifier uint64, nodeTLV packet.LsTLVInterface, tlvs []packet.LsTLVInterface, ipv6 bool, attrs *packet.LsAttribute, raw []*bgplsv1.RawTlv) error {
	descriptor, ok := nodeTLV.(*packet.LsTLVNodeDescriptor)
	if !ok || descriptor == nil {
		return errors.New("prefix local descriptor is missing")
	}
	desc := descriptor.Extract()
	origin := nodeID(peer.DomainId, protocol, identifier, desc)
	pd := &packet.LsPrefixDescriptor{}
	pd.ParseTLVs(tlvs, ipv6)
	mt := firstMT(pd.MultiTopoIDs)
	for _, network := range pd.IPReachability {
		id := stableID("prefix", peer.DomainId, fmt.Sprint(protocol), fmt.Sprint(identifier), origin, network.String(), fmt.Sprint(mt))
		p := &bgplsv1.Prefix{Meta: &bgplsv1.EntityMeta{Id: id, DomainId: peer.DomainId, Freshness: bgplsv1.Freshness_FRESHNESS_ACTIVE, DecodedTlvs: raw}, Prefix: network.String(), OriginNodeId: origin, MultiTopologyId: uint32(mt)}
		if attrs != nil && attrs.Prefix.SrPrefixSID != nil {
			p.PrefixSid = *attrs.Prefix.SrPrefixSID
		}
		setFreshness(p, stale)
		if err := c.apply(ctx, peer, withdraw, bgplsv1.EntityKind_ENTITY_KIND_PREFIX, id, p); err != nil {
			return err
		}
	}
	return nil
}

func setFreshness(value proto.Message, stale bool) {
	if !stale {
		return
	}
	switch v := value.(type) {
	case *bgplsv1.Node:
		v.Meta.Freshness = bgplsv1.Freshness_FRESHNESS_STALE_SOURCE_LOST
	case *bgplsv1.Link:
		v.Meta.Freshness = bgplsv1.Freshness_FRESHNESS_STALE_SOURCE_LOST
	case *bgplsv1.Prefix:
		v.Meta.Freshness = bgplsv1.Freshness_FRESHNESS_STALE_SOURCE_LOST
	}
}

func stableID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}
func nodeID(domain string, protocol packet.LsProtocolID, identifier uint64, d *packet.LsNodeDescriptor) string {
	return stableID("node", domain, fmt.Sprint(protocol), fmt.Sprint(identifier), fmt.Sprint(d.Asn), fmt.Sprint(d.BGPLsID), fmt.Sprint(d.OspfAreaID), d.IGPRouterID, formatAddr(d.BGPRouterID), fmt.Sprint(d.BGPConfederationMember))
}
func nodeFrom(d *packet.LsNodeDescriptor, id, domain string, protocol bgplsv1.Protocol, attrs *packet.LsAttribute, raw []*bgplsv1.RawTlv) *bgplsv1.Node {
	n := &bgplsv1.Node{Meta: &bgplsv1.EntityMeta{Id: id, DomainId: domain, Freshness: bgplsv1.Freshness_FRESHNESS_ACTIVE, DecodedTlvs: raw}, Protocol: protocol, AutonomousSystem: d.Asn, AreaId: areaID(protocol, d, attrs), IgpRouterId: d.IGPRouterID, BgpRouterId: formatAddr(d.BGPRouterID), Pseudonode: d.PseudoNode}
	if attrs != nil {
		if attrs.Node.Name != nil {
			n.Name = *attrs.Node.Name
		}
		n.Ipv4RouterId = formatAddrPtr(attrs.Node.LocalRouterID)
		n.Ipv6RouterId = formatAddrPtr(attrs.Node.LocalRouterIDv6)
		if attrs.Node.SrAlgorithms != nil {
			for _, v := range *attrs.Node.SrAlgorithms {
				n.Algorithms = append(n.Algorithms, uint32(v))
			}
		}
	}
	return n
}
func linkFrom(id, domain, local, remote string, d *packet.LsLinkDescriptor, attrs *packet.LsAttribute, raw []*bgplsv1.RawTlv) *bgplsv1.Link {
	l := &bgplsv1.Link{Meta: &bgplsv1.EntityMeta{Id: id, DomainId: domain, Freshness: bgplsv1.Freshness_FRESHNESS_ACTIVE, DecodedTlvs: raw}, LocalNodeId: local, RemoteNodeId: remote, MultiTopologyId: uint32(firstMT(d.MultiTopoIDs))}
	if d.LinkLocalID != nil {
		l.LocalLinkId = uint64(*d.LinkLocalID)
	}
	if d.LinkRemoteID != nil {
		l.RemoteLinkId = uint64(*d.LinkRemoteID)
	}
	if d.InterfaceAddrIPv4 != nil {
		l.LocalAddress = d.InterfaceAddrIPv4.String()
	}
	if d.InterfaceAddrIPv6 != nil {
		l.LocalAddress = d.InterfaceAddrIPv6.String()
	}
	if d.NeighborAddrIPv4 != nil {
		l.RemoteAddress = d.NeighborAddrIPv4.String()
	}
	if d.NeighborAddrIPv6 != nil {
		l.RemoteAddress = d.NeighborAddrIPv6.String()
	}
	if attrs != nil {
		a := attrs.Link
		if a.IGPMetric != nil {
			l.IgpMetric = uint64(*a.IGPMetric)
		}
		if a.DefaultTEMetric != nil {
			l.TeMetric = uint64(*a.DefaultTEMetric)
		}
		if a.UnidirectionalLinkDelay != nil {
			l.DelayMicroseconds = uint64(a.UnidirectionalLinkDelay.Delay)
		}
		if a.Bandwidth != nil {
			l.MaxBandwidthBytesPerSecond = float64(*a.Bandwidth)
		}
		if a.ReservableBandwidth != nil {
			l.ReservableBandwidthBytesPerSecond = float64(*a.ReservableBandwidth)
		}
		if a.UnreservedBandwidth != nil {
			for _, v := range a.UnreservedBandwidth {
				l.UnreservedBandwidthBytesPerSecond = append(l.UnreservedBandwidthBytesPerSecond, float64(v))
			}
		}
		if a.AdminGroup != nil {
			l.AdminGroups = []uint32{*a.AdminGroup}
		}
		if a.Srlgs != nil {
			l.Srlgs = append(l.Srlgs, (*a.Srlgs)...)
		}
		if a.SrAdjacencySID != nil {
			l.AdjacencySids = []uint32{*a.SrAdjacencySID}
		}
	}
	return l
}
func firstMT(values map[uint16]struct{}) uint16 {
	if len(values) == 0 {
		return 0
	}
	ids := make([]int, 0, len(values))
	for v := range values {
		ids = append(ids, int(v))
	}
	sort.Ints(ids)
	return uint16(ids[0])
}
func extractAttributes(attrs []packet.PathAttributeInterface) *packet.LsAttribute {
	for _, attr := range attrs {
		if ls, ok := attr.(*packet.PathAttributeLs); ok {
			return ls.Extract()
		}
	}
	return nil
}
func decodedAttributeTLVs(attrs []packet.PathAttributeInterface) []*bgplsv1.RawTlv {
	var out []*bgplsv1.RawTlv
	for _, attr := range attrs {
		ls, ok := attr.(*packet.PathAttributeLs)
		if !ok {
			continue
		}
		for _, tlv := range ls.TLVs {
			header := tlv.GetLsTLV()
			var payload []byte
			if wire, err := tlv.Serialize(); err == nil && len(wire) >= 4 {
				payload = wire[4:]
			}
			out = append(out, &bgplsv1.RawTlv{Type: uint32(header.Type), Value: tlvText(tlv, payload), Registry: "bgp-ls-attribute"})
		}
	}
	return out
}

func formatAddr(addr netip.Addr) string {
	if !addr.IsValid() {
		return ""
	}
	return addr.String()
}

func formatAddrPtr(addr *netip.Addr) string {
	if addr == nil {
		return ""
	}
	return formatAddr(*addr)
}

func areaID(protocol bgplsv1.Protocol, d *packet.LsNodeDescriptor, attrs *packet.LsAttribute) string {
	switch protocol {
	case bgplsv1.Protocol_PROTOCOL_OSPFV2, bgplsv1.Protocol_PROTOCOL_OSPFV3:
		return fmt.Sprintf("%08x", d.OspfAreaID)
	case bgplsv1.Protocol_PROTOCOL_ISIS_LEVEL_1, bgplsv1.Protocol_PROTOCOL_ISIS_LEVEL_2:
		if attrs != nil && attrs.Node.IsisArea != nil {
			return formatISISArea(*attrs.Node.IsisArea)
		}
	}
	return ""
}

func formatISISArea(area []byte) string {
	if len(area) == 0 {
		return ""
	}
	parts := make([]string, 0, (len(area)+1)/2)
	parts = append(parts, fmt.Sprintf("%02x", area[0]))
	for i := 1; i < len(area); i += 2 {
		if i+1 < len(area) {
			parts = append(parts, fmt.Sprintf("%02x%02x", area[i], area[i+1]))
			continue
		}
		parts = append(parts, fmt.Sprintf("%02x", area[i]))
	}
	return strings.Join(parts, ".")
}

func tlvText(tlv packet.LsTLVInterface, value []byte) string {
	switch v := tlv.(type) {
	case *packet.LsTLVNodeName:
		return v.Name
	case *packet.LsTLVIsisArea:
		return formatISISArea(v.Area)
	case *packet.LsTLVLocalIPv4RouterID:
		return formatAddr(v.IP)
	case *packet.LsTLVLocalIPv6RouterID:
		return formatAddr(v.IP)
	case *packet.LsTLVRemoteIPv4RouterID:
		return formatAddr(v.IP)
	case *packet.LsTLVRemoteIPv6RouterID:
		return formatAddr(v.IP)
	}
	if len(value) > 0 && utf8.Valid(value) && isPrintableASCII(value) {
		return string(value)
	}
	return hex.EncodeToString(value)
}

func isPrintableASCII(value []byte) bool {
	for _, b := range value {
		if b > unicode.MaxASCII || !unicode.IsPrint(rune(b)) {
			return false
		}
	}
	return true
}
