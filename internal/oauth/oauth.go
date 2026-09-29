package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultRedirectURL is the loopback address the Worksome CLI client is
// registered with. Passport matches redirect URIs exactly, so the port is
// fixed; the listener binds it and fails clearly if something else has it.
const DefaultRedirectURL = "http://127.0.0.1:51789/callback"

// Config identifies the OAuth client and the server endpoints.
type Config struct {
	// ClientID of the public "Worksome CLI" client registered on the platform.
	ClientID string
	// AuthorizeURL is the consent page, e.g. https://use.worksome.com/oauth/authorize.
	AuthorizeURL string
	// TokenURL is the token endpoint, e.g. https://use.worksome.com/oauth/token.
	TokenURL string
	// RedirectURL is the loopback callback the client is registered with.
	RedirectURL string
	// HTTPClient is used for the token endpoint; nil means a 30-second-timeout default.
	HTTPClient *http.Client
}

// Token is what a successful exchange or refresh yields.
type Token struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// authorizeURL builds the consent URL for one login attempt.
func (c Config) authorizeURL(state, challenge string) (string, error) {
	u, err := url.Parse(c.AuthorizeURL)
	if err != nil {
		return "", fmt.Errorf("parsing authorize URL %q: %w", c.AuthorizeURL, err)
	}
	q := u.Query()
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", c.RedirectURL)
	q.Set("response_type", "code")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	// Scopes are not enforced by the platform yet; an empty scope asks for
	// everything the user can do, which is what a token has always meant here.
	q.Set("scope", "")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Exchange trades the authorization code for tokens, proving possession of
// the verifier the challenge was derived from.
func (c Config) Exchange(ctx context.Context, code, verifier string) (Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {c.ClientID},
		"redirect_uri":  {c.RedirectURL},
		"code":          {code},
		"code_verifier": {verifier},
	}
	return c.token(ctx, form)
}

// Refresh trades a refresh token for a new access token. Passport rotates the
// refresh token on every use, so callers must store the returned one.
func (c Config) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.ClientID},
		"refresh_token": {refreshToken},
	}
	return c.token(ctx, form)
}

// tokenResponse is Passport's token endpoint body, success or failure.
type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int64  `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	Hint             string `json:"hint"`
}

func (c Config) token(ctx context.Context, form url.Values) (Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("calling token endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Token{}, fmt.Errorf("reading token response: %w", err)
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return Token{}, fmt.Errorf("token endpoint returned %d with a non-JSON body", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || tr.Error != "" {
		msg := tr.ErrorDescription
		if msg == "" {
			msg = tr.Error
		}
		if tr.Hint != "" {
			msg += " (" + tr.Hint + ")"
		}
		if msg == "" {
			msg = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return Token{}, fmt.Errorf("token endpoint refused: %s", msg)
	}
	if tr.AccessToken == "" {
		return Token{}, fmt.Errorf("token endpoint returned no access token")
	}

	t := Token{AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken}
	if tr.ExpiresIn > 0 {
		t.ExpiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	return t, nil
}
