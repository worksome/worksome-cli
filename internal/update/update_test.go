package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"0.4.0", "v0.4.1", true},
		{"v0.4.0", "0.4.1", true},
		{"0.4.1", "v0.4.1", false},
		{"v0.4.1", "v0.4.1", false},
		// A dev build must never be nagged: it has no meaningful version.
		{"dev", "v0.4.1", false},
		// A local build ahead of the latest release has nothing to do either.
		{"0.5.0", "v0.4.1", false},
		{"0.4.2", "v0.4.1", false},
		{"", "v0.4.1", false},
		{"0.4.0", "", false},
		// Ordering, not string inequality: 10 > 9.
		{"0.9.0", "v0.10.0", true},
		{"0.10.0", "v0.9.0", false},
		{"1.0.0", "v1.0.1", true},
		// Missing components count as zero.
		{"1.2", "v1.2.0", false},
		{"1.2", "v1.2.1", true},
		// Pre-release tags don't parse, so we never guess.
		{"0.4.0", "v0.5.0-rc1", false},
		{"0.4.0-rc1", "v0.5.0", false},
	}
	for _, tt := range tests {
		if got := IsNewer(tt.current, tt.latest); got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

// The notice must never reach a non-interactive consumer: an agent parsing
// JSON, a pipeline, or CI.
func TestSuppressed(t *testing.T) {
	tests := []struct {
		name              string
		version           string
		stdoutTTY, stderr bool
		env               map[string]string
		want              bool
	}{
		{name: "interactive release build runs", version: "0.4.0", stdoutTTY: true, stderr: true, want: false},
		{name: "piped stdout suppresses", version: "0.4.0", stdoutTTY: false, stderr: true, want: true},
		{name: "redirected stderr suppresses", version: "0.4.0", stdoutTTY: true, stderr: false, want: true},
		{name: "dev build suppresses", version: "dev", stdoutTTY: true, stderr: true, want: true},
		{name: "opt-out suppresses", version: "0.4.0", stdoutTTY: true, stderr: true,
			env: map[string]string{"WORKSOME_NO_UPDATE_CHECK": "1"}, want: true},
		{name: "CI suppresses", version: "0.4.0", stdoutTTY: true, stderr: true,
			env: map[string]string{"CI": "true"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WORKSOME_NO_UPDATE_CHECK", "")
			t.Setenv("CI", "")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if got := Suppressed(tt.version, tt.stdoutTTY, tt.stderr); got != tt.want {
				t.Errorf("Suppressed() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Advice that names the wrong install method is worse than no advice.
func TestHintForPath(t *testing.T) {
	gopath := t.TempDir()
	t.Setenv("GOPATH", gopath)

	tests := map[string]string{
		filepath.Join("/opt/homebrew/Caskroom/worksome/0.4.1", "worksome"):   "brew upgrade --cask worksome",
		filepath.Join("/usr/local/Caskroom/worksome/0.4.1", "worksome"):      "brew upgrade --cask worksome",
		filepath.Join("/opt/homebrew/Cellar/worksome/0.4.1/bin", "worksome"): "brew upgrade worksome",
		filepath.Join(gopath, "bin", "worksome"):                             "go install github.com/worksome/worksome-cli/cmd/worksome@latest",
		filepath.Join("/usr/local/bin", "worksome"):                          downloadURL,
		filepath.Join("/w", "worksome"):                                      downloadURL,
	}
	for path, want := range tests {
		if got := hintForPath(path); got != want {
			t.Errorf("hintForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

// A directory merely *named* like the gopath bin must not match.
func TestHintForPathDoesNotMatchSiblingDirectory(t *testing.T) {
	gopath := t.TempDir()
	t.Setenv("GOPATH", gopath)

	sibling := gopath + "-other"
	if got := hintForPath(filepath.Join(sibling, "bin", "worksome")); got != downloadURL {
		t.Errorf("a sibling of GOPATH should not be treated as a go install, got %q", got)
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Release{TagName: "v9.9.9"})
	}))
	defer srv.Close()

	prev := releasesURL
	releasesURL = srv.URL
	defer func() { releasesURL = prev }()

	rel, err := Fetch(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rel.TagName != "v9.9.9" {
		t.Errorf("TagName = %q, want v9.9.9", rel.TagName)
	}
}

// GitHub rate limits unauthenticated callers. That must surface as an error to
// `version --check`, not as a bogus "up to date".
func TestFetchRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limit exceeded", http.StatusForbidden)
	}))
	defer srv.Close()

	prev := releasesURL
	releasesURL = srv.URL
	defer func() { releasesURL = prev }()

	if _, err := Fetch(context.Background(), srv.Client()); err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}

func TestLatestCachedUsesCacheAndAvoidsNetwork(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(Release{TagName: "v9.9.9"})
	}))
	defer srv.Close()

	prev := releasesURL
	releasesURL = srv.URL
	defer func() { releasesURL = prev }()

	if got := LatestCached(context.Background(), srv.Client()); got != "v9.9.9" {
		t.Fatalf("first call = %q, want v9.9.9", got)
	}
	if got := LatestCached(context.Background(), srv.Client()); got != "v9.9.9" {
		t.Fatalf("second call = %q, want v9.9.9", got)
	}
	if hits != 1 {
		t.Errorf("made %d requests, want 1: the second call must come from cache", hits)
	}
}

func TestLatestCachedRefetchesWhenStale(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	stale := cache{CheckedAt: time.Now().Add(-checkInterval - time.Hour), LatestVersion: "v0.0.1"}
	data, _ := json.Marshal(stale)
	dir := filepath.Join(home, ".worksome")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, cacheFile), data, 0600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Release{TagName: "v9.9.9"})
	}))
	defer srv.Close()

	prev := releasesURL
	releasesURL = srv.URL
	defer func() { releasesURL = prev }()

	if got := LatestCached(context.Background(), srv.Client()); got != "v9.9.9" {
		t.Errorf("stale cache should be refreshed, got %q", got)
	}
}

// A courtesy check must never become an error the user has to deal with, and
// must never be the reason a command fails in a container.
func TestLatestCachedSurvivesNoHomeDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("os.UserHomeDir resolves without HOME on this platform")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Release{TagName: "v9.9.9"})
	}))
	defer srv.Close()

	prev := releasesURL
	releasesURL = srv.URL
	defer func() { releasesURL = prev }()

	// No cache is possible, but the fetch itself must still work and not panic.
	if got := LatestCached(context.Background(), srv.Client()); got != "v9.9.9" {
		t.Errorf("got %q, want v9.9.9", got)
	}
}

func TestLatestCachedSwallowsNetworkFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	prev := releasesURL
	releasesURL = "http://127.0.0.1:1" // nothing listening
	defer func() { releasesURL = prev }()

	if got := LatestCached(context.Background(), &http.Client{Timeout: time.Second}); got != "" {
		t.Errorf("got %q, want empty on failure", got)
	}
	// The failure is cached so a broken network doesn't mean a request per run.
	if _, err := os.Stat(filepath.Join(home, ".worksome", cacheFile)); err != nil {
		t.Errorf("failure should still be cached: %v", err)
	}
}

func TestNoticeIncludesBothVersions(t *testing.T) {
	got := Notice("0.4.0", "v0.4.1")
	for _, want := range []string{"0.4.0", "0.4.1"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice should mention %s, got: %q", want, got)
		}
	}
	if strings.Contains(got, "v0.4.1") {
		t.Errorf("notice should normalise the v prefix, got: %q", got)
	}
}
