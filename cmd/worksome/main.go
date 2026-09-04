// Command worksome is the CLI wrapper for the Worksome API.
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/worksome/worksome-cli/internal/client"
	"github.com/worksome/worksome-cli/internal/config"
	"github.com/worksome/worksome-cli/internal/generated/commands"
	"github.com/worksome/worksome-cli/internal/output"
	"github.com/worksome/worksome-cli/internal/update"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	// Start the update check alongside the command rather than before it, so
	// it never adds latency to the work the user actually asked for.
	latest := startUpdateCheck()

	rootCmd := newRootCmd()
	err := rootCmd.Execute()

	// Report the failure immediately. Waiting on a courtesy check first would
	// delay the error the user actually needs, by up to the whole fetch budget
	// on a cold cache.
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	printUpdateNotice(latest)
}

// startUpdateCheck kicks off the once-a-day release check in the background,
// returning a channel that yields the latest version, or nil when the check is
// suppressed. Nothing downstream ever blocks on it for long.
func startUpdateCheck() <-chan string {
	if update.Suppressed(version, output.IsTTYFile(os.Stdout), output.IsTTYFile(os.Stderr)) {
		return nil
	}
	ch := make(chan string, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), update.FetchTimeout)
		defer cancel()
		ch <- update.LatestCached(ctx, nil)
	}()
	return ch
}

