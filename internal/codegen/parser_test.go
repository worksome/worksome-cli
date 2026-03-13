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

	// Check that list query has paginator return type
	if hires.ListQuery != nil && !hires.ListQuery.ReturnType.IsPaginator {
		t.Error("hires list query should have a paginator return type")
	}

	// Check input fields are resolved for mutations
	for _, m := range hires.Mutations {
		if m.Name == "terminateHire" {
			if m.InputTypeName != "TerminateHireInput" {
				t.Errorf("terminateHire InputTypeName = %q, want TerminateHireInput", m.InputTypeName)
			}
			if len(m.InputFields) != 2 {
				t.Errorf("terminateHire should have 2 input fields (id, reason), got %d", len(m.InputFields))
			}
			fieldNames := make(map[string]bool)
			for _, f := range m.InputFields {
				fieldNames[f.Name] = true
			}
			if !fieldNames["id"] || !fieldNames["reason"] {
				t.Errorf("terminateHire input fields should include 'id' and 'reason', got %v", fieldNames)
			}
		}
		if m.Name == "createDraftHire" {
			if m.InputTypeName != "HireInput" {
				t.Errorf("createDraftHire InputTypeName = %q, want HireInput", m.InputTypeName)
			}
			if len(m.InputFields) != 2 {
				t.Errorf("createDraftHire should have 2 input fields (jobId, workerId), got %d", len(m.InputFields))
			}
		}
	}

	// Check job resource has createJob mutation with input fields
	// (mutation "createJob" strips "create" → "job" kebab-case resource)
	job, ok := resourceMap["job"]
	if !ok {
		t.Fatal("expected job resource")
	}
	for _, m := range job.Mutations {
		if m.Name == "createJob" {
			if len(m.InputFields) != 2 {
				t.Errorf("createJob should have 2 input fields (title, description), got %d", len(m.InputFields))
			}
		}
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

func TestSplitPascalWords(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"RecruiterToHire", []string{"Recruiter", "To", "Hire"}},
		{"Job", []string{"Job"}},
		{"DraftHire", []string{"Draft", "Hire"}},
		{"", nil},
		{"Hire", []string{"Hire"}},
		{"ABC", []string{"A", "B", "C"}},
	}
	for _, tt := range tests {
		got := splitPascalWords(tt.input)
		if len(got) != len(tt.expected) {
			t.Errorf("splitPascalWords(%q) = %v, want %v", tt.input, got, tt.expected)
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("splitPascalWords(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.expected[i])
			}
		}
	}
}

func TestMatchSuffix(t *testing.T) {
	resources := map[string]*Resource{
		"hires": {Name: "hires"},
		"jobs":  {Name: "jobs"},
		"job":   {Name: "job"},
	}

	tests := []struct {
		rest     string
		expected string
	}{
		// "RecruiterToHire" -> suffix "Hire" -> plural "hires" -> found
		{"RecruiterToHire", "hires"},
		// "RecruiterFromHire" -> suffix "Hire" -> plural "hires" -> found
		{"RecruiterFromHire", "hires"},
		// "DraftJob" -> suffix "Job" -> exact match "job" -> found
		{"DraftJob", "job"},
		// Single word: nothing to suffix-match
		{"Hire", ""},
		// No matching resource
		{"RecruiterToProject", ""},
	}
	for _, tt := range tests {
		got := matchSuffix(tt.rest, resources)
		if got != tt.expected {
			t.Errorf("matchSuffix(%q) = %q, want %q", tt.rest, got, tt.expected)
		}
	}
}

func TestMatchMutationToResource_MultiWord(t *testing.T) {
	// Simulate a parser with no doc (not needed for matchMutationToResource)
	p := &parser{}
	resources := map[string]*Resource{
		"hires": {Name: "hires"},
		"jobs":  {Name: "jobs"},
	}

	tests := []struct {
		mutation string
		expected string
	}{
		// Simple cases: exact match after stripping prefix
		{"createJob", "jobs"},
		{"terminateHire", "hires"},
		// Multi-word mutations: should match via suffix
		{"attributeRecruiterToHire", "hires"},
		{"removeRecruiterFromHire", "hires"},
		// Direct match still works
		{"deleteJob", "jobs"},
	}
	for _, tt := range tests {
		got := p.matchMutationToResource(tt.mutation, resources)
		if got != tt.expected {
			t.Errorf("matchMutationToResource(%q) = %q, want %q", tt.mutation, got, tt.expected)
		}
	}
}

