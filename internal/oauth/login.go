package oauth

import (
	"context"
	"fmt"
	"io"
)

// Login runs the whole browser flow: bind the loopback listener, open the
// consent page, wait for the redirect, exchange the code. open is how the URL
// reaches a browser (OpenBrowser in production, a test double otherwise);
// when it fails the URL is printed for the user to open by hand. out receives
// progress text and belongs on stderr.
func Login(ctx context.Context, cfg Config, open func(url string) error, out io.Writer) (Token, error) {
	if cfg.ClientID == "" {
		return Token{}, fmt.Errorf("no OAuth client id configured")
	}

	verifier, err := GenerateVerifier()
	if err != nil {
		return Token{}, err
	}
	state, err := GenerateState()
	if err != nil {
		return Token{}, err
	}

	listener, err := NewListener(cfg.RedirectURL, state)
	if err != nil {
		return Token{}, err
	}
	defer listener.Close()

	consentURL, err := cfg.authorizeURL(state, Challenge(verifier))
	if err != nil {
		return Token{}, err
	}

	_, _ = fmt.Fprintln(out, "Opening your browser to sign in to Worksome and approve access…")
	if open == nil || open(consentURL) != nil {
		_, _ = fmt.Fprintln(out, "Could not open a browser. Visit this URL to continue:")
	} else {
		_, _ = fmt.Fprintln(out, "If the browser did not open, visit:")
	}
	_, _ = fmt.Fprintf(out, "  %s\n", consentURL)
	_, _ = fmt.Fprintln(out, "Waiting for approval…")

	code, err := listener.Wait(ctx)
	if err != nil {
		return Token{}, err
	}

	return cfg.Exchange(ctx, code, verifier)
}
