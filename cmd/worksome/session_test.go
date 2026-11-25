package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/worksome/worksome-cli/internal/config"
)

const sessionEndedStage = "Your session has ended. Run `worksome auth login --browser --profile stage`."

// sessionAPI is a GraphQL stub that accepts only the bearer token accept and refuses any other as the gateway does.
func sessionAPI(t *testing.T, accept string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") == "Bearer "+accept {
			_, _ = w.Write([]byte(`{"data":{"viewer":{"name":"Ada","email":"ada@example.test"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"Unauthenticated.","extensions":{"code":"DOWNSTREAM_SERVICE_ERROR"}}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// tokenEndpoint is a token endpoint that answers every request with status and body.
func tokenEndpoint(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// unreachableURL is a loopback URL nothing listens on.
func unreachableURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return srv.URL
}

// sessionEnv saves a config whose current profile "stage" is a browser session holding acc-0/ref-0, plus a "pat" profile.
func sessionEnv(t *testing.T, api, tokenURL string, expiry time.Time) {
	t.Helper()
	clearOAuthEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("WORKSOME_API_TOKEN", "")
	t.Setenv("WORKSOME_ENDPOINT", "")
	t.Setenv("WORKSOME_PROFILE", "")

	cfg := &config.Config{CurrentProfile: "stage", Profiles: map[string]config.Profile{
		"stage": {
			Token: "acc-0", RefreshToken: "ref-0", ExpiresAt: expiry.UTC().Format(time.RFC3339),
			Endpoint: api, OAuthTokenURL: tokenURL, OAuthClientID: "cid",
		},
		"pat": {Token: "pat-token", Endpoint: api},
	}}
	if err := cfg.SaveTo(filepath.Join(home, ".worksome", "config.yaml")); err != nil {
		t.Fatal(err)
	}
}

func isSessionEnded(err error) bool {
	var se *sessionEndedError
	return errors.As(err, &se)
}

// The gateway's bare "Unauthenticated." gave a browser-login user no way forward.
func TestUnauthenticatedBrowserSessionSaysHowToSignIn(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		envToken    string
		envEndpoint bool // send the session to another platform through WORKSOME_ENDPOINT
		ended       bool
	}{
		{name: "browser session", args: []string{"viewer", "get"}, ended: true},
		{name: "browser session, auth status", args: []string{"auth", "status"}, ended: true},
		{name: "personal access token", args: []string{"--profile", "pat", "viewer", "get"}},
		{name: "--token on a browser profile", args: []string{"--token", "other", "viewer", "get"}},
		{name: "WORKSOME_API_TOKEN on a browser profile", args: []string{"viewer", "get"}, envToken: "other"},
		{name: "browser session sent to another platform", args: []string{"viewer", "get"}, envEndpoint: true},
		{name: "browser session sent to another platform, auth status", args: []string{"auth", "status"}, envEndpoint: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionEnv(t, sessionAPI(t, "nothing"), unreachableURL(t), time.Now().Add(10*24*time.Hour))
			t.Setenv("WORKSOME_API_TOKEN", tt.envToken)
			if tt.envEndpoint {
				t.Setenv("WORKSOME_ENDPOINT", sessionAPI(t, "nothing"))
			}

			var err error
			captureStdout(t, func() { err = runRootCmd(tt.args...) })
			if err == nil {
				t.Fatal("expected the API to refuse the token")
			}
			if tt.ended {
				if err.Error() != sessionEndedStage {
					t.Errorf("error = %q, want %q", err, sessionEndedStage)
				}
				return
			}
			if isSessionEnded(err) || !strings.Contains(err.Error(), "Unauthenticated") {
				t.Errorf("a token the profile does not own, or one sent to another platform, must keep the API's error, got %q", err)
			}
		})
	}
}

// A network error while renewing used to block every command, even with a token that still worked.
func TestRenewalFailureKeepsAValidToken(t *testing.T) {
	invalidGrant := `{"error":"invalid_grant","error_description":"The refresh token is invalid.","hint":"Token has been revoked"}`
	tests := []struct {
		name     string
		tokenURL func(t *testing.T) string
		expiry   time.Duration
		wantErr  string // empty: the command runs on acc-0
	}{
		{name: "token endpoint unreachable, token still valid", tokenURL: unreachableURL, expiry: time.Hour},
		{name: "token endpoint down, token still valid", tokenURL: func(t *testing.T) string {
			return tokenEndpoint(t, http.StatusServiceUnavailable, `{"message":"Service Unavailable"}`)
		}, expiry: time.Hour},
		{name: "token endpoint unreachable, token expired", tokenURL: unreachableURL, expiry: -time.Hour, wantErr: "renewing session"},
		{name: "refresh token revoked, token still valid", tokenURL: func(t *testing.T) string {
			return tokenEndpoint(t, http.StatusBadRequest, invalidGrant)
		}, expiry: time.Hour, wantErr: sessionEndedStage},
		{name: "refresh token revoked, token expired", tokenURL: func(t *testing.T) string {
			return tokenEndpoint(t, http.StatusBadRequest, invalidGrant)
		}, expiry: -time.Hour, wantErr: sessionEndedStage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionEnv(t, sessionAPI(t, "acc-0"), tt.tokenURL(t), time.Now().Add(tt.expiry))

			var err error
			captureStdout(t, func() { err = runRootCmd("viewer", "get") })

			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("the current token should still be used, got %v", err)
			case tt.wantErr == sessionEndedStage && (err == nil || err.Error() != sessionEndedStage):
				t.Errorf("error = %v, want %q", err, sessionEndedStage)
			case tt.wantErr != "" && tt.wantErr != sessionEndedStage:
				if err == nil || isSessionEnded(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want a renewal error that does not claim the session ended", err)
				}
			}
		})
	}
}

// auth status reported a session that could still renew as expired, and exited 0 for a refused token.
func TestAuthStatusRenewsFirstAndFailsWhenRefused(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		api        string // the token the API accepts
		tokenURL   func(t *testing.T) string
		expiry     time.Duration
		wantErr    string // empty: exits 0
		wantStatus string
	}{
		{
			name: "expired token that renews", args: []string{"auth", "status"}, api: "acc-1",
			tokenURL: func(t *testing.T) string { srv, _ := rotatingTokenServer(t); return srv.URL },
			expiry:   -time.Hour, wantStatus: "Status:   Authenticated",
		},
		{
			name: "revoked refresh token", args: []string{"auth", "status"}, api: "acc-1",
			tokenURL: func(t *testing.T) string {
				return tokenEndpoint(t, http.StatusBadRequest, `{"error":"invalid_grant","error_description":"The refresh token is invalid."}`)
			},
			expiry: -time.Hour, wantErr: sessionEndedStage,
		},
		{
			name: "browser session refused by the API", args: []string{"auth", "status"}, api: "nothing",
			tokenURL: unreachableURL, expiry: 10 * 24 * time.Hour,
			wantErr: sessionEndedStage, wantStatus: "Status:   Invalid or expired token",
		},
		{
			name: "personal access token refused by the API", args: []string{"--profile", "pat", "auth", "status"}, api: "nothing",
			tokenURL: unreachableURL, expiry: 10 * 24 * time.Hour,
			wantErr: "Unauthenticated", wantStatus: "Status:   Invalid or expired token",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionEnv(t, sessionAPI(t, tt.api), tt.tokenURL(t), time.Now().Add(tt.expiry))

			var err error
			out := captureStdout(t, func() { err = runRootCmd(tt.args...) })

			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("auth status failed: %v", err)
			case tt.wantErr == sessionEndedStage && (err == nil || err.Error() != sessionEndedStage):
				t.Errorf("error = %v, want %q", err, sessionEndedStage)
			case tt.wantErr != "" && tt.wantErr != sessionEndedStage && (err == nil || isSessionEnded(err) || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("error = %v, want the API's %q", err, tt.wantErr)
			}
			if !strings.Contains(out, tt.wantStatus) {
				t.Errorf("output = %q, want it to contain %q", out, tt.wantStatus)
			}
		})
	}
}
