package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/worksome/worksome-cli/internal/config"
	"github.com/worksome/worksome-cli/internal/oauth"
)

func clearOAuthEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"WORKSOME_OAUTH_CLIENT_ID", "WORKSOME_OAUTH_AUTHORIZE_URL", "WORKSOME_OAUTH_TOKEN_URL"} {
		t.Setenv(k, "")
	}
}

func TestOAuthURLsFollowTheEndpoint(t *testing.T) {
	tests := []struct {
		name, endpoint, base string
	}{
		{"production gateway", config.DefaultEndpoint, "https://use.worksome.com"},
		{"production gateway, other path", "https://api.worksome.com/other", "https://use.worksome.com"},
		{"production gateway, mixed case", "https://API.Worksome.com/graphql", "https://use.worksome.com"},
		{"production app host", "https://use.worksome.com/web-api/v2/graphql", "https://use.worksome.com"},
		{"stage gateway", "https://stage-api.worksome.com/graphql", "https://stage.worksome.com"},
		{"stage app host", "https://stage.worksome.com/web-api/v2/graphql", "https://stage.worksome.com"},
		{"sandbox gateway", "https://demo-api.sand.hz.worksome.com/graphql", "https://demo.sand.hz.worksome.com"},
		{"sandbox app host", "https://demo.sand.hz.worksome.com/web-api/v2/graphql", "https://demo.sand.hz.worksome.com"},
		{"port kept", "https://demo-api.sand.hz.worksome.com:8443/graphql", "https://demo.sand.hz.worksome.com:8443"},
		{"basic-auth credentials dropped", "https://user:secret@demo-api.sand.hz.worksome.com/graphql", "https://demo.sand.hz.worksome.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, tok, err := oauthURLs(tt.endpoint)
			if err != nil {
				t.Fatalf("oauthURLs(%q) error = %v", tt.endpoint, err)
			}
			if a != tt.base+"/oauth/authorize" || tok != tt.base+"/oauth/token" {
				t.Errorf("oauthURLs(%q) = %q, %q; want %s/oauth/{authorize,token}", tt.endpoint, a, tok, tt.base)
			}
		})
	}
}

// Deriving OAuth URLs for any host would hand that host the code, the verifier and every refresh token.
func TestOAuthURLsRefuseHostsOutsideWorksome(t *testing.T) {
	for _, endpoint := range []string{
		"https://staging.example.test/web-api/v2/graphql",
		"https://worksome.com/graphql",
		"https://evilworksome.com/graphql",
		"https://api.worksome.com.example.test/graphql",
		"https://localhost:8443/graphql",
		"not a url",
		"",
	} {
		if a, tok, err := oauthURLs(endpoint); err == nil || !strings.Contains(err.Error(), "WORKSOME_OAUTH_AUTHORIZE_URL") {
			t.Errorf("oauthURLs(%q) = %q, %q, %v; want an error naming the overrides", endpoint, a, tok, err)
		}
	}
}

func TestOAuthConfigDefaultsAndOverrides(t *testing.T) {
	clearOAuthEnv(t)
	cfg, err := oauthConfig("https://stage-api.worksome.com/graphql")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientID != defaultOAuthClientID {
		t.Errorf("ClientID = %q, want the first-party client", cfg.ClientID)
	}
	if cfg.TokenURL != "https://stage.worksome.com/oauth/token" {
		t.Errorf("TokenURL = %q", cfg.TokenURL)
	}

	t.Setenv("WORKSOME_OAUTH_CLIENT_ID", "env-client")
	t.Setenv("WORKSOME_OAUTH_AUTHORIZE_URL", "https://env.example.test/authorize")
	t.Setenv("WORKSOME_OAUTH_TOKEN_URL", "https://env.example.test/token")
	cfg, err = oauthConfig(config.DefaultEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientID != "env-client" || cfg.AuthorizeURL != "https://env.example.test/authorize" || cfg.TokenURL != "https://env.example.test/token" {
		t.Errorf("environment overrides ignored: %+v", cfg)
	}
}

func TestOAuthConfigForAForeignHostNeedsBothOverrides(t *testing.T) {
	const endpoint = "https://platform.example.test/web-api/v2/graphql"
	tests := []struct {
		name, authorize, token string
		wantErr                bool
	}{
		{"no override", "", "", true},
		{"authorize only", "https://platform.example.test/oauth/authorize", "", true},
		{"token only", "", "https://platform.example.test/oauth/token", true},
		{"both", "https://platform.example.test/oauth/authorize", "https://platform.example.test/oauth/token", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearOAuthEnv(t)
			t.Setenv("WORKSOME_OAUTH_AUTHORIZE_URL", tt.authorize)
			t.Setenv("WORKSOME_OAUTH_TOKEN_URL", tt.token)
			cfg, err := oauthConfig(endpoint)
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "not a Worksome host") {
					t.Errorf("oauthConfig() = %+v, %v; want the foreign host refused", cfg, err)
				}
				return
			}
			if err != nil || cfg.AuthorizeURL != tt.authorize || cfg.TokenURL != tt.token {
				t.Errorf("oauthConfig() = %+v, %v; want the overrides", cfg, err)
			}
		})
	}
}

// One override on a Worksome host keeps the other URL derived, e.g. a sandbox token URL that carries basic-auth credentials.
func TestOAuthConfigMixesOneOverrideWithTheDerivedURL(t *testing.T) {
	clearOAuthEnv(t)
	t.Setenv("WORKSOME_OAUTH_TOKEN_URL", "https://user:secret@demo.sand.hz.worksome.com/oauth/token")
	cfg, err := oauthConfig("https://demo-api.sand.hz.worksome.com/graphql")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthorizeURL != "https://demo.sand.hz.worksome.com/oauth/authorize" || cfg.TokenURL != "https://user:secret@demo.sand.hz.worksome.com/oauth/token" {
		t.Errorf("oauthConfig() = %+v", cfg)
	}
}

