package output

import (
	"testing"

	"github.com/spf13/cobra"
)

// newTestCommand builds a root command with a --filter persistent flag and a
// child command with the given local flags, ready for ApplyFilterFlag tests.
func newTestCommand(flags map[string]string) (*cobra.Command, *cobra.Command) {
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("filter", "", "filter flag")

	child := &cobra.Command{
		Use:  "list",
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	for name, def := range flags {
		child.Flags().String(name, def, "test flag "+name)
	}

	root.AddCommand(child)
	return root, child
}

func TestApplyFilterFlag_empty(t *testing.T) {
	_, child := newTestCommand(map[string]string{"status": ""})

	// No --filter set, should be a no-op.
	if err := ApplyFilterFlag(child); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, _ := child.Flags().GetString("status")
	if v != "" {
		t.Errorf("expected empty status, got %q", v)
	}
}

func TestApplyFilterFlag_singlePair(t *testing.T) {
	root, child := newTestCommand(map[string]string{"status": ""})
	root.PersistentFlags().Set("filter", "status=ACTIVE")

	if err := ApplyFilterFlag(child); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, _ := child.Flags().GetString("status")
	if v != "ACTIVE" {
		t.Errorf("expected status=ACTIVE, got %q", v)
	}
}

func TestApplyFilterFlag_multiplePairs(t *testing.T) {
	root, child := newTestCommand(map[string]string{
		"status":   "",
		"currency": "",
	})
	root.PersistentFlags().Set("filter", "status=ACTIVE,currency=DKK")

	if err := ApplyFilterFlag(child); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status, _ := child.Flags().GetString("status")
	currency, _ := child.Flags().GetString("currency")

	if status != "ACTIVE" {
		t.Errorf("expected status=ACTIVE, got %q", status)
	}
	if currency != "DKK" {
		t.Errorf("expected currency=DKK, got %q", currency)
	}
}

func TestApplyFilterFlag_pluralFallback(t *testing.T) {
	root, child := newTestCommand(map[string]string{"statuses": ""})
	root.PersistentFlags().Set("filter", "status=ACTIVE")

	if err := ApplyFilterFlag(child); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, _ := child.Flags().GetString("statuses")
	if v != "ACTIVE" {
		t.Errorf("expected statuses=ACTIVE, got %q", v)
	}
}

func TestApplyFilterFlag_camelCaseKey(t *testing.T) {
	root, child := newTestCommand(map[string]string{"payment-status": ""})
	root.PersistentFlags().Set("filter", "paymentStatus=PAID")

	if err := ApplyFilterFlag(child); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, _ := child.Flags().GetString("payment-status")
	if v != "PAID" {
		t.Errorf("expected payment-status=PAID, got %q", v)
	}
}

func TestApplyFilterFlag_trimSpaces(t *testing.T) {
	root, child := newTestCommand(map[string]string{"status": ""})
	root.PersistentFlags().Set("filter", " status = ACTIVE ")

	if err := ApplyFilterFlag(child); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, _ := child.Flags().GetString("status")
	if v != "ACTIVE" {
		t.Errorf("expected status=ACTIVE, got %q", v)
	}
}

func TestApplyFilterFlag_unknownKey(t *testing.T) {
	root, child := newTestCommand(map[string]string{"status": ""})
	root.PersistentFlags().Set("filter", "nonexistent=VALUE")

	err := ApplyFilterFlag(child)
	if err == nil {
		t.Fatal("expected error for unknown filter key")
	}

	expected := `unknown filter key "nonexistent" (no --nonexistent flag)`
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestApplyFilterFlag_invalidFormat_noEquals(t *testing.T) {
	root, child := newTestCommand(map[string]string{"status": ""})
	root.PersistentFlags().Set("filter", "statusACTIVE")

	err := ApplyFilterFlag(child)
	if err == nil {
		t.Fatal("expected error for invalid format")
	}

	expected := `invalid filter format "statusACTIVE": use key=value`
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestApplyFilterFlag_emptyKey(t *testing.T) {
	root, child := newTestCommand(map[string]string{"status": ""})
	root.PersistentFlags().Set("filter", "=VALUE")

	err := ApplyFilterFlag(child)
	if err == nil {
		t.Fatal("expected error for empty key")
	}

	expected := `invalid filter format "=VALUE": key cannot be empty`
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestApplyFilterFlag_valueWithEquals(t *testing.T) {
	// Values can contain "=" characters (e.g., base64-encoded data).
	root, child := newTestCommand(map[string]string{"token": ""})
	root.PersistentFlags().Set("filter", "token=abc=def")

	if err := ApplyFilterFlag(child); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, _ := child.Flags().GetString("token")
	if v != "abc=def" {
		t.Errorf("expected token=abc=def, got %q", v)
	}
}

func TestToKebabCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"status", "status"},
		{"paymentStatus", "payment-status"},
		{"HTMLParser", "htmlparser"},
		{"myURL", "my-url"},
		{"snake_case", "snake-case"},
		{"alreadyKebab", "already-kebab"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := toKebabCase(tc.input)
			if got != tc.expected {
				t.Errorf("toKebabCase(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}
