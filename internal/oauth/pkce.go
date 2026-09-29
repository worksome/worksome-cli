// Package oauth implements the CLI side of the OAuth 2.0 authorization code
// grant with PKCE against Worksome's Laravel Passport server: a loopback
// redirect listener, the consent URL, the code exchange, and refresh.
//
// The CLI is a public client. There is no client secret to protect, which is
// what PKCE is for: the code can only be exchanged by the process that started
// the flow, because only it knows the verifier.
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// GenerateVerifier returns a PKCE code verifier: 64 random bytes encoded as
// unpadded base64url, 86 characters, within RFC 7636's 43–128 and its
// unreserved character set.
func GenerateVerifier() (string, error) {
	return randomToken(64)
}

// GenerateState returns an opaque value bound to one login attempt. The
// redirect must carry it back unchanged, or the code is ignored.
func GenerateState() (string, error) {
	return randomToken(32)
}

// Challenge returns the S256 code challenge for a verifier:
// base64url(sha256(verifier)) without padding.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
