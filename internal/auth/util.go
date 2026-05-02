package auth

import (
	"encoding/json"
	"strings"
)

// jsonDecode is a thin wrapper kept here so oidc.go doesn't need
// an encoding/json import (the rest of oidc.go is otherwise stdlib-
// minimal).
func jsonDecode(b []byte, v any) error {
	return json.Unmarshal(b, v)
}

func splitDot(s string) []string { return strings.Split(s, ".") }
