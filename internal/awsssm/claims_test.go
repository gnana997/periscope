package awsssm

import (
	"encoding/base64"
	"testing"
)

func jwtWith(payload string) string {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	return enc(`{"alg":"none"}`) + "." + enc(payload) + ".sig"
}

func TestIDTokenAud(t *testing.T) {
	// single-string aud (the common Auth0 id_token shape)
	auds, ok := IDTokenAud(jwtWith(`{"aud":"client-id-123"}`))
	if !ok || len(auds) != 1 || auds[0] != "client-id-123" {
		t.Fatalf("single aud: got %v ok=%v", auds, ok)
	}
	// array aud
	auds, ok = IDTokenAud(jwtWith(`{"aud":["a","b"]}`))
	if !ok || len(auds) != 2 {
		t.Fatalf("array aud: got %v ok=%v", auds, ok)
	}
	// unparseable
	if _, ok := IDTokenAud("not-a-jwt"); ok {
		t.Fatal("expected ok=false for non-JWT")
	}
}

func TestAudMatches(t *testing.T) {
	tok := jwtWith(`{"aud":["periscope","other"]}`)
	if !AudMatches(tok, "periscope") {
		t.Error("should match a present aud")
	}
	if AudMatches(tok, "nope") {
		t.Error("should not match an absent aud")
	}
	if AudMatches("garbage", "periscope") {
		t.Error("garbage token should not match")
	}
}
