package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// SPKI-hash pinning for the registration TLS dial. Lets operators
// run agents against a self-signed central server (single LB, no
// public cert) without distributing the full CA bundle.
//
// Pattern borrowed from kubeadm's `--discovery-token-ca-cert-hash`
// (RFC 7469 PKP). The hash is computed over the peer certificate's
// SubjectPublicKeyInfo, so cert rotation that preserves the key
// (the common case for short-lived certs minted from a stable CA)
// doesn't invalidate the pin.
//
// Used only during the agent's first-boot registration TLS dial to
// the central server's HTTP endpoint. After registration succeeds
// the agent has the real CA bundle and uses standard TLS chain
// verification for the long-lived tunnel.

// expectedHashPrefix is the only hash algorithm we accept. Mirrors
// kubeadm's choice; SHA-256 over SPKI is the de-facto standard for
// public-key pinning.
const expectedHashPrefix = "sha256:"

// parseSPKIHash unwraps the "sha256:<64-hex>" form into raw bytes.
// Returns a clear error for anything that's not exactly that shape
// — operators get a useful "expected sha256:<hex>, got X" instead
// of a low-level decode error.
func parseSPKIHash(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, expectedHashPrefix) {
		return nil, fmt.Errorf("agent server CA hash: expected prefix %q, got %q", expectedHashPrefix, s)
	}
	hex64 := strings.TrimPrefix(s, expectedHashPrefix)
	if len(hex64) != 64 {
		return nil, fmt.Errorf("agent server CA hash: expected 64 hex chars after prefix, got %d", len(hex64))
	}
	raw, err := hex.DecodeString(hex64)
	if err != nil {
		return nil, fmt.Errorf("agent server CA hash: hex decode: %w", err)
	}
	return raw, nil
}

// computeSPKIHash returns the SHA-256 hash of the certificate's
// SubjectPublicKeyInfo bytes (the DER-encoded SPKI structure as it
// appears on the wire — `cert.RawSubjectPublicKeyInfo`).
func computeSPKIHash(cert *x509.Certificate) []byte {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return sum[:]
}

// pinningTLSConfig returns a *tls.Config that performs SPKI-based
// hash pinning against the supplied expected hash. Standard chain
// verification is intentionally disabled (`InsecureSkipVerify: true`)
// — the pin is the trust anchor, replacing CA-chain validation for
// the registration dial only.
//
// Usage in an http.Client.Transport:
//
//	tlsCfg, err := pinningTLSConfig(hashHex)
//	if err != nil { ... }
//	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}}
//
// On a hash mismatch the TLS handshake fails with our error so it
// surfaces in the agent log with the actual computed hash for
// comparison — the operator can copy-paste it back into the Helm
// values if they confirm it's the right server.
func pinningTLSConfig(expectedHashHex string) (*tls.Config, error) {
	expectedRaw, err := parseSPKIHash(expectedHashHex)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		// We're replacing chain validation with pin validation; the
		// VerifyPeerCertificate callback is what actually decides.
		InsecureSkipVerify: true, //nolint:gosec
		MinVersion:         tls.VersionTLS12,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("agent SPKI pin: server presented no cert")
			}
			leaf, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("agent SPKI pin: parse leaf: %w", err)
			}
			got := computeSPKIHash(leaf)
			if !bytesEqual(got, expectedRaw) {
				return fmt.Errorf("agent SPKI pin mismatch: server presented sha256:%s, expected sha256:%s",
					hex.EncodeToString(got), expectedHashHex[len(expectedHashPrefix):])
			}
			return nil
		},
	}, nil
}

// bytesEqual is the constant-time comparison appropriate for hash
// equality. Stdlib's crypto/subtle has ConstantTimeCompare; using a
// thin wrapper here keeps the imports tidy and the call site obvious.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
