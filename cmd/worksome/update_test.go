package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestUpdateRunsInstallersUpgradeCommand(t *testing.T) {
	var ran []string
	stubUpgrade(t, []string{"brew", "upgrade", "--cask", "worksome"}, func(_ *cobra.Command, argv []string) error {
		ran = argv
		return nil
	})

	cmd := newUpdateCmd()
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"brew", "upgrade", "--cask", "worksome"}; !reflect.DeepEqual(ran, want) {
		t.Errorf("ran %v, want %v", ran, want)
	}
	if !strings.Contains(stderr.String(), "Running: brew upgrade --cask worksome") {
		t.Errorf("stderr = %q, want the command announced", stderr.String())
	}
}

func TestUpdateRefusesManualInstall(t *testing.T) {
	stubUpgrade(t, nil, func(*cobra.Command, []string) error {
		t.Fatal("nothing should run for a manual install")
		return nil
	})

	cmd := newUpdateCmd()
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "download the latest release") {
		t.Fatalf("err = %v, want a pointer to the download", err)
	}
}

func TestUpdateDryRunRunsNothing(t *testing.T) {
	stubUpgrade(t, []string{"brew", "upgrade", "--cask", "worksome"}, func(*cobra.Command, []string) error {
		t.Fatal("--dry-run must not run the upgrade")
		return nil
	})

	root := newRootCmd()
	var stderr bytes.Buffer
	root.SetErr(&stderr)
	root.SetArgs([]string{"update", "--dry-run"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "Would run: brew upgrade --cask worksome") {
		t.Errorf("stderr = %q, want the command it would run", stderr.String())
	}
}

func stubUpgrade(t *testing.T, argv []string, run func(*cobra.Command, []string) error) {
	t.Helper()
	origCmd, origRun := upgradeCommand, runUpgrade
	t.Cleanup(func() { upgradeCommand, runUpgrade = origCmd, origRun })
	upgradeCommand = func() []string { return argv }
	runUpgrade = run
}