func TestIgnoreMutationsNotQueries(t *testing.T) {
	// Schema with both a "hire" query and a "hire" mutation (deprecated).
	// The "hire" mutation should be ignored, but the "hire" query should remain.
	schema := `
type Query {
	"Get a specific hire."
	hire(id: ID!): Hire
	"Get all hires."
	hires(first: Int! = 10, page: Int): HirePaginator!
}

type Mutation {
	"Deprecated: use createDraftHire."
	hire(input: HireInput!): Hire! @deprecated(reason: "Use createDraftHire")
	"Create a draft hire."
	createDraftHire(input: HireInput!): Hire!
}

type Hire {
	id: ID!
	status: String!
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

input HireInput {
	jobId: ID!
	workerId: ID!
}
`
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}

	overridesPath := filepath.Join(dir, "overrides.yaml")
	overridesContent := `
ignore: []
ignore_mutations:
  - "hire"
`
	if err := os.WriteFile(overridesPath, []byte(overridesContent), 0o644); err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseSchema(schemaPath, overridesPath)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	// Find the hires resource
	var hiresResource *Resource
	for i := range parsed.Resources {
		if parsed.Resources[i].Name == "hires" {
			hiresResource = &parsed.Resources[i]
			break
		}
	}
	if hiresResource == nil {
		var names []string
		for _, r := range parsed.Resources {
			names = append(names, r.Name)
		}
		t.Fatalf("expected 'hires' resource, got resources: %v", names)
	}

	// The hire query should be present as the get query
	if hiresResource.GetQuery == nil {
		t.Error("hires should have a get query (the hire(id: ID!) query should NOT be ignored)")
	}

	// The deprecated "hire" mutation should be excluded
	for _, m := range hiresResource.Mutations {
		if m.Name == "hire" {
			t.Error("deprecated 'hire' mutation should be ignored via ignore_mutations")
		}
	}

	// The createDraftHire mutation should still be present
	mutationNames := make(map[string]bool)
	for _, m := range hiresResource.Mutations {
		mutationNames[m.Name] = true
	}
	if !mutationNames["createDraftHire"] {
		t.Error("expected createDraftHire mutation in hires resource")
	}
}

func TestIgnoreIntrospectionQueries(t *testing.T) {
	// Schema with __schema and __type queries — they should be filtered out.
	schema := `
type Query {
	"Get a specific hire."
	hire(id: ID!): Hire
	"Get all hires."
	hires(first: Int! = 10, page: Int): HirePaginator!
}

type Hire {
	id: ID!
	status: String!
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
`
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}

	overridesPath := filepath.Join(dir, "overrides.yaml")
	overridesContent := `
ignore:
  - "__schema"
  - "__type"
`
	if err := os.WriteFile(overridesPath, []byte(overridesContent), 0o644); err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseSchema(schemaPath, overridesPath)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	// Verify no resource is created for __schema or __type
	for _, r := range parsed.Resources {
		if r.Name == "--schema" || r.Name == "--type" || r.Name == "__schema" || r.Name == "__type" {
			t.Errorf("unexpected resource %q — introspection queries should be ignored", r.Name)
		}
	}
}

func TestDeriveMutationCLIName_SingularStrip(t *testing.T) {
	p := &parser{}

	tests := []struct {
		mutation     string
		resource     string
		expectedCLI  string
	}{
		// Singular resource name in mutation should be stripped
		{"terminateHire", "hires", "terminate"},
		{"cancelHire", "hires", "cancel"},
		{"rejectHire", "hires", "reject"},
		{"shareHire", "hires", "share"},
		{"endJob", "jobs", "end"},
		{"duplicateJob", "jobs", "duplicate"},
		// Multi-word mutations — singular suffix still stripped
		{"attributeRecruiterToHire", "hires", "attribute-recruiter-to"},
		// CRUD prefix still works as before
		{"createJob", "jobs", "create"},
		{"deleteJob", "jobs", "delete"},
		{"updateJob", "jobs", "update"},
	}
	for _, tt := range tests {
		got := p.deriveMutationCLIName(tt.mutation, tt.resource)
		if got != tt.expectedCLI {
			t.Errorf("deriveMutationCLIName(%q, %q) = %q, want %q", tt.mutation, tt.resource, got, tt.expectedCLI)
		}
	}
}

func TestParseSchemaMultiWordMutations(t *testing.T) {
	// Schema with multi-word mutations that should be grouped under "hires"
	schema := `
type Query {
	hire(id: ID!): Hire
	hires(first: Int! = 10, page: Int): HirePaginator!
}

type Mutation {
	attributeRecruiterToHire(input: AttributeRecruiterToHireInput!): Hire!
	removeRecruiterFromHire(input: RemoveRecruiterFromHireInput!): Hire!
	terminateHire(input: TerminateHireInput!): Hire!
}

type Hire {
	id: ID!
	status: String!
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

input AttributeRecruiterToHireInput {
	hireId: ID!
	recruiterId: ID!
}

input RemoveRecruiterFromHireInput {
	hireId: ID!
	recruiterId: ID!
}

input TerminateHireInput {
	id: ID!
	reason: String
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

	// Find the hires resource
	var hiresResource *Resource
	for i := range parsed.Resources {
		if parsed.Resources[i].Name == "hires" {
			hiresResource = &parsed.Resources[i]
			break
		}
	}
	if hiresResource == nil {
		var names []string
		for _, r := range parsed.Resources {
			names = append(names, r.Name)
		}
		t.Fatalf("expected 'hires' resource, got resources: %v", names)
	}

	// All three mutations should be grouped under hires
	mutationNames := make(map[string]bool)
	for _, m := range hiresResource.Mutations {
		mutationNames[m.Name] = true
	}

	for _, expected := range []string{"attributeRecruiterToHire", "removeRecruiterFromHire", "terminateHire"} {
		if !mutationNames[expected] {
			t.Errorf("expected mutation %q in hires resource, got mutations: %v", expected, mutationNames)
		}
	}

	// Verify there are no spurious resources like "recruiter-to-hire"
	for _, r := range parsed.Resources {
		if r.Name == "recruiter-to-hire" || r.Name == "recruiter-from-hire" {
			t.Errorf("unexpected resource %q — multi-word mutation should be grouped under 'hires'", r.Name)
		}
	}
}
