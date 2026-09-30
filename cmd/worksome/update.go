package main

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/worksome/worksome-cli/internal/update"
)

// Vars so tests can stub the install method and the exec.
var (
	upgradeCommand = update.UpgradeCommand
	runUpgrade     = func(cmd *cobra.Command, argv []string) error {
		c := exec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
		c.Stdin, c.Stdout, c.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
		return c.Run()
	}
)

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Upgrade the CLI with the tool it was installed by",
		Long: `Upgrade the CLI with the tool it was installed by.

Runs brew upgrade --cask worksome for a Homebrew cask, or go install ...@latest
for a go install build. The CLI never replaces its own binary, so a manual
download has to be upgraded by downloading the latest release again.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			argv := upgradeCommand()
			if argv == nil {
				return fmt.Errorf("this install can't be upgraded automatically; download the latest release from %s", update.UpgradeHint())
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Running: %s\n", strings.Join(argv, " "))
			if err := runUpgrade(cmd, argv); err != nil {
				return fmt.Errorf("%s: %w", argv[0], err)
			}
			return nil
		},
	}
}
