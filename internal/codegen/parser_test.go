package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

const minimalSchema = `
type Query {
	"Get a specific hire."
	hire(id: ID!): Hire
	"Get all hires."
	hires(first: Int! = 10, page: Int, status: HireStatus): HirePaginator!
	"Get the viewer."
	viewer: User!
}

type Mutation {
	"Create a draft hire."
	createDraftHire(input: HireInput!): Hire!
	"Terminate a hire."
	terminateHire(input: TerminateHireInput!): Hire!
	"Create a job."
	createJob(input: CreateJobInput!): Job!
}

type Hire {
	id: ID!
	status: HireStatus!
	company: Company!
}

type Job {
	id: ID!
	title: String!
}

type Company {
	id: ID!
	name: String!
}

type User {
	id: ID!
	name: String!
	email: String
}

type HirePaginator {
	paginatorInfo: PaginatorInfo!
	data: [Hire!]!
}

type PaginatorInfo {
	count: Int!
	currentPage: Int!
	hasMorePages: Boolean!
	lastPage: Int!
	perPage: Int!
	total: Int!
}

enum HireStatus {
	DRAFT
	ACTIVE
	ENDED
	CANCELLED
}

input HireInput {
	jobId: ID!
	workerId: ID!
}

input TerminateHireInput {
	id: ID!
	reason: String
}

input CreateJobInput {
	title: String!
	description: String
}
`

func writeTestSchema(t *testing.T) (schemaPath, overridesPath string) {
	t.Helper()
	dir := t.TempDir()

	schemaPath = filepath.Join(dir, "schema.graphql")
	if err := os.WriteFile(schemaPath, []byte(minimalSchema), 0o644); err != nil {
		t.Fatal(err)
	}

	overridesPath = filepath.Join(dir, "overrides.yaml")
	overridesContent := `
resources:
  viewer:
    queries: ["viewer"]
    mutations: []
ignore: []
`
	if err := os.WriteFile(overridesPath, []byte(overridesContent), 0o644); err != nil {
		t.Fatal(err)
	}

	return schemaPath, overridesPath
}

func TestParseSchema(t *testing.T) {
	schemaPath, overridesPath := writeTestSchema(t)

	schema, err := ParseSchema(schemaPath, overridesPath)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	// Check enums
	if len(schema.Enums) != 1 {
		t.Errorf("expected 1 enum, got %d", len(schema.Enums))
	}
	if schema.Enums[0].Name != "HireStatus" {
		t.Errorf("expected enum HireStatus, got %s", schema.Enums[0].Name)
	}
	if len(schema.Enums[0].Values) != 4 {
		t.Errorf("expected 4 enum values, got %d", len(schema.Enums[0].Values))
	}

	// Check inputs
	if len(schema.Inputs) != 3 {
		t.Errorf("expected 3 inputs, got %d", len(schema.Inputs))
	}

	// Check objects
	objectNames := make(map[string]bool)
	for _, obj := range schema.Objects {
		objectNames[obj.Name] = true
	}
	for _, expected := range []string{"Hire", "Job", "Company", "User"} {
		if !objectNames[expected] {
			t.Errorf("expected object %s not found", expected)
		}
	}

	// Check resources
	if len(schema.Resources) == 0 {
		t.Fatal("expected at least 1 resource, got 0")
	}

	resourceMap := make(map[string]Resource)
	for _, r := range schema.Resources {
		resourceMap[r.Name] = r
	}

	// Check viewer resource (from overrides)
	viewer, ok := resourceMap["viewer"]
	if !ok {
		t.Fatal("expected viewer resource")
	}
	if viewer.GetQuery == nil {
		t.Error("viewer should have a get query")
	}

	// Check hires resource
	hires, ok := resourceMap["hires"]
	if !ok {
		t.Fatal("expected hires resource")
	}
	if hires.ListQuery == nil {
		t.Error("hires should have a list query")
	}
	if hires.ListQuery != nil && hires.ListQuery.CLIName != "list" {
		t.Errorf("hires list query CLI name should be 'list', got %q", hires.ListQuery.CLIName)
	}

	// Check hire mutations were grouped into hires
	mutationNames := make(map[string]bool)
	for _, m := range hires.Mutations {
		mutationNames[m.Name] = true
	}
	if !mutationNames["terminateHire"] {
		t.Error("expected terminateHire mutation in hires resource")
	}
}

func TestParseSchemaMissingFile(t *testing.T) {
	_, err := ParseSchema("/nonexistent/schema.graphql", "")
	if err == nil {
		t.Error("expected error for missing schema file")
	}
}

func TestToPascalCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"helloWorld", "HelloWorld"},
		{"hello-world", "HelloWorld"},
		{"hello_world", "HelloWorld"},
		{"id", "Id"},
		{"", ""},
		{"HTML", "HTML"},
	}
	for _, tt := range tests {
		got := toPascalCase(tt.input)
		if got != tt.expected {
			t.Errorf("toPascalCase(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestToKebabCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"helloWorld", "hello-world"},
		{"HTMLParser", "htmlparser"},
		{"createJob", "create-job"},
		{"ID", "id"},
	}
	for _, tt := range tests {
		got := toKebabCase(tt.input)
		if got != tt.expected {
			t.Errorf("toKebabCase(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestToSingular(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hires", "hire"},
		{"jobs", "job"},
		{"companies", "company"},
		{"addresses", "address"},
		{"status", "status"},
	}
	for _, tt := range tests {
		got := toSingular(tt.input)
		if got != tt.expected {
			t.Errorf("toSingular(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
