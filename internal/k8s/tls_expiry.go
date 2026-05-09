// TLS cert expiry helpers. Used by the Secrets list (TLS-typed
// secrets get an expiry chip directly) and the Ingresses list
// (joins to TLS-typed Secrets in the same namespace and picks the
// soonest expiry across an Ingress's spec.tls[]).
//
// All helpers soft-fail to nil rather than returning errors — a
// malformed PEM block in one Secret should not poison the whole
// list-page render. The SPA renders nil as an em-dash chip.
package k8s

import (
	"crypto/x509"
	"encoding/pem"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// parseFirstTLSCertExpiry parses the PEM-encoded `tls.crt` value of a
// Secret of type kubernetes.io/tls and returns the NotAfter timestamp
// of the LEAF certificate (the first PEM block). Soft-fails on:
//   - missing tls.crt key
//   - empty value
//   - non-PEM value
//   - non-CERTIFICATE PEM block
//   - x509 parse failure
//
// Returns nil in any of those cases.
func parseFirstTLSCertExpiry(data map[string][]byte) *time.Time {
	raw, ok := data["tls.crt"]
	if !ok || len(raw) == 0 {
		return nil
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	t := cert.NotAfter
	return &t
}

// secretTLSExpiry is a small wrapper that gates parseFirstTLSCertExpiry
// on the Secret's Type. Non-TLS Secrets always return nil — we don't
// peek into arbitrary opaque secret payloads.
func secretTLSExpiry(s *corev1.Secret) *time.Time {
	if s.Type != corev1.SecretTypeTLS {
		return nil
	}
	return parseFirstTLSCertExpiry(s.Data)
}

// soonestExpiry returns the earlier of two *time.Time values, treating
// nil as "no expiry known" — i.e., a known expiry always wins over nil.
// Used by the Ingress list to fold per-secret expiries into a single
// per-ingress chip.
func soonestExpiry(a, b *time.Time) *time.Time {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.Before(*b) {
		return a
	}
	return b
}
