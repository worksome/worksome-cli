package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// binaryPath is set once during TestMain after building the CLI.
var binaryPath string

func TestMain(m *testing.M) {
	if os.Getenv("WORKSOME_INTEGRATION_TEST") != "1" {
		// Not running in integration mode; skip entirely.
		os.Exit(0)
	}

	if os.Getenv("WORKSOME_API_TOKEN") == "" {
		// Can't run integration tests without a token.
		os.Exit(0)
	}

	// Build the CLI binary into a temp directory.
	tmpDir, err := os.MkdirTemp("", "worksome-integration-*")
	if err != nil {
		panic("creating temp dir: " + err.Error())
	}
	defer os.RemoveAll(tmpDir)

	binaryPath = filepath.Join(tmpDir, "worksome")
	build := exec.Command("go", "build", "-o", binaryPath, "./cmd/worksome/")
	build.Dir = filepath.Join("..", "..")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("building CLI binary: " + err.Error())
	}

	os.Exit(m.Run())
}

func skipUnlessIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("WORKSOME_API_TOKEN") == "" {
		t.Skip("WORKSOME_API_TOKEN not set; skipping integration test")
	}
}

func token() string {
	return os.Getenv("WORKSOME_API_TOKEN")
}

// runCLI executes the worksome binary with the given arguments and returns
// combined stdout output. It fails the test on non-zero exit.
func runCLI(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(binaryPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %v failed: %v\noutput: %s", args, err, string(out))
	}
	return string(out)
}

func TestVersion(t *testing.T) {
	skipUnlessIntegration(t)

	out := runCLI(t, "version")
	if out == "" {
		t.Fatal("expected version output, got empty string")
	}
	if len(out) < 10 {
		t.Errorf("version output suspiciously short: %q", out)
	}
	// The version command prints "worksome-cli <version> (commit: <hash>)"
	if !strings.Contains(out, "worksome-cli") {
		t.Errorf("version output missing 'worksome-cli': %q", out)
	}
}

func TestAuthStatus(t *testing.T) {
	skipUnlessIntegration(t)

	// auth status reads from config, not the --token flag. Pass --token anyway
	// so the command shape matches what was requested. The test verifies the
	// command exits cleanly and produces meaningful output.
	out := runCLI(t, "auth", "status", "--token", token())
	if out == "" {
		t.Fatal("expected auth status output, got empty string")
	}
	// Valid outputs contain either profile info or the not-authenticated message.
	if !strings.Contains(out, "Profile:") && !strings.Contains(out, "Not authenticated") {
		t.Errorf("unexpected auth status output: %q", out)
	}
}

func TestHiresList(t *testing.T) {
	skipUnlessIntegration(t)

	out := runCLI(t, "hires", "list", "--first", "1", "--output", "json", "--token", token())
	if out == "" {
		t.Fatal("expected hires list output, got empty string")
	}
	if !json.Valid([]byte(out)) {
		t.Errorf("hires list output is not valid JSON:\n%s", truncate(out, 500))
	}
}

func TestHiresListAll(t *testing.T) {
	skipUnlessIntegration(t)

	out := runCLI(t, "hires", "list", "--first", "1", "--all", "--output", "json", "--token", token())
	if out == "" {
		t.Fatal("expected hires list --all output, got empty string")
	}
	if !json.Valid([]byte(out)) {
		t.Errorf("hires list --all output is not valid JSON:\n%s", truncate(out, 500))
	}
}

// truncate returns the first n bytes of s, appending "..." if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
