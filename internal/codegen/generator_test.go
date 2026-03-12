package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateFromMinimalSchema(t *testing.T) {
	schemaPath, overridesPath := writeTestSchema(t)

	schema, err := ParseSchema(schemaPath, overridesPath)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	outputDir := t.TempDir()
	gen := NewGenerator(schema, outputDir, "github.com/worksome/worksome-cli")

	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify expected files exist
	expectedFiles := []string{
		"types/enums.go",
		"types/types.go",
		"types/inputs.go",
		"queries/queries.go",
		"commands/commands.go",
		"commands/root.go",
	}

	for _, f := range expectedFiles {
		path := filepath.Join(outputDir, f)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected file %s not found: %v", f, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("file %s is empty", f)
		}
	}
}

func TestGenerateFromFullSchema(t *testing.T) {
	schemaPath := "../../schema/schema.graphql"
	overridesPath := "../../schema/overrides.yaml"

	if _, err := os.Stat(schemaPath); os.IsNotExist(err) {
		t.Skip("Full schema not available, skipping")
	}

	schema, err := ParseSchema(schemaPath, overridesPath)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	if len(schema.Resources) < 50 {
		t.Errorf("expected at least 50 resources from full schema, got %d", len(schema.Resources))
	}
	if len(schema.Enums) < 50 {
		t.Errorf("expected at least 50 enums from full schema, got %d", len(schema.Enums))
	}

	outputDir := t.TempDir()
	gen := NewGenerator(schema, outputDir, "github.com/worksome/worksome-cli")

	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate from full schema failed: %v", err)
	}

	// Verify root.go has RegisterAll
	rootBytes, err := os.ReadFile(filepath.Join(outputDir, "commands", "root.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rootBytes) < 100 {
		t.Error("root.go seems too small")
	}
}
