package bgp

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"github.com/osrg/gobgp/v4/pkg/apiutil"
	packet "github.com/osrg/gobgp/v4/pkg/packet/bgp"
)

func TestStableNodeTranslation(t *testing.T) {
	descriptor := &packet.LsNodeDescriptor{Asn: 65000, BGPLsID: 7, OspfAreaID: 1, IGPRouterID: "192.0.2.1"}
	first := nodeID("core", packet.LS_PROTOCOL_OSPF_V2, 42, descriptor)
	second := nodeID("core", packet.LS_PROTOCOL_OSPF_V2, 42, descriptor)
	if first != second || len(first) != 32 {
		t.Fatalf("unstable node ID: %q %q", first, second)
	}
	name := "r1"
	algorithms := []byte{0, 1}
	node := nodeFrom(descriptor, first, "core", bgplsv1.Protocol_PROTOCOL_OSPFV2, &packet.LsAttribute{Node: packet.LsAttributeNode{Name: &name, SrAlgorithms: &algorithms}}, nil)
	if node.Name != "r1" || len(node.Algorithms) != 2 || node.AutonomousSystem != 65000 {
		t.Fatalf("unexpected node translation: %+v", node)
	}
	if node.BgpRouterId != "" {
		t.Fatalf("unset BGP router ID should be empty, got %q", node.BgpRouterId)
	}
	if node.AreaId != "00000001" {
		t.Fatalf("OSPF area ID = %q", node.AreaId)
	}
}

func TestISISNodeTranslationUsesAttributeRouterIDs(t *testing.T) {
	descriptor := &packet.LsNodeDescriptor{IGPRouterID: "0000.0000.0001"}
	name := "r1"
	area := []byte{0x49, 0x00, 0x01}
	ipv4 := netip.MustParseAddr("10.255.0.1")
	ipv6 := netip.MustParseAddr("fd00:2:55::1")
	node := nodeFrom(descriptor, "n", "core", bgplsv1.Protocol_PROTOCOL_ISIS_LEVEL_2, &packet.LsAttribute{Node: packet.LsAttributeNode{Name: &name, IsisArea: &area, LocalRouterID: &ipv4, LocalRouterIDv6: &ipv6}}, nil)
	if node.BgpRouterId != "" {
		t.Fatalf("ISIS BGP router ID should be empty, got %q", node.BgpRouterId)
	}
	if node.AreaId != "49.0001" || node.Ipv4RouterId != "10.255.0.1" || node.Ipv6RouterId != "fd00:2:55::1" {
		t.Fatalf("unexpected ISIS node translation: %+v", node)
	}
}

func TestDecodedAttributeTLVsAreHumanReadable(t *testing.T) {
	name := "r1"
	area := []byte{0x49, 0x00, 0x01}
	ipv4 := netip.MustParseAddr("10.255.0.1")
	ipv6 := netip.MustParseAddr("fd00:2:55::1")
	decoded := decodedAttributeTLVs([]packet.PathAttributeInterface{&packet.PathAttributeLs{TLVs: []packet.LsTLVInterface{
		packet.NewLsTLVNodeName(&name),
		packet.NewLsTLVIsisArea(&area),
		packet.NewLsTLVLocalIPv4RouterID(&ipv4),
		packet.NewLsTLVLocalIPv6RouterID(&ipv6),
	}}})
	got := map[uint32]string{}
	for _, tlv := range decoded {
		got[tlv.Type] = tlv.Value
	}
	want := map[uint32]string{1026: "r1", 1027: "49.0001", 1028: "10.255.0.1", 1029: "fd00:2:55::1"}
	for typ, value := range want {
		if got[typ] != value {
			t.Fatalf("TLV %d = %q, want %q", typ, got[typ], value)
		}
	}
}

func TestFormatAddrOmitsInvalidIP(t *testing.T) {
	if got := formatAddr(netip.Addr{}); got != "" {
		t.Fatalf("zero address = %q", got)
	}
	if got := formatAddr(netip.MustParseAddr("192.0.2.1")); got != "192.0.2.1" {
		t.Fatalf("valid address = %q", got)
	}
	descriptor := &packet.LsNodeDescriptor{BGPRouterID: netip.MustParseAddr("192.0.2.10")}
	node := nodeFrom(descriptor, "n", "core", bgplsv1.Protocol_PROTOCOL_BGP, nil, nil)
	if node.BgpRouterId != "192.0.2.10" {
		t.Fatalf("BGP router ID = %q", node.BgpRouterId)
	}
}

