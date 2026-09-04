package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/worksome/worksome-cli/internal/config"
)

// setupTestEnv points HOME at a temp dir, clears Worksome env vars, and writes
// a config with "default" and "stage" profiles pointing at a stub API that
// always rejects the token (a GraphQL error is not retried, so tests stay fast).
// Returns the config path and the stub endpoint URL.
func setupTestEnv(t *testing.T) (configPath, endpoint string) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"unauthorized"}]}`))
	}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WORKSOME_API_TOKEN", "")
	t.Setenv("WORKSOME_ENDPOINT", "")
	t.Setenv("WORKSOME_PROFILE", "")

	cfg := &config.Config{
		CurrentProfile: "default",
		Profiles: map[string]config.Profile{
			"default": {Token: "default-token-1234", Endpoint: srv.URL},
			"stage":   {Token: "stage-token-5678", Endpoint: srv.URL},
		},
	}

	path := filepath.Join(home, ".worksome", "config.yaml")
	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("saving test config: %v", err)
	}
	return path, srv.URL
}

func runRootCmd(args ...string) error {
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	return root.Execute()
}

// captureStdout captures os.Stdout while fn runs (auth status prints with fmt.Printf).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	fn()

	os.Stdout = old
	if err := w.Close(); err != nil {
		t.Fatalf("closing pipe writer: %v", err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}
	return buf.String()
}

func TestAuthStatusResolvesProfile(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		envProfile  string
		wantProfile string
		wantToken   string
	}{
		{
			name:        "profile flag beats config",
			args:        []string{"--profile", "stage", "auth", "status"},
			wantProfile: "stage",
			wantToken:   "stage-token-5678",
		},
		{
			name:        "WORKSOME_PROFILE env beats config",
			args:        []string{"auth", "status"},
			envProfile:  "stage",
			wantProfile: "stage",
			wantToken:   "stage-token-5678",
		},
		{
			name:        "profile flag beats env",
			args:        []string{"--profile", "default", "auth", "status"},
			envProfile:  "stage",
			wantProfile: "default",
			wantToken:   "default-token-1234",
		},
		{
			name:        "config current_profile is the fallback",
			args:        []string{"auth", "status"},
			wantProfile: "default",
			wantToken:   "default-token-1234",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setupTestEnv(t)
			if tc.envProfile != "" {
				t.Setenv("WORKSOME_PROFILE", tc.envProfile)
			}

			out := captureStdout(t, func() {
				if err := runRootCmd(tc.args...); err != nil {
					t.Errorf("Execute failed: %v", err)
				}
			})

			wantProfileLine := fmt.Sprintf("Profile:  %s\n", tc.wantProfile)
			if !strings.Contains(out, wantProfileLine) {
				t.Errorf("output = %q, want it to contain %q", out, wantProfileLine)
			}

			wantTokenLine := fmt.Sprintf("Token:    %s\n", config.MaskToken(tc.wantToken))
			if !strings.Contains(out, wantTokenLine) {
				t.Errorf("output = %q, want it to contain %q", out, wantTokenLine)
			}
		})
	}
}

func TestAuthLogoutResolvesProfile(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		envProfile  string
		wantRemoved string
		wantKept    string
		wantCurrent string
	}{
		{
			name:        "no arg uses profile flag",
			args:        []string{"--profile", "stage", "auth", "logout"},
			wantRemoved: "stage",
			wantKept:    "default",
			wantCurrent: "default",
		},
		{
			name:        "no arg uses WORKSOME_PROFILE env",
			args:        []string{"auth", "logout"},
			envProfile:  "stage",
			wantRemoved: "stage",
			wantKept:    "default",
			wantCurrent: "default",
		},
		{
			name:        "no arg falls back to config current_profile",
			args:        []string{"auth", "logout"},
			wantRemoved: "default",
			wantKept:    "stage",
			wantCurrent: "",
		},
		{
			name:        "positional arg beats profile flag",
			args:        []string{"--profile", "default", "auth", "logout", "stage"},
			wantRemoved: "stage",
			wantKept:    "default",
			wantCurrent: "default",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := setupTestEnv(t)
			if tc.envProfile != "" {
				t.Setenv("WORKSOME_PROFILE", tc.envProfile)
			}

			if err := runRootCmd(tc.args...); err != nil {
				t.Fatalf("Execute failed: %v", err)
			}

			cfg, err := config.LoadFrom(path)
			if err != nil {
				t.Fatalf("LoadFrom failed: %v", err)
			}

			if _, ok := cfg.Profiles[tc.wantRemoved]; ok {
				t.Errorf("profile %q should have been removed", tc.wantRemoved)
			}
			if _, ok := cfg.Profiles[tc.wantKept]; !ok {
				t.Errorf("profile %q should have been kept", tc.wantKept)
			}
			if cfg.CurrentProfile != tc.wantCurrent {
				t.Errorf("CurrentProfile = %q, want %q", cfg.CurrentProfile, tc.wantCurrent)
			}
		})
	}
}

func TestAuthLoginShorthandFlags(t *testing.T) {
	_, endpoint := setupTestEnv(t)

	// -p/-t must parse on auth login; the stub API rejects the token, proving
	// parsing succeeded and the failure happened at validation.
	err := runRootCmd("auth", "login", "-p", "test-profile", "-t", "dummy", "--endpoint", endpoint)
	if err == nil {
		t.Fatal("expected token validation error against stub endpoint")
	}
	if strings.Contains(err.Error(), "unknown shorthand flag") {
		t.Fatalf("shorthand flags did not parse: %v", err)
	}
	if !strings.Contains(err.Error(), "token validation failed") {
		t.Errorf("expected token validation failure, got: %v", err)
	}
}

// auth login and auth status once built their own client with the default
// agent, which carries no version — and an empty version silently drops the
// Apollo client-version header, so those calls went out unattributed.
func TestUserAgentCarriesVersionAndPlatform(t *testing.T) {
	ua := userAgent()

	name, rest, ok := strings.Cut(ua, "/")
	if !ok || name != "worksome-cli" {
		t.Fatalf("userAgent() = %q, want a worksome-cli/... prefix", ua)
	}

	ver, platform, ok := strings.Cut(rest, " ")
	if ver == "" {
		t.Errorf("userAgent() = %q, want a non-empty version", ua)
	}
	if !ok || !strings.HasPrefix(platform, "(") || !strings.Contains(platform, runtime.GOOS) {
		t.Errorf("userAgent() = %q, want the platform named as (%s/%s)", ua, runtime.GOOS, runtime.GOARCH)
	}
}
