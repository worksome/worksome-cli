package main

import (
	"os"

	"github.com/worksome/worksome-cli/internal/oauth"
)

// oauthClientID is the id of the public "Worksome CLI" OAuth client. It is
// set at build time (-X main.oauthClientID=...) so releases carry it; the
// environment can override it, which is how a staging platform is targeted.
// Empty means browser login is unavailable and `auth login` says so.
var oauthClientID = ""

const (
	defaultAuthorizeURL = "https://use.worksome.com/oauth/authorize"
	defaultTokenURL     = "https://use.worksome.com/oauth/token"
)

// oauthConfig assembles the client configuration from build-time defaults
// and the environment.
func oauthConfig() oauth.Config {
	return oauth.Config{
		ClientID:     envOr("WORKSOME_OAUTH_CLIENT_ID", oauthClientID),
		AuthorizeURL: envOr("WORKSOME_OAUTH_AUTHORIZE_URL", defaultAuthorizeURL),
		TokenURL:     envOr("WORKSOME_OAUTH_TOKEN_URL", defaultTokenURL),
		RedirectURL:  oauth.DefaultRedirectURL,
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
