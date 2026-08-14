package snmp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/bgpls/bgpls/internal/utilization"
	"github.com/gosnmp/gosnmp"
)

const (
	ifNameOID           = ".1.3.6.1.2.1.31.1.1.1.1"
	ifHCInOctetsOID     = ".1.3.6.1.2.1.31.1.1.1.6"
	ifHCOutOctetsOID    = ".1.3.6.1.2.1.31.1.1.1.10"
	ifHighSpeedOID      = ".1.3.6.1.2.1.31.1.1.1.15"
	ipAddressIfIndexOID = ".1.3.6.1.2.1.4.34.1.3"
	ipAdEntIfIndexOID   = ".1.3.6.1.2.1.4.20.1.2"
)

type Config struct {
	Name           string
	Address        string
	Community      string
	SampleInterval time.Duration
	AddressRefresh time.Duration
}

type Collector struct {
	cfg Config
}

func New(cfg Config) (*Collector, error) {
	if cfg.Name == "" || cfg.Address == "" {
		return nil, fmt.Errorf("snmp collector requires name and address")
	}
	if cfg.Community == "" {
		cfg.Community = "public"
	}
	if cfg.SampleInterval <= 0 {
		cfg.SampleInterval = 10 * time.Second
	}
	if cfg.AddressRefresh <= 0 {
		cfg.AddressRefresh = 5 * time.Minute
	}
	return &Collector{cfg: cfg}, nil
}

func (c *Collector) Describe() string {
	return "snmp:" + c.cfg.Name + ":" + c.cfg.Address
}

func (c *Collector) Run(ctx context.Context, out chan<- utilization.InterfaceSample) error {
	addrs := map[int]addrSet{}
	if err := c.refreshAddrs(ctx, addrs); err != nil {
		slog.Warn("SNMP address walk failed", "target", c.cfg.Name, "error", err)
	}
	ticker := time.NewTicker(c.cfg.SampleInterval)
	defer ticker.Stop()
	refresh := time.NewTicker(c.cfg.AddressRefresh)
	defer refresh.Stop()
	c.poll(ctx, addrs, out)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-refresh.C:
			if err := c.refreshAddrs(ctx, addrs); err != nil {
				slog.Warn("SNMP address walk failed", "target", c.cfg.Name, "error", err)
			}
		case <-ticker.C:
			c.poll(ctx, addrs, out)
		}
	}
}

type addrSet struct {
	ipv4 []string
	ipv6 []string
}

type ifRow struct {
	name      string
	inOctets  uint64
	outOctets uint64
	speedMbps uint64
}

func (c *Collector) client() (*gosnmp.GoSNMP, error) {
	host, port := splitAddr(c.cfg.Address)
	client := &gosnmp.GoSNMP{
		Target:    utilization.PreferIPv4Host(host),
		Port:      port,
		Community: c.cfg.Community,
		Version:   gosnmp.Version2c,
		Timeout:   5 * time.Second,
		Retries:   1,
	}
	if err := client.Connect(); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *Collector) poll(ctx context.Context, addrs map[int]addrSet, out chan<- utilization.InterfaceSample) {
	if err := ctx.Err(); err != nil {
		return
	}
	client, err := c.client()
	if err != nil {
		slog.Warn("SNMP connect failed", "target", c.cfg.Name, "error", err)
		return
	}
	defer client.Conn.Close()
	rows := map[int]*ifRow{}
	walk := func(oid string, apply func(idx int, pdu gosnmp.SnmpPDU)) {
		_ = client.BulkWalk(oid, func(pdu gosnmp.SnmpPDU) error {
			idx, ok := oidIndex(oid, pdu.Name)
			if !ok {
				return nil
			}
			apply(idx, pdu)
			return nil
		})
	}
	walk(ifNameOID, func(idx int, pdu gosnmp.SnmpPDU) {
		row := rows[idx]
		if row == nil {
			row = &ifRow{}
			rows[idx] = row
		}
		row.name = pduString(pdu)
	})
	walk(ifHCInOctetsOID, func(idx int, pdu gosnmp.SnmpPDU) {
		row := rows[idx]
		if row == nil {
			row = &ifRow{}
			rows[idx] = row
		}
		row.inOctets = pduUint(pdu)
	})
	walk(ifHCOutOctetsOID, func(idx int, pdu gosnmp.SnmpPDU) {
		row := rows[idx]
		if row == nil {
			row = &ifRow{}
			rows[idx] = row
		}
		row.outOctets = pduUint(pdu)
	})
	walk(ifHighSpeedOID, func(idx int, pdu gosnmp.SnmpPDU) {
		row := rows[idx]
		if row == nil {
			row = &ifRow{}
			rows[idx] = row
		}
		row.speedMbps = pduUint(pdu)
	})
	now := time.Now().UTC()
	for idx, row := range rows {
		if skipInterface(row.name) {
			continue
		}
		set := addrs[idx]
		if len(set.ipv4) == 0 && len(set.ipv6) == 0 {
			continue
		}
		sample := utilization.InterfaceSample{
			Device:        c.cfg.Name,
			InterfaceName: row.name,
			IPv4Addrs:     append([]string(nil), set.ipv4...),
			IPv6Addrs:     append([]string(nil), set.ipv6...),
			SpeedBps:      row.speedMbps * 1_000_000,
			InOctets:      row.inOctets,
			OutOctets:     row.outOctets,
			Timestamp:     now,
		}
		select {
		case <-ctx.Done():
			return
		case out <- sample:
		}
	}
}

