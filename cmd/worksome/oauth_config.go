package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/worksome/worksome-cli/internal/config"
	"github.com/worksome/worksome-cli/internal/oauth"
)

// defaultOAuthClientID is the first-party public client the platform registers in every environment.
const defaultOAuthClientID = "00000000-2ac6-40ca-96d7-5c25396b168d"

// productionOAuthBase serves OAuth for production, whose GraphQL lives on api.worksome.com instead.
const productionOAuthBase = "https://use.worksome.com"

// oauthConfig assembles the client configuration for the platform behind endpoint, honouring environment overrides.
func oauthConfig(endpoint string) oauth.Config {
	authorize, token := oauthURLs(endpoint)
	return oauth.Config{
		ClientID:     envOr("WORKSOME_OAUTH_CLIENT_ID", defaultOAuthClientID),
		AuthorizeURL: envOr("WORKSOME_OAUTH_AUTHORIZE_URL", authorize),
		TokenURL:     envOr("WORKSOME_OAUTH_TOKEN_URL", token),
		RedirectURL:  oauth.DefaultRedirectURL,
	}
}

// oauthURLs maps a GraphQL endpoint to its platform's OAuth endpoints, which every platform but production serves on the same host.
func oauthURLs(endpoint string) (authorize, token string) {
	base := productionOAuthBase
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" && u.Hostname() != "api.worksome.com" {
		base = u.Scheme + "://" + u.Host
	}
	return base + "/oauth/authorize", base + "/oauth/token"
}

// sessionOAuthConfig is where p's session renews: the issuer stored at login, or production for profiles saved before it was stored.
func sessionOAuthConfig(p config.Profile) oauth.Config {
	if p.OAuthTokenURL == "" {
		return oauthConfig(config.DefaultEndpoint)
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
