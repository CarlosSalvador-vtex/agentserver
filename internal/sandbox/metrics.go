package sandbox

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Sprint 2 PR-2 (improvements.md #6). Composition-resolve latency lives in the
// sandbox package so the package can self-instrument without depending on
// internal/server. Both packages register against the global DefaultRegisterer
// that the /metrics handler scrapes.

var compositionResolveDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "sandbox_composition_resolve_duration_seconds",
		Help:    "Latency of sandbox.Manager.ResolveComposition, labeled by outcome.",
		Buckets: []float64{0.005, 0.025, 0.1, 0.5, 1, 5},
	},
	[]string{"result"},
)

func observeCompositionResolve(result string, seconds float64) {
	compositionResolveDuration.WithLabelValues(result).Observe(seconds)
}
