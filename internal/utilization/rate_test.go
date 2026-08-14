package utilization

import (
	"math"
	"testing"
	"time"
)

func TestComputeRatesHappyPath(t *testing.T) {
	t0 := time.Unix(0, 0).UTC()
	prev := CounterState{InOctets: 1000, OutOctets: 2000, Timestamp: t0}
	curr := CounterState{InOctets: 1000 + 1250, OutOctets: 2000 + 2500, Timestamp: t0.Add(10 * time.Second)}
	got := ComputeRates(prev, curr)
	if !got.OK || got.InBps != 1000 || got.OutBps != 2000 {
		t.Fatalf("got %+v", got)
	}
}

func TestComputeRatesWrap(t *testing.T) {
	t0 := time.Unix(0, 0).UTC()
	prev := CounterState{InOctets: math.MaxUint64 - 99, OutOctets: math.MaxUint64 - 49, Timestamp: t0}
	curr := CounterState{InOctets: 100, OutOctets: 50, Timestamp: t0.Add(time.Second)}
	got := ComputeRates(prev, curr)
	if !got.OK {
		t.Fatalf("64-bit wrap should continue the series: %+v", got)
	}
	if got.InBps != 200*8 || got.OutBps != 100*8 {
		t.Fatalf("wrap rates = %+v", got)
	}
}

func TestComputeRatesReboot(t *testing.T) {
	t0 := time.Unix(0, 0).UTC()
	prev := CounterState{InOctets: 1 << 60, OutOctets: 1 << 60, Timestamp: t0}
	curr := CounterState{InOctets: 10, OutOctets: 10, Timestamp: t0.Add(time.Second)}
	got := ComputeRates(prev, curr)
	if got.OK || got.Reason != "reboot" {
		t.Fatalf("expected reboot discard, got %+v", got)
	}
}

func TestComputeRatesNonIncreasingTime(t *testing.T) {
	ts := time.Unix(10, 0).UTC()
	got := ComputeRates(CounterState{Timestamp: ts}, CounterState{InOctets: 1, Timestamp: ts})
	if got.OK || got.Reason != "interval" {
		t.Fatalf("got %+v", got)
	}
}

func TestUtilizationUnknownSpeed(t *testing.T) {
	ratio, available, ok := UtilizationRatio(1_000_000, 2_000_000, 0)
	if ok || ratio != 0 || available != 0 {
		t.Fatalf("unknown speed must not be guessed: %v %d %v", ratio, available, ok)
	}
}

func TestUtilizationKnown(t *testing.T) {
	ratio, available, ok := UtilizationRatio(1_000_000, 4_000_000, 10_000_000)
	if !ok || available != 6_000_000 || ratio < 0.39 || ratio > 0.41 {
		t.Fatalf("ratio=%v available=%d ok=%v", ratio, available, ok)
	}
}

func TestTrackerSeedThenRateThenRebootReseed(t *testing.T) {
	tr := NewTracker()
	t0 := time.Unix(0, 0).UTC()
	first := tr.Observe(InterfaceSample{Device: "r1", InterfaceName: "eth1", InOctets: 100, Timestamp: t0})
	if first.OK || first.Reason != "seed" {
		t.Fatalf("first = %+v", first)
	}
	second := tr.Observe(InterfaceSample{Device: "r1", InterfaceName: "eth1", InOctets: 100 + 125, OutOctets: 250, Timestamp: t0.Add(time.Second)})
	if !second.OK || second.InBps != 1000 {
		t.Fatalf("second = %+v", second)
	}
	reboot := tr.Observe(InterfaceSample{Device: "r1", InterfaceName: "eth1", InOctets: 1, Timestamp: t0.Add(2 * time.Second)})
	if reboot.OK || reboot.Reason != "reboot" {
		t.Fatalf("reboot = %+v", reboot)
	}
	after := tr.Observe(InterfaceSample{Device: "r1", InterfaceName: "eth1", InOctets: 1 + 125, Timestamp: t0.Add(3 * time.Second)})
	if !after.OK || after.InBps != 1000 {
		t.Fatalf("reseeded = %+v", after)
	}
}
