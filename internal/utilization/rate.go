package utilization

import (
	"sync"
	"time"
)

const maxPlausibleBps = 10e12 // 10 Tbps; larger deltas are treated as a counter reset.

// SampleKey identifies a counter series.
type SampleKey struct {
	Device        string
	InterfaceName string
}

// CounterState is the last accepted raw sample for a series.
type CounterState struct {
	InOctets  uint64
	OutOctets uint64
	Timestamp time.Time
}

// RateResult is the computed bit-rate between two consecutive samples.
type RateResult struct {
	InBps  uint64
	OutBps uint64
	OK     bool
	Reason string
}

// ComputeRates converts two cumulative octet counters into bits per second.
// Unsigned subtraction handles 64-bit wrap. A delta that implies more than
// maxPlausibleBps is treated as a reboot/reset and the interval is discarded.
func ComputeRates(prev, curr CounterState) RateResult {
	dt := curr.Timestamp.Sub(prev.Timestamp).Seconds()
	if dt <= 0 {
		return RateResult{Reason: "interval"}
	}
	in, ok, reason := octetDeltaBps(prev.InOctets, curr.InOctets, dt)
	if !ok {
		return RateResult{Reason: reason}
	}
	out, ok, reason := octetDeltaBps(prev.OutOctets, curr.OutOctets, dt)
	if !ok {
		return RateResult{Reason: reason}
	}
	return RateResult{InBps: in, OutBps: out, OK: true}
}

func octetDeltaBps(prev, curr uint64, dt float64) (uint64, bool, string) {
	delta := curr - prev
	bps := float64(delta) * 8 / dt
	if bps > maxPlausibleBps {
		return 0, false, "reboot"
	}
	return uint64(bps), true, ""
}

// UtilizationRatio is max(in,out)/speed. ok is false when speed is unknown.
func UtilizationRatio(inBps, outBps, speedBps uint64) (ratio float64, available uint64, ok bool) {
	if speedBps == 0 {
		return 0, 0, false
	}
	load := inBps
	if outBps > load {
		load = outBps
	}
	if load >= speedBps {
		return float64(load) / float64(speedBps), 0, true
	}
	return float64(load) / float64(speedBps), speedBps - load, true
}

// Tracker remembers the previous sample per interface and yields rates.
type Tracker struct {
	mu   sync.Mutex
	prev map[SampleKey]CounterState
}

func NewTracker() *Tracker {
	return &Tracker{prev: map[SampleKey]CounterState{}}
}

// Observe records a raw sample and returns a rate when two consecutive
// samples can be compared. A discarded interval reseeds the series.
func (t *Tracker) Observe(sample InterfaceSample) RateResult {
	key := SampleKey{Device: sample.Device, InterfaceName: sample.InterfaceName}
	curr := CounterState{InOctets: sample.InOctets, OutOctets: sample.OutOctets, Timestamp: sample.Timestamp}
	t.mu.Lock()
	defer t.mu.Unlock()
	prev, ok := t.prev[key]
	t.prev[key] = curr
	if !ok {
		return RateResult{Reason: "seed"}
	}
	result := ComputeRates(prev, curr)
	if !result.OK {
		return result
	}
	return result
}
