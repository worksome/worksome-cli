package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func tokenServer(t *testing.T, check func(t *testing.T, r *http.Request), status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parsing form: %v", err)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			t.Errorf("Content-Type = %q, want form encoding", ct)
		}
		check(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestExchangeSendsPKCEAndParsesTokens(t *testing.T) {
	srv := tokenServer(t, func(t *testing.T, r *http.Request) {
		for k, want := range map[string]string{
			"grant_type":    "authorization_code",
			"client_id":     "cli-client",
			"redirect_uri":  "http://127.0.0.1:0/callback",
			"code":          "the-code",
			"code_verifier": "the-verifier",
		} {
			if got := r.PostForm.Get(k); got != want {
				t.Errorf("form[%s] = %q, want %q", k, got, want)
			}
		}
		if r.PostForm.Has("client_secret") {
			t.Error("a public client must not send a client_secret")
		}
	}, http.StatusOK, `{"token_type":"Bearer","expires_in":1296000,"access_token":"acc","refresh_token":"ref"}`)
	defer srv.Close()

	cfg := Config{ClientID: "cli-client", TokenURL: srv.URL, RedirectURL: "http://127.0.0.1:0/callback"}
	before := time.Now()
	tok, err := cfg.Exchange(context.Background(), "the-code", "the-verifier")
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if tok.AccessToken != "acc" || tok.RefreshToken != "ref" {
		t.Errorf("tokens = %+v", tok)
	}
	if want := before.Add(15 * 24 * time.Hour); tok.ExpiresAt.Before(want.Add(-time.Minute)) || tok.ExpiresAt.After(want.Add(time.Minute)) {
		t.Errorf("ExpiresAt = %v, want about %v", tok.ExpiresAt, want)
	}
}

func TestRefreshSendsRefreshGrant(t *testing.T) {
	srv := tokenServer(t, func(t *testing.T, r *http.Request) {
		if got := r.PostForm.Get("grant_type"); got != "refresh_token" {
			t.Errorf("grant_type = %q", got)
		}
		if got := r.PostForm.Get("refresh_token"); got != "old-refresh" {
			t.Errorf("refresh_token = %q", got)
		}
		if got := r.PostForm.Get("client_id"); got != "cli-client" {
			t.Errorf("client_id = %q", got)
		}
	}, http.StatusOK, `{"expires_in":100,"access_token":"new-acc","refresh_token":"new-ref"}`)
	defer srv.Close()

	tok, err := Config{ClientID: "cli-client", TokenURL: srv.URL}.Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if tok.AccessToken != "new-acc" || tok.RefreshToken != "new-ref" {
		t.Errorf("tokens = %+v", tok)
	}
}

func TestTokenEndpointErrorsAreReadable(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"passport error shape", 400, `{"error":"invalid_grant","error_description":"The refresh token is invalid.","hint":"Token has been revoked"}`, "The refresh token is invalid. (Token has been revoked)"},
		{"error code only", 401, `{"error":"invalid_client"}`, "invalid_client"},
		{"non-JSON body", 502, `<html>bad gateway</html>`, "502"},
		{"200 with no token", 200, `{}`, "no access token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := tokenServer(t, func(*testing.T, *http.Request) {}, tt.status, tt.body)
			defer srv.Close()
			_, err := Config{ClientID: "c", TokenURL: srv.URL}.Refresh(context.Background(), "r")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestAuthorizeURLCarriesPKCEAndState(t *testing.T) {
	cfg := Config{ClientID: "cli-client", AuthorizeURL: "https://use.example.test/oauth/authorize", RedirectURL: DefaultRedirectURL}
	u, err := cfg.authorizeURL("st8", "chal")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"client_id=cli-client", "response_type=code", "state=st8",
		"code_challenge=chal", "code_challenge_method=S256",
		"redirect_uri=http%3A%2F%2F127.0.0.1%3A51789%2Fcallback",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("authorize URL missing %q: %s", want, u)
		}
	}
}
