package main

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/worksome/worksome-cli/internal/config"
	"github.com/worksome/worksome-cli/internal/oauth"
)

// defaultOAuthClientID is the first-party public client the platform registers in every environment.
const defaultOAuthClientID = "00000000-2ac6-40ca-96d7-5c25396b168d"

// productionOAuthHost serves OAuth for production, whose GraphQL lives on api.worksome.com instead.
const productionOAuthHost = "use.worksome.com"

// oauthConfig assembles the client configuration for the platform behind endpoint, honouring environment overrides.
func oauthConfig(endpoint string) (oauth.Config, error) {
	authorize, token := os.Getenv("WORKSOME_OAUTH_AUTHORIZE_URL"), os.Getenv("WORKSOME_OAUTH_TOKEN_URL")
	if authorize == "" || token == "" {
		a, t, err := oauthURLs(endpoint)
		if err != nil {
			return oauth.Config{}, err
		}
		authorize, token = cmp.Or(authorize, a), cmp.Or(token, t)
	}
	return oauth.Config{
		ClientID:     envOr("WORKSOME_OAUTH_CLIENT_ID", defaultOAuthClientID),
		AuthorizeURL: authorize,
		TokenURL:     token,
		RedirectURL:  oauth.DefaultRedirectURL,
	}, nil
}

// oauthURLs maps a Worksome GraphQL endpoint to the app host that serves its OAuth: <label>-api.<domain> to <label>.<domain>, api.worksome.com to use.worksome.com.
// Any other host is refused, so a mistyped or foreign endpoint never receives the code, the verifier or the refresh token.
func oauthURLs(endpoint string) (authorize, token string, err error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("parsing endpoint: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	if !strings.HasSuffix(host, ".worksome.com") {
		return "", "", fmt.Errorf("%q is not a Worksome host, so its OAuth URLs cannot be derived: set WORKSOME_OAUTH_AUTHORIZE_URL and WORKSOME_OAUTH_TOKEN_URL", u.Hostname())
	}
	label, rest, _ := strings.Cut(host, ".")
	host = strings.TrimSuffix(label, "-api") + "." + rest
	if host == "api.worksome.com" {
		host = productionOAuthHost
	}
	if port := u.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	}
	base := u.Scheme + "://" + host
	return base + "/oauth/authorize", base + "/oauth/token", nil
}

// sessionOAuthConfig is where p's session renews: the issuer stored at login, or production for profiles saved before it was stored.
func sessionOAuthConfig(p config.Profile) oauth.Config {
	if p.OAuthTokenURL == "" {
		cfg, _ := oauthConfig(config.DefaultEndpoint) // the default endpoint always resolves
		return cfg
	}
	clientID := p.OAuthClientID
	if clientID == "" {
		clientID = defaultOAuthClientID
	}
	return oauth.Config{ClientID: clientID, TokenURL: p.OAuthTokenURL, RedirectURL: oauth.DefaultRedirectURL}
}

// sessionProfile builds the profile saved after a browser sign-in, recording where the session was issued.
func sessionProfile(endpoint string, ocfg oauth.Config, tok oauth.Token) config.Profile {
	p := config.Profile{Endpoint: endpoint, OAuthTokenURL: ocfg.TokenURL, OAuthClientID: ocfg.ClientID}
	p.SetSession(tok.AccessToken, tok.RefreshToken, tok.ExpiresAt)
	return p
}

// renewSession renews the named profile's session under the config lock: Passport revokes a refresh token once used, so two processes must not both spend it.
func renewSession(ctx context.Context, cfg *config.Config, name string) error {
	unlock, err := config.Lock()
	if err != nil {
		return fmt.Errorf("renewing session: %w", err)
	}
	defer unlock()

	// Re-read: another process may have renewed while this one waited for the lock.
	fresh, err := config.Load()
	if err != nil {
		return fmt.Errorf("renewing session: %w", err)
	}
	p, ok := fresh.Profiles[name]
	if !ok {
		return fmt.Errorf("renewing session: profile %q is no longer configured", name)
	}
	if oauth.NeedsRefresh(p, time.Now()) {
		if err := oauth.RefreshProfile(ctx, sessionOAuthConfig(p), &p); err != nil {
			return err
		}
		fresh.Profiles[name] = p
		if err := fresh.Save(); err != nil {
			// The renewed token still works for this run; only its persistence failed.
			fmt.Fprintf(os.Stderr, "warning: session renewed but could not be saved: %v\n", err)
		}
	}
	cfg.Profiles[name] = p
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