func TestMalformedDecodedPathReturnsError(t *testing.T) {
	collector := &Collector{}
	path := &apiutil.Path{Nlri: &packet.LsAddrPrefix{NLRI: &packet.LsNodeNLRI{}}, Family: packet.RF_LS}
	err := collector.safeHandlePath(context.Background(), &bgplsv1.Peer{Id: "p", DomainId: "d"}, path)
	if err == nil {
		t.Fatal("malformed decoded path was accepted")
	}
}
func TestUnsupportedNLRIReturnsSentinel(t *testing.T) {
	collector := &Collector{}
	path := &apiutil.Path{Nlri: &packet.LsAddrPrefix{NLRI: fakeNLRI{}}, Family: packet.RF_LS}
	err := collector.safeHandlePath(context.Background(), &bgplsv1.Peer{Id: "p", DomainId: "d"}, path)
	if !errors.Is(err, errUnsupportedNLRI) {
		t.Fatalf("error = %v", err)
	}
}

func TestUnsupportedOptionalTLVDoesNotRejectSupportedAttributes(t *testing.T) {
	attribute := &packet.PathAttributeLs{}
	wire := []byte{0x80, 0x29, 0x0c, 0xde, 0xad, 0x00, 0x01, 0xff, 0x04, 0x02, 0x00, 0x03, 'r', 't', 'r'}
	if err := attribute.DecodeFromBytes(wire); err != nil {
		t.Fatalf("attribute containing an unsupported optional TLV was rejected: %v", err)
	}
	decoded := attribute.Extract()
	if decoded.Node.Name == nil || *decoded.Node.Name != "rtr" {
		t.Fatalf("supported TLV after unsupported extension was lost: %+v", decoded.Node)
	}
}

type fakeNLRI struct{}

func (fakeNLRI) DecodeFromBytes([]byte) error { return nil }
func (fakeNLRI) Serialize() ([]byte, error)   { return nil, nil }
func (fakeNLRI) Len() int                     { return 0 }
func (fakeNLRI) Type() packet.LsNLRIType      { return 65535 }
func (fakeNLRI) String() string               { return "unsupported" }

func TestLinkTETranslation(t *testing.T) {
	local, remote := uint32(1), uint32(2)
	localAddr := netip.MustParseAddr("192.0.2.1")
	remoteAddr := netip.MustParseAddr("192.0.2.2")
	igp, te, delay := uint32(10), uint32(20), uint32(30)
	bandwidth := float32(1000)
	link := linkFrom("l", "core", "a", "b", &packet.LsLinkDescriptor{LinkLocalID: &local, LinkRemoteID: &remote, InterfaceAddrIPv4: &localAddr, NeighborAddrIPv4: &remoteAddr}, &packet.LsAttribute{Link: packet.LsAttributeLink{IGPMetric: &igp, DefaultTEMetric: &te, UnidirectionalLinkDelay: &packet.LsUnidirectionalLinkDelay{Delay: delay}, ReservableBandwidth: &bandwidth}}, nil)
	if link.IgpMetric != 10 || link.TeMetric != 20 || link.DelayMicroseconds != 30 || link.ReservableBandwidthBytesPerSecond != 1000 {
		t.Fatalf("unexpected link translation: %+v", link)
	}
	if link.LocalIpv4Address != "192.0.2.1" || link.RemoteIpv4Address != "192.0.2.2" || link.LocalAddress != "192.0.2.1" {
		t.Fatalf("unexpected IPv4 link addresses: %+v", link)
	}
}

func TestLinkDualStackAddresses(t *testing.T) {
	localAddr := netip.MustParseAddr("10.1.13.2")
	remoteAddr := netip.MustParseAddr("10.1.13.1")
	localV6 := netip.MustParseAddr("fd00:1:13::2")
	remoteV6 := netip.MustParseAddr("fd00:1:13::1")
	link := linkFrom("l", "core", "a", "b", &packet.LsLinkDescriptor{InterfaceAddrIPv4: &localAddr, NeighborAddrIPv4: &remoteAddr, InterfaceAddrIPv6: &localV6, NeighborAddrIPv6: &remoteV6}, nil, nil)
	if link.LocalIpv4Address != "10.1.13.2" || link.RemoteIpv4Address != "10.1.13.1" {
		t.Fatalf("IPv4 addresses were lost: %+v", link)
	}
	if link.LocalIpv6Address != "fd00:1:13::2" || link.RemoteIpv6Address != "fd00:1:13::1" {
		t.Fatalf("IPv6 addresses were lost: %+v", link)
	}
	if link.LocalAddress != "10.1.13.2" || link.RemoteAddress != "10.1.13.1" {
		t.Fatalf("legacy address fields should prefer IPv4: %+v", link)
	}
}
