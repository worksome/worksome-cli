package commands

// Hand-written. The generator only writes commands.go and root.go, so this file
// survives `make generate`. It covers writePageInfo, which is emitted into
// commands.go from the template in internal/codegen/generator.go.

import (
	"bytes"
	"testing"
)

func TestWritePageInfo(t *testing.T) {
	tests := []struct {
		name      string
		paginator map[string]any
		want      string
	}{
		{
			"full paginator info",
			map[string]any{"paginatorInfo": map[string]any{"currentPage": 1.0, "lastPage": 72.0, "total": 715.0, "perPage": 10.0}},
			"page 1 of 72 (715 total)\n",
		},
		{
			"single page",
			map[string]any{"paginatorInfo": map[string]any{"currentPage": 1.0, "lastPage": 1.0, "total": 4.0}},
			"page 1 of 1 (4 total)\n",
		},
		{"no paginatorInfo", map[string]any{"data": []any{}}, ""},
		{"paginatorInfo missing a key", map[string]any{"paginatorInfo": map[string]any{"currentPage": 1.0}}, ""},
		{"paginatorInfo of the wrong shape", map[string]any{"paginatorInfo": "nope"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			writePageInfo(&buf, tt.paginator)
			if got := buf.String(); got != tt.want {
				t.Errorf("writePageInfo() wrote %q, want %q", got, tt.want)
			}
		})
	}
}
