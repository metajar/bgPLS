package snmp

import "testing"

func TestParseIPAddressOID(t *testing.T) {
	ip, v6 := parseIPAddressOID(".1.3.6.1.2.1.4.34.1.3.1.4.10.1.79.2")
	if v6 || ip != "10.1.79.2" {
		t.Fatalf("ipv4 = %q v6=%v", ip, v6)
	}
	ip, v6 = parseIPAddressOID("1.3.6.1.2.1.4.34.1.3.2.16.253.0.1.18.0.0.0.0.0.0.0.0.0.0.0.1")
	if !v6 || ip != "fd00:112::1" {
		t.Fatalf("ipv6 = %q v6=%v", ip, v6)
	}
}

func TestParseLegacyIP(t *testing.T) {
	if got := parseLegacyIP(".1.3.6.1.2.1.4.20.1.2.10.1.12.1"); got != "10.1.12.1" {
		t.Fatalf("got %q", got)
	}
}

func TestSkipLoopback(t *testing.T) {
	if !skipInterface("lo") || skipInterface("eth1") {
		t.Fatal("loopback skip")
	}
}

func TestOidIndex(t *testing.T) {
	idx, ok := oidIndex(".1.3.6.1.2.1.31.1.1.1.1", ".1.3.6.1.2.1.31.1.1.1.1.12")
	if !ok || idx != 12 {
		t.Fatalf("idx=%d ok=%v", idx, ok)
	}
}
