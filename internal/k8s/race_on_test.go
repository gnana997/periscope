//go:build race

package k8s

// raceBuild is true when the test binary is compiled with -race. Used
// to skip end-to-end tests that exercise paths through
// rancher/remotedialer's connection close-out, which races
// internally against in-flight Writes (connection.go:49 vs :82). The
// race only triggers under aggressive test-teardown patterns; in
// production the tunnel server's close handling happens sequentially
// w.r.t. exec, so this is purely a test-time artifact.
const raceBuild = true
