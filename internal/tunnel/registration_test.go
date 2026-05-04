package tunnel

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeCSR builds a base64-encoded DER CSR that the registration
// handler will accept. Returns the bytes the agent would post.
func fakeCSR(t *testing.T, cn string) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}}, key)
	if err != nil {
		t.Fatalf("CSR: %v", err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

func TestMintTokenHandler_HappyPath(t *testing.T) {
	store := NewTokenStore(TokenStoreOptions{ReapInterval: -1})
	h := MintTokenHandler(store)

	body, _ := json.Marshal(MintTokenRequest{Cluster: "prod-eu"})
	r := httptest.NewRequest("POST", "/api/agents/tokens", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var iss TokenIssuance
	if err := json.Unmarshal(w.Body.Bytes(), &iss); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if iss.Cluster != "prod-eu" || iss.Token == "" {
		t.Fatalf("issuance = %+v", iss)
	}
}

func TestMintTokenHandler_RejectsBadName(t *testing.T) {
	store := NewTokenStore(TokenStoreOptions{ReapInterval: -1})
	h := MintTokenHandler(store)

	cases := []string{"", "UPPER", "has space", "has_underscore", "-leading", "trailing-", strings.Repeat("a", 64)}
	for _, name := range cases {
		body, _ := json.Marshal(MintTokenRequest{Cluster: name})
		r := httptest.NewRequest("POST", "/api/agents/tokens", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("name %q: status = %d, want 400", name, w.Code)
		}
	}
}

func TestMintTokenHandler_RejectsNonPOST(t *testing.T) {
	store := NewTokenStore(TokenStoreOptions{ReapInterval: -1})
	h := MintTokenHandler(store)

	r := httptest.NewRequest("GET", "/api/agents/tokens", nil)
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

func TestRegisterHandler_HappyPath(t *testing.T) {
	store := NewTokenStore(TokenStoreOptions{ReapInterval: -1})
	ca, _, _ := GenerateCA("ca", CertValidity{})
	mint := MintTokenHandler(store)
	register := RegisterHandler(store, ca, 0)

	// 1. Mint a token for the cluster.
	body, _ := json.Marshal(MintTokenRequest{Cluster: "prod-eu"})
	r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mint(w, r)
	var iss TokenIssuance
	_ = json.Unmarshal(w.Body.Bytes(), &iss)

	// 2. Agent posts CSR + token, gets back a signed cert.
	regBody, _ := json.Marshal(RegisterRequest{
		Token:   iss.Token,
		Cluster: "prod-eu",
		CSR:     fakeCSR(t, "prod-eu"),
	})
	r = httptest.NewRequest("POST", "/", bytes.NewReader(regBody))
	w = httptest.NewRecorder()
	register(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp RegisterResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Issued cert parses + has the right CN.
	block, _ := pem.Decode([]byte(resp.Cert))
	if block == nil {
		t.Fatal("response cert: no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse issued cert: %v", err)
	}
	if cert.Subject.CommonName != "prod-eu" {
		t.Fatalf("issued cert CN = %q, want prod-eu", cert.Subject.CommonName)
	}

	// CA bundle in response matches the CA that signed.
	if !bytes.Equal([]byte(resp.CABundle), ca.CertPEM()) {
		t.Fatal("CABundle in response does not match CA cert PEM")
	}
}

func TestRegisterHandler_TokenReuseRejected(t *testing.T) {
	store := NewTokenStore(TokenStoreOptions{ReapInterval: -1})
	ca, _, _ := GenerateCA("ca", CertValidity{})
	register := RegisterHandler(store, ca, 0)

	iss, _ := store.MintToken("c")
	regBody, _ := json.Marshal(RegisterRequest{Token: iss.Token, Cluster: "c", CSR: fakeCSR(t, "c")})

	for i, want := range []int{http.StatusOK, http.StatusUnauthorized} {
		r := httptest.NewRequest("POST", "/", bytes.NewReader(regBody))
		w := httptest.NewRecorder()
		register(w, r)
		if w.Code != want {
			t.Fatalf("attempt %d: status = %d, want %d", i+1, w.Code, want)
		}
	}
}

func TestRegisterHandler_AllTokenFailuresReturn401(t *testing.T) {
	store := NewTokenStore(TokenStoreOptions{ReapInterval: -1})
	ca, _, _ := GenerateCA("ca", CertValidity{})
	register := RegisterHandler(store, ca, 0)

	cases := []struct {
		name string
		body RegisterRequest
	}{
		{"unknown", RegisterRequest{Token: "garbage", Cluster: "c", CSR: fakeCSR(t, "c")}},
		{"mismatch", func() RegisterRequest {
			iss, _ := store.MintToken("c")
			return RegisterRequest{Token: iss.Token, Cluster: "different", CSR: fakeCSR(t, "different")}
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
			w := httptest.NewRecorder()
			register(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
			}
			// Body is uniform across all token failures — attackers can't
			// distinguish "unknown" from "mismatch".
			if !strings.Contains(w.Body.String(), "registration rejected") {
				t.Fatalf("body = %q, want 'registration rejected'", w.Body.String())
			}
		})
	}
}

func TestRegisterHandler_BadCSRBytesRejected(t *testing.T) {
	store := NewTokenStore(TokenStoreOptions{ReapInterval: -1})
	ca, _, _ := GenerateCA("ca", CertValidity{})
	register := RegisterHandler(store, ca, 0)

	iss, _ := store.MintToken("c")
	regBody, _ := json.Marshal(RegisterRequest{
		Token: iss.Token, Cluster: "c",
		CSR: base64.StdEncoding.EncodeToString([]byte("not a CSR")),
	})
	r := httptest.NewRequest("POST", "/", bytes.NewReader(regBody))
	w := httptest.NewRecorder()
	register(w, r)
	// Token already burned by RedeemToken (it succeeds), then sign
	// fails → 500. The point is we don't 200 on garbage.
	if w.Code == http.StatusOK {
		t.Fatalf("registered with bogus CSR; status = %d", w.Code)
	}
}
