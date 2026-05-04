package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseSPKIHash(t *testing.T) {
	good := "sha256:" + strings.Repeat("a", 64)
	if _, err := parseSPKIHash(good); err != nil {
		t.Fatalf("parseSPKIHash(good): %v", err)
	}

	bad := []struct {
		name, in string
	}{
		{"missing prefix", strings.Repeat("a", 64)},
		{"wrong prefix", "sha512:" + strings.Repeat("a", 128)},
		{"short hex", "sha256:" + strings.Repeat("a", 32)},
		{"long hex", "sha256:" + strings.Repeat("a", 128)},
		{"non-hex", "sha256:" + strings.Repeat("z", 64)},
		{"empty", ""},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseSPKIHash(tc.in); err == nil {
				t.Fatalf("parseSPKIHash(%q) accepted bad input", tc.in)
			}
		})
	}
}

func TestPinningTLSConfig_HappyPath(t *testing.T) {
	srv, expectedHash := newSelfSignedHTTPSServer(t)
	defer srv.Close()

	tlsCfg, err := pinningTLSConfig(expectedHash)
	if err != nil {
		t.Fatalf("pinningTLSConfig: %v", err)
	}
	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
		Timeout:   5 * time.Second,
	}

	resp, err := httpClient.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET with matching pin failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q, want ok", body)
	}
}

func TestPinningTLSConfig_HashMismatch(t *testing.T) {
	srv, _ := newSelfSignedHTTPSServer(t)
	defer srv.Close()

	wrongHash := "sha256:" + strings.Repeat("0", 64)
	tlsCfg, err := pinningTLSConfig(wrongHash)
	if err != nil {
		t.Fatalf("pinningTLSConfig: %v", err)
	}
	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
		Timeout:   5 * time.Second,
	}

	if _, err := httpClient.Get(srv.URL); err == nil {
		t.Fatal("GET with mismatched pin should have failed")
	} else if !strings.Contains(err.Error(), "SPKI pin mismatch") {
		t.Fatalf("err = %v, want SPKI pin mismatch", err)
	}
}

func TestPinningTLSConfig_RejectsBadFormat(t *testing.T) {
	if _, err := pinningTLSConfig("not-a-hash"); err == nil {
		t.Fatal("pinningTLSConfig accepted bad input")
	}
}

// newSelfSignedHTTPSServer stands up an httptest TLS server using a
// freshly-generated ECDSA leaf cert. Returns the server and the
// SPKI-hash of its leaf in `sha256:<hex>` form so tests can assert
// pin-match success.
func newSelfSignedHTTPSServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	leaf := srv.Certificate()
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return srv, "sha256:" + hex.EncodeToString(sum[:])
}

// Compile-time: confirm we're not accidentally using deprecated
// big.Int X/Y on the leaf cert when generating test fixtures.
func TestSPKIHash_UsesRawSPKI(t *testing.T) {
	// Hand-build a minimal cert and verify computeSPKIHash matches
	// what an external SHA-256 of the SPKI bytes would produce.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    now,
		NotAfter:     now.Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	cert, _ := x509.ParseCertificate(der)

	got := computeSPKIHash(cert)
	want := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	if !bytesEqual(got, want[:]) {
		t.Fatalf("computeSPKIHash drift: %s vs %s",
			fmt.Sprintf("%x", got), fmt.Sprintf("%x", want[:]))
	}
}
