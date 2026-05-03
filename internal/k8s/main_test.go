package k8s

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goleak across the whole package after every test
// suite. The watch primitives spawn goroutines (drainWatcher, the
// fake-watch goroutines in tests, sink consumers). A leak here
// signals a real production bug: the production handler relies on
// the same lifecycle to clean up between user reconnects.
//
// Per-test `defer goleak.VerifyNone(t)` is more granular but noisier
// when adding goleak retroactively. TestMain catches the steady-state
// "everything cleaned up after the suite" property, which is the
// invariant we actually care about.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// k8s.io/client-go's leader election + log flusher start
		// background goroutines on init that don't exit by VerifyTestMain
		// time. None of these are spawned by Periscope's code; ignoring
		// them is safe.
		goleak.IgnoreTopFunction("k8s.io/klog/v2.(*loggingT).flushDaemon"),
	)
}
