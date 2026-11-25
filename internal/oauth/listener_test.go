package oauth

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestListenerIgnoresForeignStateAndAcceptsOurs(t *testing.T) {
	l, err := NewListener("http://127.0.0.1:0/callback", "good-state")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	base := "http://" + l.Addr() + "/callback"

	// Wrong state: rejected, and the login keeps waiting.
	if status, _ := get(t, base+"?code=evil&state=bad-state"); status != http.StatusBadRequest {
		t.Errorf("foreign state: status %d, want 400", status)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	if _, err := l.Wait(ctx); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("a foreign-state redirect must not complete the login, got %v", err)
	}
	cancel()

	// Right state: the code comes through and the page sends the user back to the terminal.
	status, body := get(t, base+"?code=real&state=good-state")
	if status != http.StatusOK || !strings.Contains(body, "Return to the terminal") {
		t.Errorf("status %d body %q", status, body)
	}
	code, err := l.Wait(context.Background())
	if err != nil || code != "real" {
		t.Errorf("Wait() = %q, %v", code, err)
	}
}

func TestListenerReportsDenial(t *testing.T) {
	l, err := NewListener("http://127.0.0.1:0/callback", "s")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	get(t, "http://"+l.Addr()+"/callback?state=s&error=access_denied&error_description=The+user+denied+the+request")
	_, err = l.Wait(context.Background())
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Errorf("Wait() error = %v, want denial", err)
	}
}

func TestListenerRedirectURLNamesTheBoundPort(t *testing.T) {
	l, err := NewListener("http://127.0.0.1:0/callback", "s")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if got, want := l.RedirectURL(), "http://"+l.Addr()+"/callback"; got != want || strings.HasSuffix(l.Addr(), ":0") {
		t.Errorf("RedirectURL() = %q, want %q on a bound port", got, want)
	}
}

// Passport ignores a loopback redirect's port, so a taken port must not end the login.
func TestListenerFallsBackToAFreePortWhenTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()

	l, err := NewListener("http://"+taken.Addr().String()+"/callback", "s")
	if err != nil {
		t.Fatalf("NewListener() on a taken port = %v, want a fallback", err)
	}
	defer l.Close()

	u, err := url.Parse(l.RedirectURL())
	if err != nil {
		t.Fatal(err)
	}
	if u.Hostname() != "127.0.0.1" || u.Path != "/callback" || u.Host == taken.Addr().String() || u.Port() == "0" {
		t.Errorf("RedirectURL() = %q, want /callback on another bound port of 127.0.0.1", u)
	}
	get(t, l.RedirectURL()+"?code=c&state=s")
	if code, err := l.Wait(context.Background()); err != nil || code != "c" {
		t.Errorf("Wait() = %q, %v", code, err)
	}
}

func TestListenerFailsWhenTheHostCannotBeBound(t *testing.T) {
	// 192.0.2.0/24 is reserved for documentation, so no interface carries it.
	_, err := NewListener("http://192.0.2.1:51789/callback", "s")
	if err == nil || !strings.Contains(err.Error(), "cannot listen") {
		t.Errorf("NewListener() error = %v, want a clear bind failure", err)
	}
}
