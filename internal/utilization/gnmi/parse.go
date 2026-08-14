package gnmi

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/openconfig/gnmi/proto/gnmi"
)

var portSpeedBps = map[string]uint64{
	"1G": 1_000_000_000, "SPEED_1GB": 1_000_000_000, "1000": 1_000_000_000,
	"10G": 10_000_000_000, "SPEED_10GB": 10_000_000_000, "10000": 10_000_000_000,
	"25G": 25_000_000_000, "SPEED_25GB": 25_000_000_000,
	"40G": 40_000_000_000, "SPEED_40GB": 40_000_000_000,
	"50G": 50_000_000_000, "SPEED_50GB": 50_000_000_000,
	"100G": 100_000_000_000, "SPEED_100GB": 100_000_000_000,
	"200G": 200_000_000_000, "SPEED_200GB": 200_000_000_000,
	"400G": 400_000_000_000, "SPEED_400GB": 400_000_000_000,
	"800G": 800_000_000_000, "SPEED_800GB": 800_000_000_000,
}

type ifaceState struct {
	name      string
	inOctets  uint64
	outOctets uint64
	speedBps  uint64
	ipv4      []string
	ipv6      []string
	haveIn    bool
	haveOut   bool
}

func pathString(p *gnmi.Path) (iface, leaf string, keys map[string]string) {
	keys = map[string]string{}
	if p == nil {
		return "", "", keys
	}
	var elems []string
	for _, e := range p.Elem {
		elems = append(elems, e.Name)
		for k, v := range e.Key {
			keys[k] = v
			if k == "name" && iface == "" {
				iface = v
			}
		}
	}
	if len(elems) > 0 {
		leaf = elems[len(elems)-1]
	}
	return iface, leaf, keys
}

func parseUint(v *gnmi.TypedValue) (uint64, bool) {
	if v == nil {
		return 0, false
	}
	switch x := v.Value.(type) {
	case *gnmi.TypedValue_UintVal:
		return x.UintVal, true
	case *gnmi.TypedValue_IntVal:
		if x.IntVal < 0 {
			return 0, false
		}
		return uint64(x.IntVal), true
	case *gnmi.TypedValue_StringVal:
		n, err := strconv.ParseUint(strings.TrimSpace(x.StringVal), 10, 64)
		return n, err == nil
	case *gnmi.TypedValue_JsonIetfVal:
		var raw any
		if json.Unmarshal(x.JsonIetfVal, &raw) != nil {
			return 0, false
		}
		return anyUint(raw)
	case *gnmi.TypedValue_JsonVal:
		var raw any
		if json.Unmarshal(x.JsonVal, &raw) != nil {
			return 0, false
		}
		return anyUint(raw)
	default:
		return 0, false
	}
}

func anyUint(raw any) (uint64, bool) {
	switch n := raw.(type) {
	case float64:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case string:
		v, err := strconv.ParseUint(strings.TrimSpace(n), 10, 64)
		return v, err == nil
	default:
		return 0, false
	}
}

func parseString(v *gnmi.TypedValue) string {
	if v == nil {
		return ""
	}
	switch x := v.Value.(type) {
	case *gnmi.TypedValue_StringVal:
		return strings.Trim(x.StringVal, `"`)
	case *gnmi.TypedValue_JsonIetfVal:
		var s string
		if json.Unmarshal(x.JsonIetfVal, &s) == nil {
			return s
		}
		return strings.Trim(string(x.JsonIetfVal), `"`)
	case *gnmi.TypedValue_JsonVal:
		var s string
		if json.Unmarshal(x.JsonVal, &s) == nil {
			return s
		}
		return strings.Trim(string(x.JsonVal), `"`)
	default:
		return ""
	}
}

func mapPortSpeed(raw string) uint64 {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "srl_nokia-if-ethernet:")
	s = strings.TrimPrefix(s, "if-ethernet:")
	if bps, ok := portSpeedBps[strings.ToUpper(s)]; ok {
		return bps
	}
	if bps, ok := portSpeedBps[s]; ok {
		return bps
	}
	if n, err := strconv.ParseUint(s, 10, 64); err == nil {
		if n > 0 && n < 100000 {
			return n * 1_000_000 // Mbps
		}
		return n
	}
	return 0
}

func stripPrefix(addr string) string {
	addr = strings.TrimSpace(addr)
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		return addr[:i]
	}
	return addr
}

func addUnique(dst []string, addr string) []string {
	addr = stripPrefix(addr)
	if addr == "" {
		return dst
	}
	for _, e := range dst {
		if e == addr {
			return dst
		}
	}
	return append(dst, addr)
}
