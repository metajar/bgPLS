package utilization

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	samplesReceived = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "utilcol",
		Name:      "samples_received_total",
		Help:      "Raw interface samples received from device collectors.",
	}, []string{"target"})
	rateDiscards = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "utilcol",
		Name:      "rate_discards_total",
		Help:      "Rate-compute intervals discarded.",
	}, []string{"reason"})
	reportBatches = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "utilcol",
		Name:      "report_batches_total",
		Help:      "Utilization report batches sent to bgPLS.",
	}, []string{"result"})
	targetUp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "utilcol",
		Name:      "target_up",
		Help:      "Whether a utilization collector target is currently running.",
	}, []string{"target"})
)

func init() {
	prometheus.MustRegister(samplesReceived, rateDiscards, reportBatches, targetUp)
}

// Registry runs N collectors and fans samples into one channel.
type Registry struct {
	collectors []Collector
}

func NewRegistry(collectors ...Collector) *Registry {
	return &Registry{collectors: collectors}
}

func (r *Registry) Run(ctx context.Context, out chan<- InterfaceSample) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(r.collectors))
	for _, c := range r.collectors {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := c.Describe()
			targetUp.WithLabelValues(name).Set(1)
			defer targetUp.WithLabelValues(name).Set(0)
			if err := c.Run(ctx, out); err != nil && ctx.Err() == nil {
				slog.Error("collector failed permanently", "collector", name, "error", err)
				errCh <- fmt.Errorf("%s: %w", name, err)
			}
		}()
	}
	go func() {
		wg.Wait()
		close(errCh)
	}()
	var first error
	for err := range errCh {
		if first == nil {
			first = err
		}
	}
	return first
}

func ObserveSample(target string) { samplesReceived.WithLabelValues(target).Inc() }
func ObserveDiscard(reason string) {
	if reason == "" {
		reason = "unknown"
	}
	rateDiscards.WithLabelValues(reason).Inc()
}
func ObserveBatch(result string) { reportBatches.WithLabelValues(result).Inc() }
