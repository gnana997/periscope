package tunnel

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// PKI bootstrap for the agent-tunnel: a per-deployment ECDSA CA that
// signs short-lived (90-day default) client certs for every registered
// agent. The CA itself is generated once on first server start and
// then loaded from the same on-disk material on every restart.
//
// Why ECDSA P-256 over RSA: smaller, faster signing, modern best
// practice, and our cert volume is bounded by the cluster count
// (handfuls, not thousands per second).
//
// Why a single per-deployment CA over per-cluster CAs: makes
// validation a single trust anchor and keeps the key material
// surface tiny. We sacrifice the ability to revoke "all certs for
// cluster X" by burning the issuer (we'd burn every cluster's cert
// instead). For v1.x.0 this is the right tradeoff; if/when the threat
// model demands per-cluster issuers we'll layer that in additively.
//
// What this file does NOT do:
//   - Persist the CA to a Secret. The caller (cmd/periscope) is
//     responsible for loading + saving — this package just produces
//     and consumes PEM byte slices so it can be unit-tested without
//     a kube client.
//   - Cert revocation lists. v1.x.0 trusts that an agent whose cert
//     is no longer wanted has its registration removed from the
//     server's allowlist; the cert itself is still cryptographically
//     valid until expiry. CRL/OCSP land later if needed.

// CA is a CA issuer for agent client certs.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey

	// PEM bytes of the cert; cached so we don't re-encode on every
	// agent registration.
	certPEM []byte
}

// CABundle is the persisted shape: PEM-encoded cert + key. The server
// writes this to a K8s Secret on first boot, reads it on subsequent
// boots.
type CABundle struct {
	CertPEM []byte // PEM "CERTIFICATE" block
	KeyPEM  []byte // PEM "EC PRIVATE KEY" block
}

// CertValidity is the lifetime of certs minted by this CA.
type CertValidity struct {
	CA     time.Duration // CA cert lifetime; default 10 years
	Client time.Duration // Client cert lifetime; default 90 days
}

func (v CertValidity) withDefaults() CertValidity {
	if v.CA == 0 {
		v.CA = 10 * 365 * 24 * time.Hour
	}
	if v.Client == 0 {
		v.Client = 90 * 24 * time.Hour
	}
	return v
}

// GenerateCA produces a fresh self-signed CA. The returned bundle
// is the form the caller should persist; LoadCA reverses it.
//
// Caller-supplied commonName is informational; agents validate the
// CA by trust-anchor pinning, not by name.
func GenerateCA(commonName string, validity CertValidity) (*CA, *CABundle, error) {
	v := validity.withDefaults()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("ca keygen: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, nil, fmt.Errorf("ca serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"periscope"}},
		NotBefore:             now.Add(-1 * time.Minute), // small clock-skew tolerance
		NotAfter:              now.Add(v.CA),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("ca self-sign: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("ca parse: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("ca key marshal: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return &CA{cert: cert, key: key, certPEM: certPEM},
		&CABundle{CertPEM: certPEM, KeyPEM: keyPEM},
		nil
}

// LoadCA reconstructs a CA from a previously-persisted bundle.
// Returns an error if the cert/key don't pair, the cert isn't a CA,
// or the cert is expired.
func LoadCA(bundle *CABundle) (*CA, error) {
	if bundle == nil || len(bundle.CertPEM) == 0 || len(bundle.KeyPEM) == 0 {
		return nil, errors.New("tunnel pki: empty CA bundle")
	}

	certBlock, _ := pem.Decode(bundle.CertPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, errors.New("tunnel pki: cert PEM missing or wrong type")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("tunnel pki: parse CA cert: %w", err)
	}
	if !cert.IsCA {
		return nil, errors.New("tunnel pki: loaded cert is not a CA")
	}
	if time.Now().After(cert.NotAfter) {
		return nil, fmt.Errorf("tunnel pki: CA expired at %s", cert.NotAfter.Format(time.RFC3339))
	}

	keyBlock, _ := pem.Decode(bundle.KeyPEM)
	if keyBlock == nil || keyBlock.Type != "EC PRIVATE KEY" {
		return nil, errors.New("tunnel pki: key PEM missing or wrong type")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("tunnel pki: parse CA key: %w", err)
	}

	// Confirm the key matches the cert by signing the cert's
	// SubjectPublicKey hash. Cheap, catches bundle tampering.
	if !certKeyPair(cert, key) {
		return nil, errors.New("tunnel pki: cert and key do not pair")
	}

	return &CA{cert: cert, key: key, certPEM: bundle.CertPEM}, nil
}

// CertPEM returns the CA's PEM-encoded cert. Used by the registration
// handler to ship the trust anchor back to the agent.
func (c *CA) CertPEM() []byte { return c.certPEM }

// Cert returns the parsed CA cert. Used by the mTLS authorizer to
// build the verification root pool.
func (c *CA) Cert() *x509.Certificate { return c.cert }

// SignClient mints a client cert for the given cluster name. The CSR
// is in DER form; the agent generates the keypair locally and only
// the public key + name claim cross the wire.
//
// Returns the PEM-encoded client cert. The agent stores it in its
// own Secret and presents it on every reconnect.
func (c *CA) SignClient(csrDER []byte, clusterName string, validity time.Duration) ([]byte, error) {
	if validity == 0 {
		validity = CertValidity{}.withDefaults().Client
	}

	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, fmt.Errorf("parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("verify CSR self-signature: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, fmt.Errorf("client serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		// CN = cluster name; this is the load-bearing field. The
		// mTLS authorizer reads CN to identify which cluster the
		// connecting agent belongs to. Organization is informational.
		Subject:     pkix.Name{CommonName: clusterName, Organization: []string{"periscope-agent"}},
		NotBefore:   now.Add(-1 * time.Minute),
		NotAfter:    now.Add(validity),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, csr.PublicKey, c.key)
	if err != nil {
		return nil, fmt.Errorf("sign client cert: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// SignServer mints a server cert (ExtKeyUsageServerAuth) chained to
// this CA. Used by the central server for the tunnel TLS listener so
// agents can validate the server identity using the same CA bundle
// they received at registration.
//
// dnsNames populates the cert SANs — agents connect by hostname so
// the SANs must include whatever DNS name the operator points the
// agent at (e.g. "periscope.example.com" for prod, "localhost" for
// kind smoke tests).
func (c *CA) SignServer(commonName string, dnsNames []string, validity time.Duration) ([]byte, []byte, error) {
	if validity == 0 {
		validity = CertValidity{}.withDefaults().Client
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("server keygen: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, fmt.Errorf("server serial: %w", err)
	}
	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName, Organization: []string{"periscope-server"}},
		NotBefore:    now.Add(-1 * time.Minute),
		NotAfter:     now.Add(validity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, nil, fmt.Errorf("sign server cert: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal server key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// ClusterPool returns an x509.CertPool containing only this CA. Used
// by the mTLS authorizer's TLS config so client certs are validated
// against this trust anchor and only this one.
func (c *CA) ClusterPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(c.cert)
	return pool
}

// ── helpers ──────────────────────────────────────────────────────────

func randomSerial() (*big.Int, error) {
	// 128-bit serial; RFC 5280 says 20 bytes max, 8 minimum, must be
	// positive. crypto/rand.Int returns [0, max) so we always get a
	// positive value.
	max := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, max)
}

func certKeyPair(cert *x509.Certificate, key *ecdsa.PrivateKey) bool {
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return false
	}
	return pub.X.Cmp(key.X) == 0 && pub.Y.Cmp(key.Y) == 0
}
