package oauth

import (
	"context"
	"io"
	"net/http"
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

	// Right state: the code comes through and the user sees a confirmation.
	status, body := get(t, base+"?code=real&state=good-state")
	if status != http.StatusOK || !strings.Contains(body, "Connected") {
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

func TestListenerFailsFastWhenPortIsTaken(t *testing.T) {
	first, err := NewListener("http://127.0.0.1:0/callback", "s")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	_, err = NewListener("http://"+first.Addr()+"/callback", "s")
	if err == nil || !strings.Contains(err.Error(), "cannot listen") {
		t.Errorf("second listener on the same port should fail clearly, got %v", err)
	}
}
