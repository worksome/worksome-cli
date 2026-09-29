package oauth

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The whole flow without a browser: the "opener" plays the user, following
// the consent URL's redirect_uri straight back with a code and the state.
func TestLoginEndToEnd(t *testing.T) {
	var exchanged url.Values
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		exchanged = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"expires_in":60,"access_token":"acc","refresh_token":"ref"}`))
	}))
	defer token.Close()

	// Bind a free port first so the test does not depend on 51789 being free.
	probe, err := NewListener("http://127.0.0.1:0/callback", "probe")
	if err != nil {
		t.Fatal(err)
	}
	redirect := "http://" + probe.Addr() + "/callback"
	probe.Close()

	cfg := Config{ClientID: "cli", AuthorizeURL: "https://use.example.test/oauth/authorize", TokenURL: token.URL, RedirectURL: redirect}

	var challengeSeen string
	open := func(consent string) error {
		u, err := url.Parse(consent)
		if err != nil {
			return err
		}
		q := u.Query()
		challengeSeen = q.Get("code_challenge")
		// Simulate Passport redirecting the browser back after approval.
		cb, _ := url.Parse(q.Get("redirect_uri"))
		cb.RawQuery = url.Values{"code": {"granted"}, "state": {q.Get("state")}}.Encode()
		go func() {
			time.Sleep(20 * time.Millisecond)
			resp, err := http.Get(cb.String())
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}

	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tok, err := Login(ctx, cfg, open, &out)
	if err != nil {
		t.Fatalf("Login() error = %v\n%s", err, out.String())
	}
	if tok.AccessToken != "acc" || tok.RefreshToken != "ref" {
		t.Errorf("token = %+v", tok)
	}
	if got := exchanged.Get("code"); got != "granted" {
		t.Errorf("exchanged code = %q", got)
	}
	if v := exchanged.Get("code_verifier"); v == "" || Challenge(v) != challengeSeen {
		t.Error("the verifier sent to the token endpoint must match the challenge shown to the browser")
	}
	if !strings.Contains(out.String(), "If the browser did not open, visit:") {
		t.Errorf("progress text should always print the URL, got %q", out.String())
	}
}

func TestLoginWithoutClientIDFailsBeforeAnything(t *testing.T) {
	_, err := Login(context.Background(), Config{}, nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "client id") {
		t.Errorf("error = %v", err)
	}
}

// --no-browser passes a nil opener. Nothing is attempted, so the output must
// neither announce a browser nor report that one failed to open.
func TestLoginWithoutBrowserClaimsNothing(t *testing.T) {
	// A plain net.Listener releases its port synchronously on Close, so the
	// redirect port is reliably free for Login to bind.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	redirect := "http://" + probe.Addr().String() + "/callback"
	_ = probe.Close()

	cfg := Config{ClientID: "cli", AuthorizeURL: "https://use.example.test/oauth/authorize", TokenURL: "https://use.example.test/oauth/token", RedirectURL: redirect}

	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = Login(ctx, cfg, nil, &out)
	if err == nil {
		t.Fatal("expected the login to time out waiting for approval")
	}
	if !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Login() error = %v", err)
	}

	text := out.String()
	for _, bad := range []string{"Opening your browser", "Could not open"} {
		if strings.Contains(text, bad) {
			t.Errorf("--no-browser output must not contain %q, got:\n%s", bad, text)
		}
	}
	if !strings.Contains(text, "Visit this URL") || !strings.Contains(text, "/oauth/authorize?") {
		t.Errorf("--no-browser output must print the consent URL, got:\n%s", text)
	}
}
