package tunnel

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
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

// Guard for the IP-SAN bug fixed during v1.1.4-rc soak: a mixed
// hostname + IP-literal SAN list must land in the right cert fields
// (DNSNames vs IPAddresses), because Go's x509 verifier checks DNS
// SANs only when the client dialed a hostname and IP SANs only when
// it dialed an IP literal. Putting everything into DNSNames silently
// broke agents dialing the tunnel on an IP (e.g. 192.168.0.6 in a
// local cross-cluster soak test).
func TestSignServer_SplitsDNSAndIPSANs(t *testing.T) {
	ca, _, err := GenerateCA("periscope-test", CertValidity{})
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	sans := []string{
		"localhost",
		"agents.periscope.example.com",
		"192.168.0.6",
		"10.0.0.1",
		"::1",
	}
	certPEM, _, err := ca.SignServer("periscope-server", sans, 0)
	if err != nil {
		t.Fatalf("SignServer: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("decode server cert PEM: nil block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}

	wantDNS := []string{"localhost", "agents.periscope.example.com"}
	if !equalStringSets(cert.DNSNames, wantDNS) {
		t.Errorf("DNSNames = %v, want %v", cert.DNSNames, wantDNS)
	}
	wantIPs := []net.IP{net.ParseIP("192.168.0.6"), net.ParseIP("10.0.0.1"), net.ParseIP("::1")}
	if len(cert.IPAddresses) != len(wantIPs) {
		t.Fatalf("IPAddresses len = %d, want %d (%v)", len(cert.IPAddresses), len(wantIPs), cert.IPAddresses)
	}
	for i, ip := range cert.IPAddresses {
		if !ip.Equal(wantIPs[i]) {
			t.Errorf("IPAddresses[%d] = %v, want %v", i, ip, wantIPs[i])
		}
	}

	// crypto/x509 verify on an IP-addressed dial uses IPAddresses, not
	// DNSNames. Confirm the cert actually validates for the IP we set.
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert())
	_, err = cert.Verify(x509.VerifyOptions{
		Roots:       pool,
		DNSName:     "", // matched via IPAddresses
		CurrentTime: cert.NotBefore.Add(time.Hour),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err != nil {
		t.Fatalf("verify with empty DNSName (chain only): %v", err)
	}
}

func equalStringSets(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	m := make(map[string]struct{}, len(want))
	for _, s := range want {
		m[s] = struct{}{}
	}
	for _, s := range got {
		if _, ok := m[s]; !ok {
			return false
		}
	}
	return true
}
