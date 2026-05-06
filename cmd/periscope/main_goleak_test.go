package main

import (
	"testing"

	"go.uber.org/goleak"

	"github.com/gnana997/periscope/internal/k8s"
)

// TestMain catches goroutine leaks across the handler-level tests.
// resourceWatchHandler spawns a watch goroutine per request; if the
// handler return path doesn't drain streamDone correctly, leaks would
// show up here under repeated test invocations.
//
// Co-resident with main_test.go which holds the actual tests; named
// _goleak_test.go so it's obvious where the harness lives separately
// from test logic.
func TestMain(m *testing.M) {
	// helm_chart_handler_test uses httptest fixtures bound to
	// 127.0.0.1; the production SSRF guard blocks loopback. Flip
	// the test bypass for the duration of the handler suite.
	restore := k8s.SetChartFetchAllowLoopbackForTest(true)
	defer restore()

	goleak.VerifyTestMain(m,
		// AWS SDK metric publisher and k8s client-go background loops
		// can hang around after the suite exits. None are spawned by
		// Periscope's code; ignoring them is safe.
		goleak.IgnoreTopFunction("k8s.io/klog/v2.(*loggingT).flushDaemon"),
	)
}
