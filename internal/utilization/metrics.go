package utilization

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	reportsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "bgpls",
		Subsystem: "utilization",
		Name:      "reports_total",
		Help:      "Interface utilization reports accepted or rejected by the overlay.",
	}, []string{"peer_role", "result"})
	linksCorrelated = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "bgpls",
		Subsystem: "utilization",
		Name:      "links_correlated",
		Help:      "Directed links currently holding a correlated utilization sample.",
	})
	linksUncorrelated = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "bgpls",
		Subsystem: "utilization",
		Name:      "links_uncorrelated",
		Help:      "Interface reports that could not be joined to a BGP-LS link.",
	})
	uncorrelated = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "bgpls",
		Subsystem: "utilization",
		Name:      "uncorrelated",
		Help:      "Uncorrelated interface reports by reason.",
	}, []string{"reason"})
	ambiguityTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "bgpls",
		Subsystem: "utilization",
		Name:      "ambiguity_total",
		Help:      "Reports dropped because an interface address matched links from different local nodes.",
	})
)

func init() {
	prometheus.MustRegister(reportsTotal, linksCorrelated, linksUncorrelated, uncorrelated, ambiguityTotal)
}
