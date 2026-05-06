package k8s

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
)

// SSRF guard for the chart-fetch HTTP client.
//
// The chart-fetch endpoints take an operator-supplied ref (URL or OCI
// path) and issue an outbound HTTP request to it. Without controls,
// that's a Server-Side Request Forgery vector: an attacker can pivot
// Periscope's network identity to reach:
//
//   - AWS IMDS (169.254.169.254) — would expose IAM credentials.
//   - Apiserver (kubernetes.default.svc) — authenticated as the
//     Periscope pod's ServiceAccount, surface every secret it can read.
//   - Internal services (RFC1918 ranges, IPv6 ULA, internal load
//     balancers, sidecar containers on localhost).
//
// Defense: validate the *resolved* IP at dial time via net.Dialer's
// Control callback. The address arrives as "ip:port" (post-DNS), so
// we sidestep DNS rebinding — a public-resolving hostname can't flip
// to a private IP between the check and the connect.
//
// Operators running internal chart repos can opt out of the private-
// IP block via PERISCOPE_HELM_FETCH_ALLOW_PRIVATE=true. Loopback
// stays blocked even with that flag (test code uses an explicit
// in-process override; see chartFetchAllowLoopbackForTest).

// chartFetchAllowLoopbackForTest is a package-level toggle the test
// suite flips so httptest fixtures (which bind to 127.0.0.1) work
// despite the loopback block. Production code never flips this.
var chartFetchAllowLoopbackForTest atomic.Bool

// SetChartFetchAllowLoopbackForTest enables the loopback bypass for
// tests in other packages (cmd/periscope handler tests use
// httptest, which binds to 127.0.0.1). Returns a restore func the
// caller MUST invoke (typically via t.Cleanup). Test-only.
func SetChartFetchAllowLoopbackForTest(allow bool) func() {
	prev := chartFetchAllowLoopbackForTest.Swap(allow)
	return func() { chartFetchAllowLoopbackForTest.Store(prev) }
}

// envAllowPrivate is read once at first use; operators set it via
// the Helm chart's env passthrough. Cached so we don't os.Getenv on
// every dial.
var envAllowPrivate = func() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("PERISCOPE_HELM_FETCH_ALLOW_PRIVATE")))
	return v == "true" || v == "1" || v == "yes"
}()

// dialControlSSRFGuard is the net.Dialer Control callback. Called
// after DNS resolution, before connect. Returns an error to abort
// the connection when the resolved IP is in a blocked range.
func dialControlSSRFGuard(_ string, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("ssrf-guard: bad address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Should never happen — Control is called post-resolution
		// with an IP literal. Belt-and-suspenders.
		return fmt.Errorf("ssrf-guard: %q is not an IP literal", host)
	}
	if reason := blockedReason(ip); reason != "" {
		return fmt.Errorf("ssrf-guard: blocked %s — %s", ip, reason)
	}
	return nil
}

// blockedReason returns a non-empty reason string when the IP should
// be blocked, or "" when it's allowed. Order matters: more specific
// (IMDS) before broader (link-local) so the diagnostic is precise.
func blockedReason(ip net.IP) string {
	// AWS IMDS specifically — block always, regardless of opt-in
	// flags. There is no legitimate chart-repo reason to hit IMDS
	// and the consequence (IAM credential exfil) is severe.
	if ip.Equal(net.IPv4(169, 254, 169, 254)) {
		return "AWS IMDS endpoint"
	}
	// Link-local (covers IMDS for IPv6 too, plus general 169.254/16
	// + fe80::/10). Block always. Same rationale as IMDS.
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return "link-local address"
	}
	// Loopback (127.0.0.0/8 + ::1). Block in production; allow in
	// tests (httptest binds to 127.0.0.1 by design). Operators don't
	// have a legitimate reason to reach localhost from a Periscope
	// pod's chart-fetch path.
	if ip.IsLoopback() {
		if chartFetchAllowLoopbackForTest.Load() {
			return ""
		}
		return "loopback address"
	}
	// Private RFC1918 + CGN + IPv6 ULA. Block by default; opt-in
	// via env var for operators running internal chart repos.
	if ip.IsPrivate() {
		if envAllowPrivate {
			return ""
		}
		return "private network address (set PERISCOPE_HELM_FETCH_ALLOW_PRIVATE=true to allow internal repos)"
	}
	// Multicast / unspecified / interface-local — never legitimate
	// for chart fetches.
	if ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return "non-unicast address"
	}
	return ""
}
