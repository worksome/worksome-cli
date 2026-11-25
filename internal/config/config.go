package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultEndpoint = DefaultEndpoint
	configDir       = ".worksome"
	configFile      = "config.yaml"
	lockFile        = "config.lock"
	envToken        = "WORKSOME_API_TOKEN"
	envEndpoint     = "WORKSOME_ENDPOINT"
	envProfile      = "WORKSOME_PROFILE"
)

// Config holds all CLI configuration, including named profiles.
type Config struct {
	Profiles       map[string]Profile `yaml:"profiles"`
	CurrentProfile string             `yaml:"current_profile"`
}

// Profile stores credentials and endpoint for a single environment.
//
// Token is always the bearer token sent to the API. For a personal access
// token that is all there is. For an OAuth session (browser login) the
// profile also carries the refresh token and the access token's expiry, so
// the CLI can renew it without asking the user to sign in again.
type Profile struct {
	Token        string `yaml:"token,omitempty"`
	Endpoint     string `yaml:"endpoint,omitempty"`
	RefreshToken string `yaml:"refresh_token,omitempty"`
	ExpiresAt    string `yaml:"expires_at,omitempty"` // RFC 3339, UTC
	// OAuthTokenURL and OAuthClientID record where the session was issued, so it is renewed there.
	OAuthTokenURL string `yaml:"oauth_token_url,omitempty"`
	OAuthClientID string `yaml:"oauth_client_id,omitempty"`
}

// IsOAuth reports whether the profile holds a browser-login session rather
// than a personal access token.
func (p Profile) IsOAuth() bool {
	return p.RefreshToken != ""
}

// Expiry returns when the access token expires. ok is false when the profile
// carries no (or an unreadable) expiry.
func (p Profile) Expiry() (expiry time.Time, ok bool) {
	if p.ExpiresAt == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, p.ExpiresAt)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// SetSession records an OAuth session on the profile.
func (p *Profile) SetSession(accessToken, refreshToken string, expiresAt time.Time) {
	p.Token = accessToken
	p.RefreshToken = refreshToken
	if expiresAt.IsZero() {
		p.ExpiresAt = ""
	} else {
		p.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	}
}

// configPath returns the full path to the config file.
func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, configDir, configFile), nil
}

// Load reads the config from ~/.worksome/config.yaml.
// If the file does not exist, it returns a zero-value Config (no error).
//
// Loading never writes to disk and never fails on an unresolvable home
// directory: a container may have no HOME, or a read-only one, while still
// supplying a token via --token or WORKSOME_API_TOKEN. Save creates the
// directory when there is actually something to persist.
func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return &Config{Profiles: map[string]Profile{}}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Profiles: map[string]Profile{}}, nil
		}
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}

	return &cfg, nil
}

// LoadFrom reads config from an arbitrary path (useful for testing).
func LoadFrom(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Profiles: map[string]Profile{}}, nil
		}
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}

	return &cfg, nil
}

// Save writes the config to ~/.worksome/config.yaml with 0600 permissions.
func (c *Config) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}

	return c.SaveTo(path)
}

// SaveTo writes the config to an arbitrary path (useful for testing).
func (c *Config) SaveTo(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}

	path, err = linkTarget(path)
	if err != nil {
		return err
	}

	// Write a 0600 temp file and rename it over the config, so a concurrent reader never sees half a file.
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing config file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing config file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replacing config file: %w", err)
	}
	// Best-effort: persist the rename across a crash; not every platform can sync a directory.
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}

	return nil
}

// linkTarget returns the file a symlinked config, such as one from dotfiles, points to, so a save writes that file and keeps the link.
func linkTarget(path string) (string, error) {
	target, err := filepath.EvalSymlinks(path)
	if err == nil {
		return target, nil
	}
	if info, lerr := os.Lstat(path); errors.Is(lerr, fs.ErrNotExist) {
		return path, nil
	} else if lerr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a symlink to a missing file; create that file or remove the link: %w", path, err)
	}
	return "", fmt.Errorf("resolving config path: %w", err)
}

// Update applies fn to a freshly loaded config and saves it, all under the config lock, so concurrent writers do not undo each other.
func Update(fn func(*Config) error) error {
	unlock, err := Lock()
	if err != nil {
		return err
	}
	defer unlock()

	cfg, err := Load()
	if err != nil {
		return err
	}
	if err := fn(cfg); err != nil {
		return err
	}
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	return nil
}

// Lock takes an exclusive advisory lock next to the config file and returns its release.
func Lock() (unlock func(), err error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	return LockAt(filepath.Join(filepath.Dir(path), lockFile))
}

// LockAt takes an exclusive advisory lock on path, blocking until it is free.
func LockAt(path string) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("creating config directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}
	if err := lockFileExclusive(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
	return func() {
		_ = unlockFile(f)
		_ = f.Close()
	}, nil
}

// ActiveProfile returns the profile indicated by CurrentProfile.
// It returns a zero-value Profile and false if no profile is set or the
// named profile does not exist.
func (c *Config) ActiveProfile() (Profile, bool) {
	if c.CurrentProfile == "" {
		return Profile{}, false
	}

	p, ok := c.Profiles[c.CurrentProfile]
	return p, ok
}

// ResolveProfile determines the active profile name with the following precedence:
//
//  1. flagValue (explicit CLI flag, e.g. --profile)
//  2. WORKSOME_PROFILE environment variable
//  3. CurrentProfile from the config file
func (c *Config) ResolveProfile(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv(envProfile); v != "" {
		return v
	}
	return c.CurrentProfile
}

// ResolveToken determines the API token with the following precedence:
//
//  1. flagValue (explicit CLI flag, e.g. --token)
//  2. WORKSOME_API_TOKEN environment variable
//  3. Token from the active profile in the config file
func (c *Config) ResolveToken(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}

	if v := os.Getenv(envToken); v != "" {
		return v
	}

	if p, ok := c.ActiveProfile(); ok {
		return p.Token
	}

	return ""
}

// ResolveEndpoint determines the API endpoint with the following precedence:
//
//  1. flagValue (explicit CLI flag, e.g. --endpoint)
//  2. WORKSOME_ENDPOINT environment variable
//  3. Endpoint from the active profile in the config file
//  4. Default endpoint (https://api.worksome.com/graphql)
func (c *Config) ResolveEndpoint(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}

	if v := os.Getenv(envEndpoint); v != "" {
		return v
	}

	if p, ok := c.ActiveProfile(); ok && p.Endpoint != "" {
		return p.Endpoint
	}

	return defaultEndpoint
}

// DefaultEndpoint is the production GraphQL endpoint used when nothing else
// names one.
const DefaultEndpoint = "https://api.worksome.com/graphql"

// MaskToken masks a token for display as a fixed-width prefix of four stars
// followed by its last 4 characters, whatever its length. OAuth access tokens
// run to ~1 KB, so a one-star-per-character mask would swamp the output.
// Tokens with 4 or fewer characters are fully masked.
func MaskToken(token string) string {
	if token == "" {
		return ""
	}

	if len(token) <= 4 {
		return strings.Repeat("*", len(token))
	}

	return "****" + token[len(token)-4:]
}
