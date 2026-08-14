package utilization

import (
	"context"
	"fmt"
	"net"
)

// PreferIPv4Host resolves host and returns an IPv4 literal when one exists.
// Docker embedded DNS often returns AAAA first (the 3fff:: mgmt ULA), and
// gRPC/gosnmp then dial IPv6 even when the daemon only listens on IPv4.
func PreferIPv4Host(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		return host
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return host
	}
	var v6 net.IP
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip.String()
		}
		if v6 == nil {
			v6 = ip
		}
	}
	if v6 != nil {
		return v6.String()
	}
	return host
}

// DialPreferIPv4 dials network ("tcp" or "udp") trying IPv4 addresses first.
func DialPreferIPv4(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return d.DialContext(ctx, network, addr)
	}
	var v4, v6 []net.IPAddr
	for _, ip := range ips {
		if ip.IP.To4() != nil {
			v4 = append(v4, ip)
		} else {
			v6 = append(v6, ip)
		}
	}
	var last error
	for _, ip := range append(v4, v6...) {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	if last != nil {
		return nil, last
	}
	return nil, fmt.Errorf("dial %s %s: no addresses", network, addr)
}

// GRPCDialer is a grpc.WithContextDialer that prefers IPv4.
func GRPCDialer(ctx context.Context, addr string) (net.Conn, error) {
	return DialPreferIPv4(ctx, "tcp", addr)
}
