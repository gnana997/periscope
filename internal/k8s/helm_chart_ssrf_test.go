package k8s

import (
	"net"
	"testing"
)

func TestBlockedReason(t *testing.T) {
	// Reset env-driven flag for predictable tests; restore at end.
	prevAllow := envAllowPrivate
	envAllowPrivate = false
	t.Cleanup(func() { envAllowPrivate = prevAllow })

	// Disable loopback bypass for the duration so the loopback path
	// is testable. Other tests in this package set it true via
	// TestMain; we save and restore.
	prev := chartFetchAllowLoopbackForTest.Swap(false)
	t.Cleanup(func() { chartFetchAllowLoopbackForTest.Store(prev) })

	cases := []struct {
		name      string
		ip        string
		want      bool // true means SHOULD be blocked
		wantMatch string
	}{
		{name: "AWS IMDS v4", ip: "169.254.169.254", want: true, wantMatch: "IMDS"},
		{name: "loopback v4", ip: "127.0.0.1", want: true, wantMatch: "loopback"},
		{name: "loopback v6", ip: "::1", want: true, wantMatch: "loopback"},
		{name: "RFC1918 10/8", ip: "10.0.0.1", want: true, wantMatch: "private"},
		{name: "RFC1918 172.16/12", ip: "172.16.0.1", want: true, wantMatch: "private"},
		{name: "RFC1918 192.168/16", ip: "192.168.1.1", want: true, wantMatch: "private"},
		{name: "link-local v4", ip: "169.254.0.5", want: true, wantMatch: "link-local"},
		{name: "link-local v6", ip: "fe80::1", want: true, wantMatch: "link-local"},
		{name: "IPv6 ULA", ip: "fc00::1", want: true, wantMatch: "private"},
		{name: "unspecified v4", ip: "0.0.0.0", want: true, wantMatch: "non-unicast"},
		// 224.0.0.1 is link-local-multicast — caught by the
		// link-local check (which fires before the broader multicast
		// branch). Both cover the same threat; the precise reason
		// string is implementation detail.
		{name: "multicast v4", ip: "224.0.0.1", want: true, wantMatch: "link-local"},
		{name: "non-link-local multicast v4", ip: "239.255.255.250", want: true, wantMatch: "non-unicast"},
		{name: "public v4 (Cloudflare DNS)", ip: "1.1.1.1", want: false},
		{name: "public v4 (Google DNS)", ip: "8.8.8.8", want: false},
		{name: "public v6 (Cloudflare DNS)", ip: "2606:4700:4700::1111", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("parse %q failed", tc.ip)
			}
			got := blockedReason(ip)
			if (got != "") != tc.want {
				t.Errorf("blockedReason(%s) = %q; blocked? got=%v want=%v",
					tc.ip, got, got != "", tc.want)
			}
			if tc.want && tc.wantMatch != "" && !strContains(got, tc.wantMatch) {
				t.Errorf("blockedReason(%s) = %q; expected to mention %q",
					tc.ip, got, tc.wantMatch)
			}
		})
	}
}

func TestBlockedReason_LoopbackBypassFlag(t *testing.T) {
	prev := chartFetchAllowLoopbackForTest.Swap(true)
	t.Cleanup(func() { chartFetchAllowLoopbackForTest.Store(prev) })

	if reason := blockedReason(net.ParseIP("127.0.0.1")); reason != "" {
		t.Errorf("loopback should be allowed when bypass flag set; blocked = %q", reason)
	}
}

func TestBlockedReason_PrivateOptIn(t *testing.T) {
	prevAllow := envAllowPrivate
	envAllowPrivate = true
	t.Cleanup(func() { envAllowPrivate = prevAllow })

	if reason := blockedReason(net.ParseIP("10.0.0.1")); reason != "" {
		t.Errorf("RFC1918 should be allowed under opt-in; blocked = %q", reason)
	}
	// IMDS is never allowed even under opt-in.
	if reason := blockedReason(net.ParseIP("169.254.169.254")); reason == "" {
		t.Errorf("IMDS must remain blocked even under private-opt-in")
	}
}

func strContains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
