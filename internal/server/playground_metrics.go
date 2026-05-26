package server

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Sprint 2 PR-2 (improvements.md #6). Per-draft counters + per-call latency
// histograms emitted by the playground handlers and the composition resolver.
//
// Cardinality contract:
//   - `kind` label is bounded to {skill, soul}
//   - `action` label is bounded to {created, patched, archived, promoted}
//   - `result` labels are bounded to a small enum per metric
// None of these accept user input, so we cannot blow up cardinality via
// crafted requests.

var (
	playgroundDraftsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "playground_drafts_total",
			Help: "Number of playground draft state transitions, partitioned by kind and action.",
		},
		[]string{"kind", "action"},
	)

	playgroundDryRunDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "playground_dryrun_duration_seconds",
			Help:    "Latency of /api/playground/{skills,souls}/{id}/dry-run end-to-end (handler entry to response write), labeled by kind and outcome.",
			Buckets: []float64{0.5, 1, 2.5, 5, 10, 30},
		},
		[]string{"kind", "result"},
	)

	playgroundPromoteTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "playground_promote_total",
			Help: "Number of playground promote attempts, labeled by kind and result.",
		},
		[]string{"kind", "result"},
	)

	playgroundTestSandboxTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "playground_test_sandbox_total",
			Help: "Number of playground test-sandbox spawn attempts, labeled by result.",
		},
		[]string{"result"},
	)
)

// RecordDraftAction is a thin wrapper kept exported in case future packages
// (audit log, marketplace) want to emit equivalent events without taking a
// direct prometheus dependency.
func RecordDraftAction(kind, action string) {
	playgroundDraftsTotal.WithLabelValues(kind, action).Inc()
}

// RecordPromoteResult mirrors RecordDraftAction for promote.
func RecordPromoteResult(kind, result string) {
	playgroundPromoteTotal.WithLabelValues(kind, result).Inc()
}

// RecordTestSandboxResult mirrors for test-sandbox spawn.
func RecordTestSandboxResult(result string) {
	playgroundTestSandboxTotal.WithLabelValues(result).Inc()
}

// ObserveDryRun records dry-run latency with outcome label.
func ObserveDryRun(kind, result string, seconds float64) {
	playgroundDryRunDuration.WithLabelValues(kind, result).Observe(seconds)
}
