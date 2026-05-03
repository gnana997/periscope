package main

import (
	"testing"

	"go.uber.org/goleak"
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
	goleak.VerifyTestMain(m,
		// AWS SDK metric publisher and k8s client-go background loops
		// can hang around after the suite exits. None are spawned by
		// Periscope's code; ignoring them is safe.
		goleak.IgnoreTopFunction("k8s.io/klog/v2.(*loggingT).flushDaemon"),
	)
}
