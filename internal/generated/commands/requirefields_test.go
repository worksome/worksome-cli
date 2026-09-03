package commands

// Hand-written. The generator only writes commands.go and root.go, so this file
// survives `make generate`. It covers requireFields, which is emitted into
// commands.go from the template in internal/codegen/generator.go.

import (
	"strings"
	"testing"
)

func TestRequireFields(t *testing.T) {
	required := []requiredField{{"hire", "hire"}, {"reason", "reason"}, {"date", "date"}}

	tests := []struct {
		name    string
		input   map[string]any
		wantErr string // substring; "" means no error
	}{
		{"all present", map[string]any{"hire": "x", "reason": "OTHER", "date": "2026-08-31"}, ""},
		{"extra keys are fine", map[string]any{"hire": "x", "reason": "OTHER", "date": "d", "comments": "c"}, ""},
		{"one missing", map[string]any{"hire": "x", "reason": "OTHER"}, "missing required input field: date (set --date, or include it in --input)"},
		{"two missing", map[string]any{"hire": "x"}, "missing required input fields: reason, date (set --reason, --date, or include them in --input)"},
		{"explicit null counts as provided", map[string]any{"hire": "x", "reason": "OTHER", "date": nil}, ""},
		{"empty input", map[string]any{}, "hire, reason, date"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireFields(tt.input, required)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}

	if err := requireFields(map[string]any{}, nil); err != nil {
		t.Errorf("no required fields must never error, got %v", err)
	}
}
