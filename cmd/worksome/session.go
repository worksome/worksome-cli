package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/worksome/worksome-cli/internal/client"
	"github.com/worksome/worksome-cli/internal/config"
	"github.com/worksome/worksome-cli/internal/oauth"
)

// sessionEndedError tells the user how to sign in again once Worksome no longer accepts a browser-login session.
type sessionEndedError struct {
	profile string
	cause   error
}

func (e *sessionEndedError) Error() string {
	return fmt.Sprintf("Your session has ended. Run `worksome auth login --browser --profile %s`.", e.profile)
}

func (e *sessionEndedError) Unwrap() error { return e.cause }

// sessionFor returns name when p is a browser session sent to its own endpoint, else "": another platform's refusal says nothing about the session.
func sessionFor(name string, p config.Profile, endpoint string) string {
	own := p.Endpoint
	if own == "" {
		own = config.DefaultEndpoint
	}
	if !p.IsOAuth() || endpoint != own {
		return ""
	}
	return name
}

// explainEndedSession turns an Unauthenticated error on the named browser-login profile into how to sign in again; session is empty for any other token.
func explainEndedSession(session string, err error) error {
	if session != "" && client.IsAuthError(err) {
		return &sessionEndedError{profile: session, cause: err}
	}
	return err
}

// explainEndedSessions applies explainEndedSession to the error of every command under cmd.
func explainEndedSessions(cmd *cobra.Command, session *string) {
	for _, sub := range cmd.Commands() {
		explainEndedSessions(sub, session)
	}
	if run := cmd.RunE; run != nil {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			// run sets *session, so read it only after the call.
			err := run(c, args)
			return explainEndedSession(*session, err)
		}
	}
}

// useSession renews the named browser-login profile when it is due; if Worksome cannot be reached, the current token is kept while it is still valid.
func useSession(ctx context.Context, cfg *config.Config, name string) error {
	p, ok := cfg.Profiles[name]
	if !ok || !oauth.NeedsRefresh(p, time.Now()) {
		return nil
	}
	err := renewSession(ctx, cfg, name)
	if err == nil {
		return nil
	}
	if oauth.IsInvalidGrant(err) {
		return &sessionEndedError{profile: name, cause: err}
	}
	exp, ok := p.Expiry()
	if !ok || !exp.After(time.Now()) {
		return err
	}
	fmt.Fprintf(os.Stderr, "warning: %v; using the current token, which expires %s\n", err, exp.Local().Format("2 Jan 2006 15:04"))
	return nil
}
