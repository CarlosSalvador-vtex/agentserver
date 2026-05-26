package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// TestMetricsEndpointExposesPlaygroundCounters confirms that the /metrics
// scrape returns counter + histogram names registered in playground_metrics.go
// and sandbox/metrics.go (HELP lines are emitted on first collection even
// before any sample is recorded).
func TestMetricsEndpointExposesPlaygroundCounters(t *testing.T) {
	// Touch every metric so its HELP line is emitted by the default registry.
	RecordDraftAction("skill", "created")
	RecordPromoteResult("skill", "ok")
	RecordTestSandboxResult("ok")
	ObserveDryRun("skill", "ok", 0.1)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	promhttp.Handler().ServeHTTP(rr, req)

	body := rr.Body.String()
	wantNames := []string{
		"playground_drafts_total",
		"playground_dryrun_duration_seconds",
		"playground_promote_total",
		"playground_test_sandbox_total",
	}
	for _, name := range wantNames {
		if !strings.Contains(body, name) {
			t.Errorf("/metrics output missing %q", name)
		}
	}
}
