package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/worksome/worksome-cli/internal/client"
	"github.com/worksome/worksome-cli/internal/config"
	"golang.org/x/term"
)

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

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with a personal access token",
		Long: `Authenticate with the Worksome API using a Personal Access Token (PAT).

Create a token at: https://use.worksome.com/integrations/api-tokens

The token is stored in ~/.worksome/config.yaml with restricted file permissions.`,
		Example: `  # Interactive login (prompts for token)
  worksome auth login

  # Non-interactive login (useful when paste doesn't work or for CI)
  worksome auth login --token <your-token>

  # Login with a custom endpoint and profile
  worksome auth login --token <your-token> --endpoint https://staging.worksome.com/graphql --profile staging

  # Pipe token from stdin
  echo "<your-token>" | worksome auth login`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			if profileName == "" {
				profileName = "default"
			}

			token := tokenFlag
			endpoint := endpointFlag

			// If token not provided via flag, prompt interactively
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

				// Prompt for endpoint if not provided via flag
				if endpoint == "" {
					endpoint = "https://api.worksome.com/graphql"
					fmt.Fprintf(os.Stderr, "API endpoint [%s]: ", endpoint)
					line, err := reader.ReadString('\n')
					if err == nil {
						if ep := strings.TrimSpace(line); ep != "" {
							endpoint = ep
						}
					}
				}
			}

			if endpoint == "" {
				endpoint = "https://api.worksome.com/graphql"
			}

			// Validate token by querying viewer
			fmt.Fprint(os.Stderr, "Validating token... ")
			c := client.New(endpoint, token)
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

			// Save to config
			if cfg.Profiles == nil {
				cfg.Profiles = make(map[string]config.Profile)
			}
			cfg.Profiles[profileName] = config.Profile{
				Token:    token,
				Endpoint: endpoint,
			}
			cfg.CurrentProfile = profileName

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Fprintf(os.Stderr, "Token saved to profile %q\n", profileName)
			return nil
		},
	}

	// Same shorthands as the root persistent flags these shadow, so -p/-t keep working
	cmd.Flags().StringVarP(&profileName, "profile", "p", "default", "Profile name to save token under")
	cmd.Flags().StringVarP(&tokenFlag, "token", "t", "", "Personal Access Token (skips interactive prompt)")
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
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			profileName := args[0]
			if _, ok := cfg.Profiles[profileName]; !ok {
				available := make([]string, 0, len(cfg.Profiles))
				for name := range cfg.Profiles {
					available = append(available, name)
				}
				return fmt.Errorf("profile %q not found. Available: %s", profileName, strings.Join(available, ", "))
			}

			cfg.CurrentProfile = profileName
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
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
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			// Resolve profile like the client factory: flag > WORKSOME_PROFILE > config
			profileFlag, _ := cmd.Root().PersistentFlags().GetString("profile")
			profileName := cfg.ResolveProfile(profileFlag)
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

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
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
	c := client.New(endpoint, token)
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
