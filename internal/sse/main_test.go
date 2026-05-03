package sse

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain catches goroutine leaks across the package's tests.
// sse.Writer is single-goroutine by design (no spawned goroutines),
// so any leak detected here would be from a future regression.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