// printUpdateNotice writes the notice once the check finishes.
//
// The check runs concurrently with the command, so for anything that touches
// the API it has long since completed and this returns immediately. The wait
// only bites on an instant command with a cold cache, once a day. It must be
// long enough for the fetch to land: cutting it short would kill the goroutine
// before it writes the cache, so the cache would never warm and the notice
// would never appear at all.
func printUpdateNotice(latest <-chan string) {
	if latest == nil {
		return
	}
	select {
	case v := <-latest:
		if update.IsNewer(version, v) {
			fmt.Fprint(os.Stderr, update.Notice(version, v))
		}
	// Outlast the fetch itself, or a slow response is cut off mid-write and the
	// cache never warms -- the notice would then never appear at all.
	case <-time.After(update.FetchTimeout + 500*time.Millisecond):
	}
}

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "worksome",
		Short:         "CLI wrapper for the Worksome API",
		Long:          "A multiplatform CLI for interacting with the Worksome GraphQL API. Designed for both human users and AI agents.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global flags
	rootCmd.PersistentFlags().StringP("profile", "p", "", "Config profile to use")
	rootCmd.PersistentFlags().StringP("token", "t", "", "API token (overrides config)")
	rootCmd.PersistentFlags().String("endpoint", "", "API endpoint URL (overrides config)")
	rootCmd.PersistentFlags().StringP("output", "o", "", "Output format: json, table")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display (e.g., id,name,status)")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Show request/response details")
	rootCmd.PersistentFlags().Bool("no-color", false, "Disable colored output")
	rootCmd.PersistentFlags().Bool("dry-run", false, "Show operation details without executing")
	rootCmd.PersistentFlags().Int("timeout", 30, "Request timeout in seconds")
	rootCmd.PersistentFlags().String("fields", "", "Comma-separated list of fields to request and display (e.g., id,name,worker.name)")
	rootCmd.PersistentFlags().String("filter", "", `Key=value filter pairs (e.g., "status=ACTIVE,currency=DKK")`)

	// Register shell completion for --profile flag
	_ = rootCmd.RegisterFlagCompletionFunc("profile", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		cfg, err := config.Load()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for name := range cfg.Profiles {
			names = append(names, name)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	})

	// Register shell completion for --output flag
	_ = rootCmd.RegisterFlagCompletionFunc("output", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"json", "table"}, cobra.ShellCompDirectiveNoFileComp
	})

	// Set up client factory for generated commands
	commands.SetClientFactory(func() (*client.Client, error) {
		cfg, err := config.Load()
		if err != nil {
			return nil, fmt.Errorf("loading config: %w", err)
		}

		timeout, _ := rootCmd.PersistentFlags().GetInt("timeout")
		if timeout < 0 {
			return nil, fmt.Errorf("--timeout must be non-negative (got %d)", timeout)
		}

		tokenFlag, _ := rootCmd.PersistentFlags().GetString("token")
		endpointFlag, _ := rootCmd.PersistentFlags().GetString("endpoint")
		profileFlag, _ := rootCmd.PersistentFlags().GetString("profile")

		cfg.CurrentProfile = cfg.ResolveProfile(profileFlag)

		token := cfg.ResolveToken(tokenFlag)
		endpoint := cfg.ResolveEndpoint(endpointFlag)

		if token == "" {
			return nil, fmt.Errorf("no API token configured. Run 'worksome auth login' to set up authentication")
		}

		verbose, _ := rootCmd.PersistentFlags().GetBool("verbose")
		opts := []client.Option{}
		opts = append(opts, client.WithUserAgent(fmt.Sprintf("worksome-cli/%s (%s/%s)", version, runtime.GOOS, runtime.GOARCH)))
		if verbose {
			opts = append(opts, client.WithVerbose(true))
		}
		if timeout > 0 {
			opts = append(opts, client.WithTimeout(time.Duration(timeout)*time.Second))
		}
		if fields, _ := rootCmd.PersistentFlags().GetString("fields"); fields != "" {
			opts = append(opts, client.WithFields(strings.Split(fields, ",")))
		}

		return client.New(endpoint, token, opts...), nil
	})

	// Command groups for organized help output
	rootCmd.AddGroup(
		&cobra.Group{ID: "auth", Title: "Authentication"},
		&cobra.Group{ID: "core", Title: "Core Resources"},
		&cobra.Group{ID: "recruitment", Title: "Recruitment & Talent"},
		&cobra.Group{ID: "finance", Title: "Finance & Billing"},
		&cobra.Group{ID: "admin", Title: "Administration"},
		&cobra.Group{ID: "other", Title: "Other Resources"},
	)

	// Register built-in commands
	rootCmd.AddCommand(newAuthCmd())
	rootCmd.AddCommand(newVersionCmd())
	rootCmd.AddCommand(newCompletionCmd())

	// Register all generated resource commands
	commands.RegisterAll(rootCmd)

	// Assign groups to commands based on name
	coreResources := map[string]bool{
		"hires": true, "jobs": true, "bids": true, "contracts": true,
		"timesheets": true, "projects": true, "milestones": true,
		"worker": true, "company": true,
		"conversations": true, "files": true, "viewer": true,
		"employments": true, "classifications": true, "compliance": true,
		"gate": true, "skills": true, "industries": true, "note": true,
	}
	financeResources := map[string]bool{
		"invoices": true, "invoice-row": true, "payment-requests": true,
		"batches": true, "batch-action": true, "bank-details": true,
	}
	adminResources := map[string]bool{
		"accounts": true, "organisation": true, "user-groups": true,
		"webhooks": true, "webhook-events": true, "webhook-event-logs": true,
		"multi-factors": true, "workflows": true, "workflow-variables": true,
		"approvals": true, "approval-rules": true, "approval-states": true,
		"approval-approvables": true, "approvers": true, "custom-fields": true,
		"inherited-custom-fields": true,
		"email":                   true, "password": true,
	}
	recruitmentResources := map[string]bool{
		"company-recruiters": true, "recruiter-candidates": true, "recruiters": true,
		"trusted-contacts": true, "organisation-trusted-contacts": true,
		"invite-link":              true,
		"reinvite-trusted-contact": true, "block-trusted-contact": true,
		"job-candidates": true, "job-candidate-preferred": true,
		"job-candidate-status": true, "job-shares": true, "partner": true,
		"accept-bid": true,
	}

	for _, cmd := range rootCmd.Commands() {
		name := cmd.Name()
		switch {
		case name == "auth":
			cmd.GroupID = "auth"
		case name == "version" || name == "completion" || name == "help":
			// leave ungrouped
		case coreResources[name]:
			cmd.GroupID = "core"
		case recruitmentResources[name]:
			cmd.GroupID = "recruitment"
		case financeResources[name]:
			cmd.GroupID = "finance"
		case adminResources[name]:
			cmd.GroupID = "admin"
		default:
			cmd.GroupID = "other"
		}
	}

	return rootCmd
}

func newVersionCmd() *cobra.Command {
	var check bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Long: `Print version information.

With --check, ask GitHub for the latest release and report whether this
build is out of date, along with the upgrade command for how it was
installed. This always performs a request, ignoring the once-a-day cache.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("worksome-cli %s (commit: %s)\n", version, commit)
			if !check {
				return nil
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			rel, err := update.Fetch(ctx, nil)
			if err != nil {
				return err
			}

			switch {
			case version == "dev":
				fmt.Printf("latest release: %s (this is a dev build)\n", rel.TagName)
			case update.IsNewer(version, rel.TagName):
				fmt.Printf("\nA new release is available: %s\n%s\n", rel.TagName, update.UpgradeHint())
			default:
				fmt.Println("up to date")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "Check whether a newer release is available")
	return cmd
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
