package tunnel

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
)

// MTLSAuthorizer validates an inbound agent connection by inspecting
// the verified mTLS peer certificate. The cluster name is the cert's
// CN (set at signing time by CA.SignClient).
//
// Designed to be plugged into ServerOptions.Authorizer.
//
// Caller responsibilities:
//   - The HTTP server must be configured with TLS and
//     ClientAuth=RequireAndVerifyClientCert against a CertPool that
//     includes only this CA (use CA.ClusterPool()).
//   - The optional NameAllowed callback gives the registry a chance
//     to refuse a cluster the operator has since deregistered, even
//     if the cert hasn't expired yet.
type MTLSAuthorizer struct {
	// NameAllowed is called with the cluster name extracted from the
	// peer cert's CN. Return false to reject the connection. nil =
	// allow any name with a valid cert (useful in early dev; never
	// in production).
	NameAllowed func(name string) bool
}

// Authorize implements the Authorizer signature from server.go.
func (a *MTLSAuthorizer) Authorize(r *http.Request) (string, bool, error) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return "", false, errors.New("tunnel mTLS: no peer certificate presented")
	}
	leaf := r.TLS.PeerCertificates[0]
	if err := checkClientCertUsage(leaf); err != nil {
		return "", false, fmt.Errorf("tunnel mTLS: %w", err)
	}
	name := leaf.Subject.CommonName
	if name == "" {
		return "", false, errors.New("tunnel mTLS: peer cert CN is empty")
	}
	if a.NameAllowed != nil && !a.NameAllowed(name) {
		return name, false, fmt.Errorf("tunnel mTLS: cluster %q not in registry", name)
	}
	return name, true, nil
}

// checkClientCertUsage enforces that the presented cert was minted
// for client auth — defense in depth in case the TLS layer somehow
// accepted a cert without ExtKeyUsageClientAuth.
func checkClientCertUsage(cert *x509.Certificate) error {
	for _, eku := range cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageClientAuth {
			return nil
		}
	}
	return errors.New("peer cert missing clientAuth extKeyUsage")
}
