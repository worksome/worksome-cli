package oauth

import (
	"context"
	"fmt"
	"time"

	"github.com/worksome/worksome-cli/internal/config"
)

// RefreshLeeway is how close to expiry a session is renewed. Access tokens
// live 15 days, so a day of margin means a token is never presented in its
// last hours, where a slow clock or a long-running command could cross the line.
const RefreshLeeway = 24 * time.Hour

// NeedsRefresh reports whether p is an OAuth session whose access token
// expires within RefreshLeeway of now. Personal access tokens (no refresh
// token) never need refreshing.
func NeedsRefresh(p config.Profile, now time.Time) bool {
	if p.RefreshToken == "" {
		return false
	}
	expiry, ok := p.Expiry()
	if !ok {
		// An OAuth session with an unreadable expiry: refresh, which either
		// repairs the record or fails clearly.
		return true
	}
	return !expiry.After(now.Add(RefreshLeeway))
}

// RefreshProfile renews p's session in place. On success p holds the new
// access token, the rotated refresh token and the new expiry; the caller
// persists it. On failure p is untouched.
func RefreshProfile(ctx context.Context, cfg Config, p *config.Profile) error {
	tok, err := cfg.Refresh(ctx, p.RefreshToken)
	if err != nil {
		return fmt.Errorf("renewing session: %w", err)
	}
	refresh := tok.RefreshToken
	if refresh == "" {
		refresh = p.RefreshToken
	}
	p.SetSession(tok.AccessToken, refresh, tok.ExpiresAt)
	return nil
}