func (c *Collector) refreshAddrs(ctx context.Context, addrs map[int]addrSet) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	defer client.Conn.Close()
	next := map[int]addrSet{}
	err = client.BulkWalk(ipAddressIfIndexOID, func(pdu gosnmp.SnmpPDU) error {
		idx := int(pduUint(pdu))
		ip, v6 := parseIPAddressOID(pdu.Name)
		if ip == "" || idx == 0 {
			return nil
		}
		set := next[idx]
		if v6 {
			set.ipv6 = appendUnique(set.ipv6, ip)
		} else {
			set.ipv4 = appendUnique(set.ipv4, ip)
		}
		next[idx] = set
		return nil
	})
	if err != nil || len(next) == 0 {
		_ = client.BulkWalk(ipAdEntIfIndexOID, func(pdu gosnmp.SnmpPDU) error {
			idx := int(pduUint(pdu))
			ip := parseLegacyIP(pdu.Name)
			if ip == "" || idx == 0 {
				return nil
			}
			set := next[idx]
			set.ipv4 = appendUnique(set.ipv4, ip)
			next[idx] = set
			return nil
		})
	}
	for k := range addrs {
		delete(addrs, k)
	}
	for k, v := range next {
		addrs[k] = v
	}
	return nil
}

func splitAddr(addr string) (string, uint16) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 161
	}
	n, _ := strconv.Atoi(port)
	if n <= 0 {
		n = 161
	}
	return host, uint16(n)
}

func oidIndex(prefix, name string) (int, bool) {
	name = strings.TrimPrefix(name, ".")
	prefix = strings.TrimPrefix(prefix, ".")
	if !strings.HasPrefix(name, prefix+".") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(name, prefix+"."))
	return n, err == nil
}

func pduUint(pdu gosnmp.SnmpPDU) uint64 {
	switch v := pdu.Value.(type) {
	case uint:
		return uint64(v)
	case uint32:
		return uint64(v)
	case uint64:
		return v
	case int:
		if v < 0 {
			return 0
		}
		return uint64(v)
	case int64:
		if v < 0 {
			return 0
		}
		return uint64(v)
	case []byte:
		n, _ := strconv.ParseUint(string(v), 10, 64)
		return n
	case string:
		n, _ := strconv.ParseUint(v, 10, 64)
		return n
	default:
		n, _ := strconv.ParseUint(fmt.Sprint(v), 10, 64)
		return n
	}
}

func pduString(pdu gosnmp.SnmpPDU) string {
	switch v := pdu.Value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

func skipInterface(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || n == "lo" || strings.HasPrefix(n, "lo.") || strings.HasPrefix(n, "lo:") {
		return true
	}
	if strings.HasPrefix(n, "docker") || strings.HasPrefix(n, "dummy") || strings.HasPrefix(n, "sit") {
		return true
	}
	return false
}

func appendUnique(dst []string, v string) []string {
	for _, e := range dst {
		if e == v {
			return dst
		}
	}
	return append(dst, v)
}

// parseIPAddressOID decodes ipAddressIfIndex index: type, length, address octets.
func parseIPAddressOID(name string) (addr string, ipv6 bool) {
	const prefix = "1.3.6.1.2.1.4.34.1.3."
	name = strings.TrimPrefix(name, ".")
	if !strings.HasPrefix(name, prefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(name, prefix), ".")
	if len(parts) < 3 {
		return "", false
	}
	typ, _ := strconv.Atoi(parts[0])
	length, _ := strconv.Atoi(parts[1])
	rest := parts[2:]
	if length <= 0 || len(rest) < length {
		return "", false
	}
	octets := make([]byte, length)
	for i := 0; i < length; i++ {
		n, _ := strconv.Atoi(rest[i])
		octets[i] = byte(n)
	}
	switch typ {
	case 1: // ipv4
		if length >= 4 {
			return net.IP(octets[:4]).String(), false
		}
	case 2: // ipv6
		if length >= 16 {
			return net.IP(octets[:16]).String(), true
		}
	}
	return "", false
}

func parseLegacyIP(name string) string {
	const prefix = "1.3.6.1.2.1.4.20.1.2."
	name = strings.TrimPrefix(name, ".")
	if !strings.HasPrefix(name, prefix) {
		return ""
	}
	ip := net.ParseIP(strings.TrimPrefix(name, prefix))
	if ip == nil {
		return ""
	}
	return ip.String()
}
