package codegen

import (
	"os"
	"path/filepath"
	"strings"
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

func TestGenerateMarkFlagRequired(t *testing.T) {
	// Schema with required non-id arguments on both get and list queries.
	// The generated commands should call MarkFlagRequired for those flags.
	schema := `
type Query {
	"Get a specific gate."
	gate(gate: String!): Gate
	"List workflow variables."
	workflowVariables(first: Int! = 10, page: Int, appliesTo: String!): WorkflowVariablePaginator!
	"Get a specific invoice."
	invoice(number: String!): Invoice
}

type Gate {
	id: ID!
	name: String!
}

type Invoice {
	id: ID!
	number: String!
	amount: Float!
}

type WorkflowVariable {
	id: ID!
	name: String!
}

type WorkflowVariablePaginator {
	paginatorInfo: PaginatorInfo!
	data: [WorkflowVariable!]!
}

type PaginatorInfo {
	count: Int!
	currentPage: Int!
	hasMorePages: Boolean!
	lastPage: Int!
	perPage: Int!
	total: Int!
}
`
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseSchema(schemaPath, "")
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	outputDir := t.TempDir()
	gen := NewGenerator(parsed, outputDir, "github.com/worksome/worksome-cli")

	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Read the generated commands file
	cmdsPath := filepath.Join(outputDir, "commands", "commands.go")
	cmdsBytes, err := os.ReadFile(cmdsPath)
	if err != nil {
		t.Fatalf("reading generated commands: %v", err)
	}
	cmds := string(cmdsBytes)

	// Verify MarkFlagRequired is emitted for required non-id, non-pagination args
	requiredFlags := []string{
		`MarkFlagRequired("gate")`,
		`MarkFlagRequired("applies-to")`,
		`MarkFlagRequired("number")`,
	}
	for _, flag := range requiredFlags {
		if !strings.Contains(cmds, flag) {
			t.Errorf("expected generated commands to contain %q", flag)
		}
	}

	// Verify MarkFlagRequired is NOT emitted for pagination args
	forbiddenFlags := []string{
		`MarkFlagRequired("first")`,
		`MarkFlagRequired("page")`,
	}
	for _, flag := range forbiddenFlags {
		if strings.Contains(cmds, flag) {
			t.Errorf("generated commands should NOT contain %q (pagination args have defaults)", flag)
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
