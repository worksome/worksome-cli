package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateReadmeCounts(t *testing.T) {
	schema := &Schema{Resources: []Resource{
		{Name: "hires", GetQuery: &Operation{Name: "hire"}, ListQuery: &Operation{Name: "hires"}, Mutations: []Operation{{Name: "terminateHire"}, {Name: "cancelHire"}}},
		{Name: "viewer", GetQuery: &Operation{Name: "viewer"}},
	}}
	if got := schema.OperationCount(); got != 5 {
		t.Fatalf("OperationCount() = %d, want 5", got)
	}

	path := filepath.Join(t.TempDir(), "README.md")
	readme := "Full API coverage — <!-- resources -->82<!-- /resources --> resource groups, <!-- operations -->199<!-- /operations --> operations.\n" +
		"`worksome --help` lists all <!-- resources -->82<!-- /resources --> resource groups.\n"
	if err := os.WriteFile(path, []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := UpdateReadmeCounts(path, schema)
	if err != nil {
		t.Fatalf("UpdateReadmeCounts() error = %v", err)
	}
	if !changed {
		t.Error("expected the README to change")
	}
	got, _ := os.ReadFile(path)
	want := "<!-- resources -->2<!-- /resources --> resource groups, <!-- operations -->5<!-- /operations --> operations."
	if !strings.Contains(string(got), want) {
		t.Errorf("README = %s\nwant it to contain %s", got, want)
	}
	if strings.Count(string(got), "<!-- resources -->2<!-- /resources -->") != 2 {
		t.Errorf("every resources marker should be rewritten:\n%s", got)
	}

	// Second run is a no-op.
	changed, err = UpdateReadmeCounts(path, schema)
	if err != nil || changed {
		t.Errorf("second run: changed=%v err=%v, want false and nil", changed, err)
	}
}

func TestUpdateReadmeCounts_MissingMarkerIsAnError(t *testing.T) {
	schema := &Schema{Resources: []Resource{{Name: "hires", ListQuery: &Operation{Name: "hires"}}}}
	path := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(path, []byte("Only <!-- resources -->1<!-- /resources --> here.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateReadmeCounts(path, schema); err == nil || !strings.Contains(err.Error(), "operations") {
		t.Errorf("expected an error naming the missing operations marker, got %v", err)
	}
}
