package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/worksome/worksome-cli/internal/config"
)

func TestNeedsRefresh(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	stamp := func(d time.Duration) string { return now.Add(d).UTC().Format(time.RFC3339) }
	tests := []struct {
		name string
		p    config.Profile
		want bool
	}{
		{"personal access token never refreshes", config.Profile{Token: "pat", ExpiresAt: stamp(-time.Hour)}, false},
		{"fresh session", config.Profile{Token: "a", RefreshToken: "r", ExpiresAt: stamp(10 * 24 * time.Hour)}, false},
		{"inside the leeway", config.Profile{Token: "a", RefreshToken: "r", ExpiresAt: stamp(RefreshLeeway - time.Minute)}, true},
		{"already expired", config.Profile{Token: "a", RefreshToken: "r", ExpiresAt: stamp(-time.Minute)}, true},
		{"session with unreadable expiry", config.Profile{Token: "a", RefreshToken: "r", ExpiresAt: "yesterday"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NeedsRefresh(tt.p, now); got != tt.want {
				t.Errorf("NeedsRefresh() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRefreshProfileRotatesAndKeepsOldOnFailure(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"expires_in":3600,"access_token":"new-acc","refresh_token":"new-ref"}`))
	}))
	defer ok.Close()

	p := config.Profile{Token: "old-acc", RefreshToken: "old-ref", Endpoint: "https://api.example.test/graphql"}
	if err := RefreshProfile(context.Background(), Config{ClientID: "c", TokenURL: ok.URL}, &p); err != nil {
		t.Fatalf("RefreshProfile() error = %v", err)
	}
	if p.Token != "new-acc" || p.RefreshToken != "new-ref" || p.Endpoint != "https://api.example.test/graphql" {
		t.Errorf("profile after refresh = %+v", p)
	}
	if exp, ok := p.Expiry(); !ok || exp.Before(time.Now().Add(50*time.Minute)) {
		t.Errorf("expiry not recorded: %+v", p)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"The refresh token is invalid."}`))
	}))
	defer bad.Close()

	before := p
	if err := RefreshProfile(context.Background(), Config{ClientID: "c", TokenURL: bad.URL}, &p); err == nil {
		t.Fatal("expected an error")
	}
	if p != before {
		t.Errorf("a failed refresh must leave the profile untouched: %+v", p)
	}
}
