package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
)

// Listener receives the authorization redirect on the loopback interface.
type Listener struct {
	ln    net.Listener
	srv   *http.Server
	path  string
	state string
	once  sync.Once
	done  chan result
}

type result struct {
	code string
	err  error
}

// NewListener binds the host:port of redirectURL, or a free port on that host when it is taken
// (Passport ignores a loopback redirect's port), before the browser opens, and serves its path.
func NewListener(redirectURL, state string) (*Listener, error) {
	u, err := url.Parse(redirectURL)
	if err != nil {
		return nil, fmt.Errorf("parsing redirect URL %q: %w", redirectURL, err)
	}
	ln, err := net.Listen("tcp", u.Host)
	if err != nil {
		ln, err = net.Listen("tcp", net.JoinHostPort(u.Hostname(), "0"))
	}
	if err != nil {
		return nil, fmt.Errorf("cannot listen on %s for the login redirect: %w", u.Hostname(), err)
	}

	l := &Listener{ln: ln, path: u.Path, state: state, done: make(chan result, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc(u.Path, l.handle)
	l.srv = &http.Server{Handler: mux}
	go func() { _ = l.srv.Serve(ln) }()
	return l, nil
}

// Addr is the address actually bound, useful when the port was 0.
func (l *Listener) Addr() string { return l.ln.Addr().String() }

// RedirectURL is the callback URL on the port actually bound.
func (l *Listener) RedirectURL() string { return "http://" + l.Addr() + l.path }

func (l *Listener) handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// A redirect carrying a foreign or missing state is not ours. Answer it
	// and keep waiting: a stray request must not be able to end a legitimate
	// login, and it must never be able to inject a code into it.
	if q.Get("state") != l.state {
		http.Error(w, "This login link does not match the one the CLI is waiting for. Return to the terminal and try again.", http.StatusBadRequest)
		return
	}

	if e := q.Get("error"); e != "" {
		desc := q.Get("error_description")
		if desc == "" {
			desc = e
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, page("Access was not granted", "You can close this tab. Nothing was changed."))
		l.finish(result{err: fmt.Errorf("access denied: %s", desc)})
		return
	}

	code := q.Get("code")
	if code == "" {
		http.Error(w, "The redirect carried no authorization code.", http.StatusBadRequest)
		l.finish(result{err: errors.New("redirect carried no authorization code")})
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	// The code is not exchanged yet, so the page must not claim success.
	_, _ = fmt.Fprint(w, page("Return to the terminal", "Return to the terminal to finish signing in. You can close this tab."))
	l.finish(result{code: code})
}

func (l *Listener) finish(r result) {
	l.once.Do(func() { l.done <- r })
}

// Wait blocks until the redirect arrives or ctx ends, and returns the code.
func (l *Listener) Wait(ctx context.Context) (string, error) {
	select {
	case r := <-l.done:
		return r.code, r.err
	case <-ctx.Done():
		return "", fmt.Errorf("timed out waiting for the browser to return (%w)", ctx.Err())
	}
}

// Close stops serving. Safe to call more than once.
func (l *Listener) Close() {
	_ = l.srv.Close()
	// Serve may not have started yet, in which case srv.Close leaves the socket bound.
	_ = l.ln.Close()
}

func page(title, body string) string {
	return `<!doctype html><meta charset="utf-8"><title>` + title + `</title>
<style>body{font-family:system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1.5rem;color:#14181f}h1{font-size:1.5rem}p{color:#5b636f}</style>
<h1>` + title + `</h1><p>` + body + `</p>`
}
