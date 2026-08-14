package gnmi

import (
	"testing"

	"github.com/openconfig/gnmi/proto/gnmi"
)

func TestMapPortSpeed(t *testing.T) {
	if got := mapPortSpeed("100G"); got != 100_000_000_000 {
		t.Fatalf("100G = %d", got)
	}
	if got := mapPortSpeed("srl_nokia-if-ethernet:10G"); got != 10_000_000_000 {
		t.Fatalf("prefixed 10G = %d", got)
	}
	if got := mapPortSpeed("unknown"); got != 0 {
		t.Fatalf("unknown should stay 0, got %d", got)
	}
}

func TestPathStringExtractsInterface(t *testing.T) {
	p := &gnmi.Path{Elem: []*gnmi.PathElem{
		{Name: "interface", Key: map[string]string{"name": "ethernet-1/1"}},
		{Name: "statistics"},
		{Name: "in-octets"},
	}}
	iface, leaf, _ := pathString(p)
	if iface != "ethernet-1/1" || leaf != "in-octets" {
		t.Fatalf("iface=%q leaf=%q", iface, leaf)
	}
}

func TestStripPrefix(t *testing.T) {
	if got := stripPrefix("10.1.79.2/30"); got != "10.1.79.2" {
		t.Fatalf("got %q", got)
	}
}
