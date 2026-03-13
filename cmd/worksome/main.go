// Command worksome is the CLI wrapper for the Worksome API.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/worksome/worksome-cli/internal/client"
	"github.com/worksome/worksome-cli/internal/config"
	"github.com/worksome/worksome-cli/internal/generated/commands"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "worksome",
		Short: "CLI wrapper for the Worksome API",
		Long:  "A multiplatform CLI for interacting with the Worksome GraphQL API. Designed for both human users and AI agents.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global flags
	rootCmd.PersistentFlags().String("profile", "", "Config profile to use")
	rootCmd.PersistentFlags().String("token", "", "API token (overrides config)")
	rootCmd.PersistentFlags().String("endpoint", "", "API endpoint URL (overrides config)")
	rootCmd.PersistentFlags().String("output", "", "Output format: json, table")
	rootCmd.PersistentFlags().Bool("verbose", false, "Show request/response details")
	rootCmd.PersistentFlags().Bool("no-color", false, "Disable colored output")
	rootCmd.PersistentFlags().Bool("dry-run", false, "Show query without executing (mutations only)")
	rootCmd.PersistentFlags().Int("timeout", 30, "Request timeout in seconds")

	// Set up client factory for generated commands
	commands.SetClientFactory(func() (*client.Client, error) {
		cfg, err := config.Load()
		if err != nil {
			return nil, fmt.Errorf("loading config: %w", err)
		}

		tokenFlag, _ := rootCmd.PersistentFlags().GetString("token")
		endpointFlag, _ := rootCmd.PersistentFlags().GetString("endpoint")
		profileFlag, _ := rootCmd.PersistentFlags().GetString("profile")

		if profileFlag != "" {
			cfg.CurrentProfile = profileFlag
		}

		token := cfg.ResolveToken(tokenFlag)
		endpoint := cfg.ResolveEndpoint(endpointFlag)

		if token == "" {
			return nil, fmt.Errorf("no API token configured. Run 'worksome auth login' to set up authentication")
		}

		verbose, _ := rootCmd.PersistentFlags().GetBool("verbose")
		timeout, _ := rootCmd.PersistentFlags().GetInt("timeout")
		opts := []client.Option{}
		if verbose {
			opts = append(opts, client.WithVerbose(true))
		}
		if timeout > 0 {
			opts = append(opts, client.WithTimeout(time.Duration(timeout)*time.Second))
		}

		return client.New(endpoint, token, opts...), nil
	})

	// Register built-in commands
	rootCmd.AddCommand(newAuthCmd())
	rootCmd.AddCommand(newVersionCmd())
	rootCmd.AddCommand(newCompletionCmd())

	// Register all generated resource commands
	commands.RegisterAll(rootCmd)

	return rootCmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("worksome-cli %s (commit: %s)\n", version, commit)
		},
	}
}

func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for worksome CLI.

To load completions:

Bash:
  $ source <(worksome completion bash)

Zsh:
  $ source <(worksome completion zsh)

Fish:
  $ worksome completion fish | source

PowerShell:
  PS> worksome completion powershell | Out-String | Invoke-Expression`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return fmt.Errorf("unsupported shell: %s", args[0])
			}
		},
	}
	return cmd
}
