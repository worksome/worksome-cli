package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultEndpoint = "https://api.worksome.com/graphql"
	configDir       = ".worksome"
	configFile      = "config.yaml"
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
type Profile struct {
	Token    string `yaml:"token,omitempty"`
	Endpoint string `yaml:"endpoint,omitempty"`
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

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
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

// MaskToken masks all but the last 4 characters of a token for display.
// Tokens with 4 or fewer characters are fully masked.
func MaskToken(token string) string {
	if token == "" {
		return ""
	}

	if len(token) <= 4 {
		return strings.Repeat("*", len(token))
	}

	return strings.Repeat("*", len(token)-4) + token[len(token)-4:]
}
