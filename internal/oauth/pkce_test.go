package oauth

import (
	"regexp"
	"testing"
)

// RFC 7636 appendix B: the S256 challenge for a known verifier.
func TestChallengeMatchesRFC7636Example(t *testing.T) {
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const want = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := Challenge(verifier); got != want {
		t.Errorf("Challenge() = %q, want %q", got, want)
	}
}

func TestGenerateVerifierIsWellFormedAndUnique(t *testing.T) {
	unreserved := regexp.MustCompile(`^[A-Za-z0-9\-._~]{43,128}$`)
	seen := map[string]bool{}
	for range 50 {
		v, err := GenerateVerifier()
		if err != nil {
			t.Fatal(err)
		}
		if !unreserved.MatchString(v) {
			t.Fatalf("verifier %q is outside RFC 7636's charset or length", v)
		}
		if seen[v] {
			t.Fatal("verifier repeated")
		}
		seen[v] = true
	}
	s1, _ := GenerateState()
	s2, _ := GenerateState()
	if s1 == "" || s1 == s2 {
		t.Errorf("state values must be non-empty and distinct, got %q and %q", s1, s2)
	}
}
