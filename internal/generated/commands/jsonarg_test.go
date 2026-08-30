package commands

// Hand-written. The generator only writes commands.go and root.go, so this file
// survives `make generate`. It covers jsonArg, which is emitted into commands.go
// from the template in internal/codegen/generator.go.

import (
	"strings"
	"testing"
)

func TestJSONArg(t *testing.T) {
	tests := []struct {
		name    string
		gqlType string
		raw     string
		wantErr bool
	}{
		{"object for input", "DateRangeInput", `{"start":"2026-07-01"}`, false},
		{"list for list of inputs", "[OrderByClauseInput!]", `[{"column":"ID"}]`, false},
		{"object for list of inputs", "[OrderByClauseInput!]", `{"column":"ID"}`, false},
		{"explicit null", "DateRangeInput", `null`, false},

		{"malformed JSON", "DateRangeInput", `2026-07-01..2026-07-31`, true},
		// Well-formed JSON of the wrong shape used to reach the server and fail there.
		{"bare string", "DateRangeInput", `"2026-07-01"`, true},
		{"number", "DateRangeInput", `42`, true},
		{"bool", "DateRangeInput", `true`, true},
		{"string for list of inputs", "[OrderByClauseInput!]", `"nonsense"`, true},
		{"list for a non-list input", "DateRangeInput", `[{"start":"a"}]`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := jsonArg("some-flag", tt.gqlType, tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("jsonArg(%q) error = %v, wantErr %v", tt.raw, err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), tt.gqlType) {
				t.Errorf("error should name the expected type %q, got: %v", tt.gqlType, err)
			}
		})
	}
}
