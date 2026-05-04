package tunnel

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// signedClientCert returns a real cert minted by the supplied CA so
// the authorizer's CN + EKU checks have something realistic to chew on.
func signedClientCert(t *testing.T, ca *CA, cn string) *x509.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csr, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}},
		key,
	)
	if err != nil {
		t.Fatalf("CSR: %v", err)
	}
	certPEM, err := ca.SignClient(csr, cn, 0)
	if err != nil {
		t.Fatalf("SignClient: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse signed cert: %v", err)
	}
	return cert
}

func TestMTLSAuthorizer_HappyPath(t *testing.T) {
	ca, _, _ := GenerateCA("ca", CertValidity{})
	cert := signedClientCert(t, ca, "prod-eu")

	authz := &MTLSAuthorizer{NameAllowed: func(string) bool { return true }}
	r := httptest.NewRequest("GET", "/connect", nil)
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}

	name, ok, err := authz.Authorize(r)
	if err != nil || !ok {
		t.Fatalf("Authorize: ok=%v err=%v", ok, err)
	}
	if name != "prod-eu" {
		t.Fatalf("name = %q, want prod-eu", name)
	}
}

func TestMTLSAuthorizer_NoTLS(t *testing.T) {
	authz := &MTLSAuthorizer{}
	r := httptest.NewRequest("GET", "/connect", nil) // r.TLS is nil
	if _, ok, err := authz.Authorize(r); ok || err == nil {
		t.Fatalf("Authorize with no TLS: ok=%v err=%v", ok, err)
	}
}

func TestMTLSAuthorizer_NoPeerCert(t *testing.T) {
	authz := &MTLSAuthorizer{}
	r := httptest.NewRequest("GET", "/connect", nil)
	r.TLS = &tls.ConnectionState{}
	if _, ok, err := authz.Authorize(r); ok || err == nil {
		t.Fatalf("Authorize with TLS but no peer cert: ok=%v err=%v", ok, err)
	}
}

func TestMTLSAuthorizer_DenyByName(t *testing.T) {
	ca, _, _ := GenerateCA("ca", CertValidity{})
	cert := signedClientCert(t, ca, "decommissioned")

	authz := &MTLSAuthorizer{NameAllowed: func(name string) bool {
		return name != "decommissioned"
	}}
	r := httptest.NewRequest("GET", "/connect", nil)
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}

	name, ok, err := authz.Authorize(r)
	if ok {
		t.Fatal("Authorize allowed a denied name")
	}
	if err == nil {
		t.Fatal("Authorize denied without an error message")
	}
	if name != "decommissioned" {
		t.Fatalf("name still returned for logging = %q, want decommissioned", name)
	}
}

func TestMTLSAuthorizer_RejectsCertWithoutClientAuthEKU(t *testing.T) {
	// Hand-build a leaf cert without ExtKeyUsageClientAuth — the
	// defense-in-depth check must reject it even if the TLS layer
	// would have accepted it.
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Now().UTC()
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "rogue-ca"},
		IsCA:                  true,
		BasicConstraintsValid: true,
		NotBefore:             now,
		NotAfter:              now.Add(time.Hour),
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)

	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "agent"},
		NotBefore:    now,
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		// ExtKeyUsage deliberately omitted.
	}
	leafDER, _ := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	leafCert, _ := x509.ParseCertificate(leafDER)

	authz := &MTLSAuthorizer{}
	r := httptest.NewRequest("GET", "/connect", nil)
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leafCert}}

	if _, ok, err := authz.Authorize(r); ok || err == nil {
		t.Fatalf("Authorize accepted cert without clientAuth EKU: ok=%v err=%v", ok, err)
	}
}

// ensure no unused-import on the standard http alias import above
var _ = http.MethodGet
