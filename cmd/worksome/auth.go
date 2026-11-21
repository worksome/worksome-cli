package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/worksome/worksome-cli/internal/buildinfo"
	"github.com/worksome/worksome-cli/internal/client"
	"github.com/worksome/worksome-cli/internal/config"
	"github.com/worksome/worksome-cli/internal/oauth"
	"golang.org/x/term"
)

// loginTimeout bounds how long `auth login` waits for the browser.
const loginTimeout = 5 * time.Minute

// loginEndpoint picks the API endpoint for `auth login` with the CLI's usual
// precedence: --endpoint, then WORKSOME_ENDPOINT, then the endpoint already
// stored on the profile being (re)written, then the production default.
func loginEndpoint(cfg *config.Config, profileName, flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv("WORKSOME_ENDPOINT"); v != "" {
		return v
	}
	if p, ok := cfg.Profiles[profileName]; ok && p.Endpoint != "" {
		return p.Endpoint
	}
	return config.DefaultEndpoint
}

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newAuthLoginCmd())
	cmd.AddCommand(newAuthStatusCmd())
	cmd.AddCommand(newAuthSwitchCmd())
	cmd.AddCommand(newAuthLogoutCmd())
	cmd.AddCommand(newAuthListCmd())

	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	var profileName string
	var tokenFlag string
	var endpointFlag string
	var usePAT bool
	var useBrowser bool
	var noBrowser bool

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in with a personal access token, or through your browser",
		Long: `Sign in to the Worksome API.

By default this asks for a Personal Access Token: pass --token, pipe it on
stdin, set WORKSOME_API_TOKEN, or type it at the hidden prompt. Create one at:
https://use.worksome.com/integrations/api-tokens

With --browser it opens your browser instead: you sign in to Worksome as usual
(SSO and MFA included) and approve the CLI. The session renews itself while you
keep using it and expires after 90 days without use. The browser must run on
the same machine as the CLI, because Worksome sends it back to 127.0.0.1.

Credentials are stored in ~/.worksome/config.yaml with restricted permissions.`,
		Example: `  # Prompt for a personal access token
  worksome auth login

  # Personal access token: non-interactive, for CI and scheduled jobs
  worksome auth login --token <your-token>
  echo "<your-token>" | worksome auth login

  # Sign in through the browser
  worksome auth login --browser

  # Browser sign-in, printing the URL instead of opening a browser
  worksome auth login --no-browser

  # A second profile against another platform
  worksome auth login --profile staging --endpoint https://staging.worksome.com/web-api/v2/graphql`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			if profileName == "" {
				profileName = "default"
			}

			token := tokenFlag
			// Resolve the endpoint the same way every other command does
			// (flag, WORKSOME_ENDPOINT, then the profile being written), so
			// signing in against another platform validates against that
			// platform rather than production.
			endpoint := loginEndpoint(cfg, profileName, endpointFlag)
			var session *config.Profile

			// Browser sign-in is opt-in; --no-browser implies it.
			if useBrowser || noBrowser {
				// The new session token is validated against endpoint, so it must not travel in clear.
				if _, err := oauth.CheckURL(endpoint); err != nil {
					return fmt.Errorf("API endpoint: %w", err)
				}
				ocfg := oauthConfig(endpoint)
				open := oauth.OpenBrowser
				if noBrowser {
					open = nil
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), loginTimeout)
				defer cancel()
				tok, err := oauth.Login(ctx, ocfg, open, os.Stderr)
				if err != nil {
					return fmt.Errorf("sign-in failed: %w", err)
				}
				p := sessionProfile(endpoint, ocfg, tok)
				session = &p
				token = tok.AccessToken
			}

			// Otherwise a personal access token: prompt for it, or read it
			// from a non-terminal stdin.
			if token == "" {
				reader := bufio.NewReader(os.Stdin)

				fmt.Fprint(os.Stderr, "Enter your Personal Access Token (input is hidden): ")

				// Try to read securely (no echo)
				if term.IsTerminal(int(os.Stdin.Fd())) {
					tokenBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
					if err != nil {
						return fmt.Errorf("reading token: %w", err)
					}
					token = string(tokenBytes)
					fmt.Fprintln(os.Stderr) // newline after hidden input
				} else {
					// Non-interactive: read from stdin
					line, err := reader.ReadString('\n')
					if err == nil {
						token = strings.TrimSpace(line)
					}
				}

				if token == "" {
					return fmt.Errorf("token cannot be empty")
				}

				// Confirm the endpoint unless it was given explicitly
				if endpointFlag == "" && os.Getenv("WORKSOME_ENDPOINT") == "" {
					fmt.Fprintf(os.Stderr, "API endpoint [%s]: ", endpoint)
					line, err := reader.ReadString('\n')
					if err == nil {
						if ep := strings.TrimSpace(line); ep != "" {
							endpoint = ep
						}
					}
				}
			}

			// Validate token by querying viewer
			fmt.Fprint(os.Stderr, "Validating token... ")
			c := client.New(endpoint, token, client.WithUserAgent(client.UserAgent(buildinfo.Version)))
			var result map[string]any
			err = c.Execute(context.Background(), `query { viewer { name email } }`, nil, &result)
			if err != nil {
				fmt.Fprintln(os.Stderr, "failed!")
				return fmt.Errorf("token validation failed: %w", err)
			}

			viewer, _ := result["viewer"].(map[string]any)
			name, _ := viewer["name"].(string)
			email, _ := viewer["email"].(string)
			if name != "" || email != "" {
				fmt.Fprintf(os.Stderr, "OK! Authenticated as %s (%s)\n", name, email)
			} else {
				fmt.Fprintln(os.Stderr, "OK!")
			}

			profile := config.Profile{Token: token, Endpoint: endpoint}
			if session != nil {
				profile = *session
			}
			// Re-read under the lock: a renewal since the load above must not be written back over.
			if err := config.Update(func(c *config.Config) error {
				c.Profiles[profileName] = profile
				c.CurrentProfile = profileName
				return nil
			}); err != nil {
				return err
			}

			if session != nil {
				fmt.Fprintf(os.Stderr, "Signed in. Session saved to profile %q; it renews automatically while in use.\n", profileName)
			} else {
				fmt.Fprintf(os.Stderr, "Token saved to profile %q\n", profileName)
			}
			return nil
		},
	}

	// Same shorthands as the root persistent flags these shadow, so -p/-t keep working
	cmd.Flags().StringVarP(&profileName, "profile", "p", "default", "Profile name to save the credentials under")
	cmd.Flags().StringVarP(&tokenFlag, "token", "t", "", "Personal Access Token")
	cmd.Flags().BoolVar(&usePAT, "pat", false, "Prompt for a Personal Access Token (the default)")
	cmd.Flags().BoolVar(&useBrowser, "browser", false, "Sign in through the browser instead of with a Personal Access Token")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Sign in through the browser, printing the URL instead of opening one")
	for _, browserFlag := range []string{"browser", "no-browser"} {
		cmd.MarkFlagsMutuallyExclusive(browserFlag, "token")
		cmd.MarkFlagsMutuallyExclusive(browserFlag, "pat")
	}
	cmd.Flags().StringVar(&endpointFlag, "endpoint", "", "API endpoint URL (default: https://api.worksome.com/graphql)")
	return cmd
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current authentication status",
		RunE: func(cmd *cobra.Command, args []string) error {
			tokenFlag, _ := cmd.Root().PersistentFlags().GetString("token")
			endpointFlag, _ := cmd.Root().PersistentFlags().GetString("endpoint")

			if tokenFlag != "" {
				// Use the provided token directly
				endpoint := endpointFlag
				if endpoint == "" {
					endpoint = "https://api.worksome.com/graphql"
				}
				fmt.Printf("Source:   --token flag\n")
				fmt.Printf("Token:    %s\n", config.MaskToken(tokenFlag))
				fmt.Printf("Endpoint: %s\n", endpoint)
				return printViewerStatus(endpoint, tokenFlag)
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}

			// Resolve profile like the client factory: flag > WORKSOME_PROFILE > config
			profileFlag, _ := cmd.Root().PersistentFlags().GetString("profile")
			cfg.CurrentProfile = cfg.ResolveProfile(profileFlag)

			// Check if env vars are overriding profile settings
			envToken := os.Getenv("WORKSOME_API_TOKEN")
			envEndpoint := os.Getenv("WORKSOME_ENDPOINT")

			profile, ok := cfg.ActiveProfile()
			if !ok && envToken == "" {
				fmt.Fprintln(os.Stderr, "Not authenticated. Run 'worksome auth login' to set up.")
				return fmt.Errorf("not authenticated")
			}

			token := ""
			endpoint := ""
			if ok {
				token = profile.Token
				endpoint = profile.Endpoint
			}

			fmt.Printf("Profile:  %s\n", cfg.CurrentProfile)

			if envToken != "" {
				fmt.Printf("Token:    %s (from WORKSOME_API_TOKEN)\n", config.MaskToken(envToken))
				token = envToken
			} else {
				fmt.Printf("Token:    %s\n", config.MaskToken(token))
				if profile.IsOAuth() {
					if exp, ok := profile.Expiry(); ok {
						fmt.Printf("Auth:     browser sign-in, renews automatically (current token expires %s)\n", exp.Local().Format("2 Jan 2006"))
					} else {
						fmt.Printf("Auth:     browser sign-in, renews automatically\n")
					}
				} else {
					fmt.Printf("Auth:     personal access token\n")
				}
			}

			if envEndpoint != "" {
				fmt.Printf("Endpoint: %s (from WORKSOME_ENDPOINT)\n", envEndpoint)
				endpoint = envEndpoint
			} else {
				fmt.Printf("Endpoint: %s\n", endpoint)
			}

			return printViewerStatus(endpoint, token)
		},
	}
}

func newAuthSwitchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "switch <profile>",
		Short: "Switch to a different profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profileName := args[0]
			if err := config.Update(func(cfg *config.Config) error {
				if _, ok := cfg.Profiles[profileName]; !ok {
					available := make([]string, 0, len(cfg.Profiles))
					for name := range cfg.Profiles {
						available = append(available, name)
					}
					return fmt.Errorf("profile %q not found. Available: %s", profileName, strings.Join(available, ", "))
				}
				cfg.CurrentProfile = profileName
				return nil
			}); err != nil {
				return err
			}

			fmt.Printf("Switched to profile %q\n", profileName)
			return nil
		},
	}
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout [profile]",
		Short: "Remove a profile and its stored credentials",
		Long:  "Remove a profile from the configuration. Defaults to the current profile if no name is given.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var profileName string
			if err := config.Update(func(cfg *config.Config) error {
				// Resolve profile like the client factory: flag > WORKSOME_PROFILE > config
				profileFlag, _ := cmd.Root().PersistentFlags().GetString("profile")
				profileName = cfg.ResolveProfile(profileFlag)
				if len(args) > 0 {
					profileName = args[0]
				}

				if profileName == "" {
					return fmt.Errorf("no profile specified and no current profile set")
				}

				if _, ok := cfg.Profiles[profileName]; !ok {
					return fmt.Errorf("profile %q not found", profileName)
				}

				delete(cfg.Profiles, profileName)
				if cfg.CurrentProfile == profileName {
					cfg.CurrentProfile = ""
				}
				return nil
			}); err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "Profile %q removed\n", profileName)
			return nil
		},
	}
}

func newAuthListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all configured profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			if len(cfg.Profiles) == 0 {
				fmt.Println("No profiles configured. Run 'worksome auth login' to set up.")
				return nil
			}

			outputFlag, _ := cmd.Root().PersistentFlags().GetString("output")
			if outputFlag == "json" {
				type profileInfo struct {
					Name     string `json:"name"`
					Endpoint string `json:"endpoint"`
					Active   bool   `json:"active"`
				}
				var profiles []profileInfo
				for name, profile := range cfg.Profiles {
					profiles = append(profiles, profileInfo{
						Name:     name,
						Endpoint: profile.Endpoint,
						Active:   name == cfg.CurrentProfile,
					})
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(profiles)
			}

			for name, profile := range cfg.Profiles {
				marker := "  "
				if name == cfg.CurrentProfile {
					marker = "* "
				}
				fmt.Printf("%s%s (endpoint: %s)\n", marker, name, profile.Endpoint)
			}
			return nil
		},
	}
}

func printViewerStatus(endpoint, token string) error {
	c := client.New(endpoint, token, client.WithUserAgent(client.UserAgent(buildinfo.Version)))
	var result map[string]any
	err := c.Execute(context.Background(), `query { viewer { name email } }`, nil, &result)
	if err != nil {
		fmt.Printf("Status:   Invalid or expired token (%v)\n", err)
		return nil
	}

	viewer, _ := result["viewer"].(map[string]any)
	name, _ := viewer["name"].(string)
	email, _ := viewer["email"].(string)
	if name != "" || email != "" {
		fmt.Printf("User:     %s (%s)\n", name, email)
	}
	fmt.Printf("Status:   Authenticated\n")
	return nil
}
