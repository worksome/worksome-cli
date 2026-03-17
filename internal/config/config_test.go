package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestSaveToCreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "deep", "nested", "dir", "config.yaml")

	cfg := &Config{
		CurrentProfile: "test",
		Profiles: map[string]Profile{
			"test": {Token: "tok", Endpoint: "https://example.com"},
		},
	}

	if err := cfg.SaveTo(nested); err != nil {
		t.Fatalf("SaveTo should create parent dirs: %v", err)
	}

	loaded, err := LoadFrom(nested)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}
	if loaded.CurrentProfile != "test" {
		t.Errorf("CurrentProfile = %q, want %q", loaded.CurrentProfile, "test")
	}
}

func TestMaskTokenEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty string", "", ""},
		{"3 char fully masked", "abc", "***"},
		{"4 char fully masked", "abcd", "****"},
		{"5 char shows last 4", "abcde", "*bcde"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MaskToken(tc.input)
			if got != tc.want {
				t.Errorf("MaskToken(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestResolveEndpointEnvVarPrecedence(t *testing.T) {
	cfg := &Config{
		CurrentProfile: "prod",
		Profiles: map[string]Profile{
			"prod": {Endpoint: "https://config.example.com/graphql"},
		},
	}

	// Env var takes precedence over config
	t.Setenv("WORKSOME_ENDPOINT", "https://env.example.com/graphql")
	got := cfg.ResolveEndpoint("")
	if got != "https://env.example.com/graphql" {
		t.Errorf("ResolveEndpoint with env = %q, want env value", got)
	}

	// Flag takes precedence over env var
	got = cfg.ResolveEndpoint("https://flag.example.com/graphql")
	if got != "https://flag.example.com/graphql" {
		t.Errorf("ResolveEndpoint with flag = %q, want flag value", got)
	}
}

func TestLoadFromInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")

	if err := os.WriteFile(path, []byte(":\n\t: bad:\nyaml: [unclosed"), 0600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	_, err := LoadFrom(path)
	if err == nil {
		t.Fatal("LoadFrom should return an error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "parsing config file") {
		t.Errorf("error should mention parsing, got: %v", err)
	}
}

func TestResolveProfilePrecedence(t *testing.T) {
	cfg := &Config{
		CurrentProfile: "config-profile",
		Profiles:       map[string]Profile{},
	}

	// 1. Flag takes highest precedence.
	got := cfg.ResolveProfile("flag-profile")
	if got != "flag-profile" {
		t.Errorf("ResolveProfile with flag = %q, want %q", got, "flag-profile")
	}

	// 2. Env var beats config.
	t.Setenv("WORKSOME_PROFILE", "env-profile")
	got = cfg.ResolveProfile("")
	if got != "env-profile" {
		t.Errorf("ResolveProfile with env = %q, want %q", got, "env-profile")
	}

	// 3. Config file is the fallback.
	t.Setenv("WORKSOME_PROFILE", "")
	got = cfg.ResolveProfile("")
	if got != "config-profile" {
		t.Errorf("ResolveProfile from config = %q, want %q", got, "config-profile")
	}

	// 4. Empty when nothing is set.
	cfg.CurrentProfile = ""
	got = cfg.ResolveProfile("")
	if got != "" {
		t.Errorf("ResolveProfile with nothing = %q, want empty", got)
	}
}

func TestConfigPath(t *testing.T) {
	// configPath uses os.UserHomeDir which reads HOME on Unix.
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	path, err := configPath()
	if err != nil {
		t.Fatalf("configPath failed: %v", err)
	}

	expected := filepath.Join(dir, ".worksome", "config.yaml")
	if path != expected {
		t.Errorf("configPath() = %q, want %q", path, expected)
	}
}

func TestLoadCreatesDirectoryAndReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Profiles == nil {
		t.Fatal("Profiles should be initialized")
	}
	if len(cfg.Profiles) != 0 {
		t.Errorf("expected empty Profiles, got %d", len(cfg.Profiles))
	}

	// The config directory should have been created
	configDir := filepath.Join(dir, ".worksome")
	info, err := os.Stat(configDir)
	if err != nil {
		t.Fatalf("config directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected .worksome to be a directory")
	}
}

func TestLoadSaveViaHomedir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfg := &Config{
		CurrentProfile: "test",
		Profiles: map[string]Profile{
			"test": {Token: "secret-token", Endpoint: "https://example.com/graphql"},
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file permissions
	path := filepath.Join(dir, ".worksome", "config.yaml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}

	// Load back and verify
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.CurrentProfile != "test" {
		t.Errorf("CurrentProfile = %q, want %q", loaded.CurrentProfile, "test")
	}
	if p, ok := loaded.Profiles["test"]; !ok {
		t.Error("test profile not found")
	} else {
		if p.Token != "secret-token" {
			t.Errorf("Token = %q, want %q", p.Token, "secret-token")
		}
		if p.Endpoint != "https://example.com/graphql" {
			t.Errorf("Endpoint = %q, want %q", p.Endpoint, "https://example.com/graphql")
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
