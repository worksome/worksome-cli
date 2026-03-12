package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	original := &Config{
		CurrentProfile: "prod",
		Profiles: map[string]Profile{
			"prod": {
				Token:    "tok-prod-abc123",
				Endpoint: "https://prod.example.com/graphql",
			},
			"staging": {
				Token:    "tok-staging-xyz",
				Endpoint: "https://staging.example.com/graphql",
			},
		},
	}

	if err := original.SaveTo(path); err != nil {
		t.Fatalf("SaveTo failed: %v", err)
	}

	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}

	if loaded.CurrentProfile != original.CurrentProfile {
		t.Errorf("CurrentProfile = %q, want %q", loaded.CurrentProfile, original.CurrentProfile)
	}

	if len(loaded.Profiles) != len(original.Profiles) {
		t.Fatalf("Profiles count = %d, want %d", len(loaded.Profiles), len(original.Profiles))
	}

	for name, want := range original.Profiles {
		got, ok := loaded.Profiles[name]
		if !ok {
			t.Errorf("profile %q not found after round-trip", name)
			continue
		}
		if got.Token != want.Token {
			t.Errorf("profile %q token = %q, want %q", name, got.Token, want.Token)
		}
		if got.Endpoint != want.Endpoint {
			t.Errorf("profile %q endpoint = %q, want %q", name, got.Endpoint, want.Endpoint)
		}
	}
}

func TestLoadNonExistentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.yaml")

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom should not error for missing file: %v", err)
	}

	if cfg.Profiles == nil {
		t.Fatal("Profiles map should be initialized, got nil")
	}

	if len(cfg.Profiles) != 0 {
		t.Errorf("Profiles should be empty, got %d entries", len(cfg.Profiles))
	}
}

func TestActiveProfile(t *testing.T) {
	cfg := &Config{
		CurrentProfile: "dev",
		Profiles: map[string]Profile{
			"dev": {Token: "dev-token", Endpoint: "https://dev.example.com"},
		},
	}

	p, ok := cfg.ActiveProfile()
	if !ok {
		t.Fatal("ActiveProfile returned false, want true")
	}
	if p.Token != "dev-token" {
		t.Errorf("Token = %q, want %q", p.Token, "dev-token")
	}

	// No current profile set.
	cfg.CurrentProfile = ""
	_, ok = cfg.ActiveProfile()
	if ok {
		t.Error("ActiveProfile should return false when CurrentProfile is empty")
	}

	// Current profile references a non-existent name.
	cfg.CurrentProfile = "nonexistent"
	_, ok = cfg.ActiveProfile()
	if ok {
		t.Error("ActiveProfile should return false for missing profile")
	}
}

func TestResolveTokenPrecedence(t *testing.T) {
	cfg := &Config{
		CurrentProfile: "default",
		Profiles: map[string]Profile{
			"default": {Token: "config-token"},
		},
	}

	// 1. Flag takes highest precedence.
	got := cfg.ResolveToken("flag-token")
	if got != "flag-token" {
		t.Errorf("ResolveToken with flag = %q, want %q", got, "flag-token")
	}

	// 2. Env var beats config.
	t.Setenv("WORKSOME_API_TOKEN", "env-token")
	got = cfg.ResolveToken("")
	if got != "env-token" {
		t.Errorf("ResolveToken with env = %q, want %q", got, "env-token")
	}

	// 3. Config file is the fallback.
	t.Setenv("WORKSOME_API_TOKEN", "")
	got = cfg.ResolveToken("")
	if got != "config-token" {
		t.Errorf("ResolveToken from config = %q, want %q", got, "config-token")
	}

	// 4. Empty when nothing is set.
	cfg.CurrentProfile = ""
	got = cfg.ResolveToken("")
	if got != "" {
		t.Errorf("ResolveToken with nothing = %q, want empty", got)
	}
}

func TestResolveEndpointWithDefault(t *testing.T) {
	cfg := &Config{
		CurrentProfile: "custom",
		Profiles: map[string]Profile{
			"custom": {Endpoint: "https://custom.example.com/graphql"},
		},
	}

	// 1. Flag takes highest precedence.
	got := cfg.ResolveEndpoint("https://flag.example.com/graphql")
	if got != "https://flag.example.com/graphql" {
		t.Errorf("ResolveEndpoint with flag = %q, want flag value", got)
	}

	// 2. Env var beats config.
	t.Setenv("WORKSOME_ENDPOINT", "https://env.example.com/graphql")
	got = cfg.ResolveEndpoint("")
	if got != "https://env.example.com/graphql" {
		t.Errorf("ResolveEndpoint with env = %q, want env value", got)
	}

	// 3. Config file value.
	t.Setenv("WORKSOME_ENDPOINT", "")
	got = cfg.ResolveEndpoint("")
	if got != "https://custom.example.com/graphql" {
		t.Errorf("ResolveEndpoint from config = %q, want config value", got)
	}

	// 4. Default when nothing else is set.
	cfg.CurrentProfile = ""
	got = cfg.ResolveEndpoint("")
	if got != defaultEndpoint {
		t.Errorf("ResolveEndpoint default = %q, want %q", got, defaultEndpoint)
	}
}

func TestMaskToken(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"ab", "**"},
		{"abcd", "****"},
		{"abcde", "*bcde"}, // 5 chars: 1 star + last 4
		{"super-secret-token-1234", "*******************1234"},
	}

	for _, tc := range tests {
		got := MaskToken(tc.input)
		if got != tc.want {
			t.Errorf("MaskToken(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg := &Config{
		CurrentProfile: "test",
		Profiles: map[string]Profile{
			"test": {Token: "secret"},
		},
	}

	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("SaveTo failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}
}
