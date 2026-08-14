package utilization

import (
	"context"
	"time"
)

// InterfaceSample is one observation of one interface's counters.
// Counters are cumulative octet counts as reported by the device;
// rate conversion happens centrally in rate.go, not in backends.
type InterfaceSample struct {
	Device        string
	InterfaceName string
	IPv4Addrs     []string
	IPv6Addrs     []string
	SpeedBps      uint64
	InOctets      uint64
	OutOctets     uint64
	Timestamp     time.Time
}

// Collector streams interface samples for one device (or one device group).
type Collector interface {
	// Run blocks until ctx is cancelled, sending samples on out.
	// Implementations own their reconnect/retry loops; a returned error
	// means permanent failure (bad config), not a transient one.
	Run(ctx context.Context, out chan<- InterfaceSample) error
	// Describe returns a human-readable identity for logs/metrics,
	// e.g. "gnmi:srl1:57401" or "snmp:r3:161".
	Describe() string
}