// A staging session used to renew against production, sending its refresh token there.
func TestSessionRenewsWhereItWasIssued(t *testing.T) {
	clearOAuthEnv(t)
	ocfg, err := oauthConfig("https://stage-api.worksome.com/graphql")
	if err != nil {
		t.Fatal(err)
	}
	p := sessionProfile("https://stage-api.worksome.com/graphql", ocfg, oauth.Token{AccessToken: "a", RefreshToken: "r"})
	if got := sessionOAuthConfig(p); got.TokenURL != "https://stage.worksome.com/oauth/token" || got.ClientID != defaultOAuthClientID {
		t.Errorf("staging session renews at %+v", got)
	}

	t.Setenv("WORKSOME_OAUTH_TOKEN_URL", "https://elsewhere.example.test/oauth/token")
	if got := sessionOAuthConfig(p); got.TokenURL != "https://stage.worksome.com/oauth/token" {
		t.Errorf("a stored issuer must win over a later environment override, got %q", got.TokenURL)
	}

	legacy := config.Profile{Token: "a", RefreshToken: "r", Endpoint: "https://stage-api.worksome.com/graphql"}
	if got := sessionOAuthConfig(legacy); got.TokenURL != "https://elsewhere.example.test/oauth/token" {
		t.Errorf("a profile without a stored issuer keeps the old behaviour, got %q", got.TokenURL)
	}
	t.Setenv("WORKSOME_OAUTH_TOKEN_URL", "")
	if got := sessionOAuthConfig(legacy); got.TokenURL != "https://use.worksome.com/oauth/token" {
		t.Errorf("a profile without a stored issuer renews at production, got %q", got.TokenURL)
	}
}

// rotatingTokenServer behaves like Passport: each refresh token works once, then is revoked.
func rotatingTokenServer(t *testing.T) (srv *httptest.Server, calls func() int) {
	t.Helper()
	var mu sync.Mutex
	valid, n := "ref-0", 0
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		time.Sleep(50 * time.Millisecond) // widen the window two unlocked refreshes would race in
		mu.Lock()
		defer mu.Unlock()
		n++
		w.Header().Set("Content-Type", "application/json")
		if r.PostForm.Get("refresh_token") != valid {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"The refresh token is invalid."}`))
			return
		}
		valid = fmt.Sprintf("ref-%d", n)
		_, _ = fmt.Fprintf(w, `{"expires_in":1296000,"access_token":"acc-%d","refresh_token":%q}`, n, valid)
	}))
	t.Cleanup(srv.Close)
	return srv, func() int { mu.Lock(); defer mu.Unlock(); return n }
}

func TestRenewSessionSerialisesConcurrentProcesses(t *testing.T) {
	clearOAuthEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	srv, calls := rotatingTokenServer(t)

	expired := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	seed := &config.Config{CurrentProfile: "stage", Profiles: map[string]config.Profile{
		"stage": {Token: "acc-0", RefreshToken: "ref-0", ExpiresAt: expired, Endpoint: "https://stage.example.test/web-api/v2/graphql", OAuthTokenURL: srv.URL, OAuthClientID: "cid"},
	}}
	if err := seed.SaveTo(filepath.Join(home, ".worksome", "config.yaml")); err != nil {
		t.Fatal(err)
	}

	const procs = 5
	var wg sync.WaitGroup
	errs := make([]error, procs)
	tokens := make([]string, procs)
	for i := range procs {
		wg.Go(func() {
			cfg, err := config.Load()
			if err != nil {
				errs[i] = err
				return
			}
			errs[i] = renewSession(context.Background(), cfg, "stage")
			tokens[i] = cfg.Profiles["stage"].Token
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("process %d: %v", i, err)
		}
		if tokens[i] != "acc-1" {
			t.Errorf("process %d uses token %q, want the one renewed token", i, tokens[i])
		}
	}
	if n := calls(); n != 1 {
		t.Errorf("token endpoint called %d times, want 1", n)
	}
	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if p := saved.Profiles["stage"]; p.RefreshToken != "ref-1" || p.OAuthTokenURL != srv.URL {
		t.Errorf("saved profile = %+v", p)
	}
}

func TestAuthLoginBrowserFlagsExcludeTokenFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--browser", "--token", "x"},
		{"--browser", "--pat"},
		{"--no-browser", "--token", "x"},
		{"--no-browser", "--pat"},
	} {
		setupTestEnv(t)
		err := runRootCmd(append([]string{"auth", "login"}, args...)...)
		if err == nil || !strings.Contains(err.Error(), "none of the others can be") {
			t.Errorf("auth login %v: error = %v, want a mutually-exclusive flag error", args, err)
		}
	}
}

func TestAuthLoginBrowserRefusesForeignEndpointBeforeSignIn(t *testing.T) {
	setupTestEnv(t)
	clearOAuthEnv(t)
	err := runRootCmd("auth", "login", "--no-browser", "--endpoint", "https://platform.example.test/web-api/v2/graphql")
	if err == nil || !strings.Contains(err.Error(), "WORKSOME_OAUTH_TOKEN_URL") {
		t.Errorf("error = %v, want the foreign endpoint refused with the overrides named", err)
	}
}

func TestAuthLoginBrowserRefusesPlaintextEndpoint(t *testing.T) {
	setupTestEnv(t)
	err := runRootCmd("auth", "login", "--no-browser", "--endpoint", "http://platform.example.test/web-api/v2/graphql")
	if err == nil || !strings.Contains(err.Error(), "must use https") {
		t.Errorf("error = %v, want the plaintext endpoint refused before sign-in", err)
	}
}
