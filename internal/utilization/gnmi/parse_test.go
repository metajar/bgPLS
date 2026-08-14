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

func TestMergePathUsesPrefixInterface(t *testing.T) {
	prefix := &gnmi.Path{Elem: []*gnmi.PathElem{{Name: "interface", Key: map[string]string{"name": "ethernet-1/2"}}}}
	p := &gnmi.Path{Elem: []*gnmi.PathElem{{Name: "statistics"}, {Name: "in-octets"}}}
	iface, leaf, _ := mergePath(prefix, p)
	if iface != "ethernet-1/2" || leaf != "in-octets" {
		t.Fatalf("iface=%q leaf=%q", iface, leaf)
	}
}

func TestJSONMapCounters(t *testing.T) {
	v := &gnmi.TypedValue{Value: &gnmi.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"in-octets":"123","out-octets":456}`)}}
	m, ok := jsonMap(v)
	if !ok {
		t.Fatal("expected json map")
	}
	in, ok := anyUint(m["in-octets"])
	if !ok || in != 123 {
		t.Fatalf("in-octets=%d ok=%v", in, ok)
	}
	out, ok := anyUint(m["out-octets"])
	if !ok || out != 456 {
		t.Fatalf("out-octets=%d ok=%v", out, ok)
	}
}
