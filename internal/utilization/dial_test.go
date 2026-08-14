package utilization

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestPreferIPv4HostLiteral(t *testing.T) {
	if got := PreferIPv4Host("172.20.20.11"); got != "172.20.20.11" {
		t.Fatalf("got %q", got)
	}
}

func TestPreferIPv4HostLocalhost(t *testing.T) {
	got := PreferIPv4Host("localhost")
	ip := net.ParseIP(got)
	if ip == nil {
		t.Fatalf("expected an IP, got %q", got)
	}
	if ip.To4() == nil && lookupHasIPv4(t, "localhost") {
		t.Fatalf("localhost resolved to IPv6 %q while IPv4 exists", got)
	}
}

func TestDialPreferIPv4Localhost(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Close()
		}
	}()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := DialPreferIPv4(ctx, "tcp", net.JoinHostPort("localhost", port))
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func lookupHasIPv4(t *testing.T, host string) bool {
	t.Helper()
	ips, err := net.LookupIP(host)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return true
		}
	}
	return false
}
