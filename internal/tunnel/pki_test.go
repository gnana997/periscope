package tunnel

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"testing"
	"time"
)

func TestGenerateCA_RoundTrip(t *testing.T) {
	ca, bundle, err := GenerateCA("periscope-test", CertValidity{})
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	if !ca.Cert().IsCA {
		t.Fatal("generated cert is not marked CA")
	}
	if len(bundle.CertPEM) == 0 || len(bundle.KeyPEM) == 0 {
		t.Fatal("bundle missing cert or key PEM")
	}

	loaded, err := LoadCA(bundle)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}
	if loaded.Cert().Subject.CommonName != "periscope-test" {
		t.Fatalf("loaded CA CN = %q, want periscope-test", loaded.Cert().Subject.CommonName)
	}
}

func TestLoadCA_RejectsTamperedKey(t *testing.T) {
	_, bundle, err := GenerateCA("ca-1", CertValidity{})
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	_, otherBundle, err := GenerateCA("ca-2", CertValidity{})
	if err != nil {
		t.Fatalf("GenerateCA #2: %v", err)
	}
	tampered := &CABundle{CertPEM: bundle.CertPEM, KeyPEM: otherBundle.KeyPEM}

	if _, err := LoadCA(tampered); err == nil {
		t.Fatal("LoadCA accepted bundle with mismatched cert/key")
	}
}

func TestLoadCA_RejectsExpired(t *testing.T) {
	_, bundle, err := GenerateCA("expired", CertValidity{CA: time.Nanosecond, Client: time.Hour})
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := LoadCA(bundle); err == nil {
		t.Fatal("LoadCA accepted expired CA")
	}
}

func TestSignClient_RoundTrip(t *testing.T) {
	ca, _, err := GenerateCA("ca", CertValidity{})
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	agentKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csrTmpl := &x509.CertificateRequest{Subject: pkix.Name{CommonName: "ignored-by-server"}}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, csrTmpl, agentKey)
	if err != nil {
		t.Fatalf("create CSR: %v", err)
	}

	certPEM, err := ca.SignClient(csrDER, "prod-eu", 0)
	if err != nil {
		t.Fatalf("SignClient: %v", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("issued cert PEM block missing or wrong type")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse signed cert: %v", err)
	}
	if cert.Subject.CommonName != "prod-eu" {
		t.Fatalf("CN = %q, want prod-eu (server overwrites the CSR's CN)", cert.Subject.CommonName)
	}
	if !hasClientAuthEKU(cert) {
		t.Fatal("signed cert missing ExtKeyUsageClientAuth")
	}

	pool := ca.ClusterPool()
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Fatalf("cert chain verification: %v", err)
	}
}

func TestSignClient_RejectsBogusCSR(t *testing.T) {
	ca, _, _ := GenerateCA("ca", CertValidity{})
	if _, err := ca.SignClient([]byte("not a CSR"), "prod-eu", 0); err == nil {
		t.Fatal("SignClient accepted garbage CSR bytes")
	}
}

// ── helpers ───────────────────────────────────────────────────────────

func hasClientAuthEKU(cert *x509.Certificate) bool {
	for _, eku := range cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageClientAuth {
			return true
		}
	}
	return false
}
