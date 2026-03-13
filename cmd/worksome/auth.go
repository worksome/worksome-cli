package main

import (
	"bufio"
	"context"
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

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with a personal access token",
		Long: `Authenticate with the Worksome API using a Personal Access Token (PAT).

Create a token at: https://use.worksome.com/integrations/api-tokens

The token is stored in ~/.worksome/config.yaml with restricted file permissions.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			if profileName == "" {
				profileName = "default"
			}

			reader := bufio.NewReader(os.Stdin)

			// Prompt for token
			fmt.Fprint(os.Stderr, "Enter your Personal Access Token: ")
			var token string

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

			// Prompt for endpoint (optional)
			endpoint := "https://api.worksome.com/graphql"
			fmt.Fprintf(os.Stderr, "API endpoint [%s]: ", endpoint)
			line, err := reader.ReadString('\n')
			if err == nil {
				if ep := strings.TrimSpace(line); ep != "" {
					endpoint = ep
				}
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
			fmt.Fprintf(os.Stderr, "OK! Authenticated as %s (%s)\n", name, email)

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

	cmd.Flags().StringVar(&profileName, "profile", "default", "Profile name to save token under")
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
				fmt.Printf("Token:    %s\n", config.MaskToken(tokenFlag))
				fmt.Printf("Endpoint: %s\n", endpoint)

				c := client.New(endpoint, tokenFlag)
				var result map[string]any
				err := c.Execute(context.Background(), `query { viewer { name email } }`, nil, &result)
				if err != nil {
					fmt.Printf("Status:   Invalid or expired token (%v)\n", err)
					return nil
				}
				viewer, _ := result["viewer"].(map[string]any)
				name, _ := viewer["name"].(string)
				email, _ := viewer["email"].(string)
				fmt.Printf("User:     %s (%s)\n", name, email)
				fmt.Printf("Status:   Authenticated\n")
				return nil
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}

			profile, ok := cfg.ActiveProfile()
			if !ok {
				fmt.Println("Not authenticated. Run 'worksome auth login' to set up.")
				return nil
			}

			fmt.Printf("Profile:  %s\n", cfg.CurrentProfile)
			fmt.Printf("Token:    %s\n", config.MaskToken(profile.Token))
			fmt.Printf("Endpoint: %s\n", profile.Endpoint)

			// Try to fetch viewer info
			c := client.New(profile.Endpoint, profile.Token)
			var result map[string]any
			err = c.Execute(context.Background(), `query { viewer { name email } }`, nil, &result)
			if err != nil {
				fmt.Printf("Status:   Invalid or expired token (%v)\n", err)
				return nil
			}

			viewer, _ := result["viewer"].(map[string]any)
			name, _ := viewer["name"].(string)
			email, _ := viewer["email"].(string)
			fmt.Printf("User:     %s (%s)\n", name, email)
			fmt.Printf("Status:   Authenticated\n")
			return nil
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

			profileName := cfg.CurrentProfile
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
