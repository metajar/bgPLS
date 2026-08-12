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
}
