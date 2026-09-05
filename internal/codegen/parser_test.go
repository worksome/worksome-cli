package codegen

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
		mutation    string
		resource    string
		expectedCLI string
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
		// Stripping the whole resource name leaves the leading verb
		{"storeBankDetails", "bank-details", "store"},
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

func TestOverrideMutationCLIName(t *testing.T) {
	// Mutations claimed by an overrides resource must still get a CLI verb.
	// Stripping the resource name from "storeBankDetails" under "bank-details"
	// leaves the leading verb "store" — never an empty command name.
	schema := `
type Query {
	viewer: User!
}

type Mutation {
	"Update the bank account details."
	storeBankDetails(input: StoreBankDetailsInput!): BankDetails!
}

type User {
	id: ID!
	name: String!
}

type BankDetails {
	id: ID!
	name: String!
}

input StoreBankDetailsInput {
	accountId: ID!
	name: String
}
`
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}

	overridesPath := filepath.Join(dir, "overrides.yaml")
	overridesContent := `
resources:
  bank-details:
    queries: []
    mutations: ["storeBankDetails"]
`
	if err := os.WriteFile(overridesPath, []byte(overridesContent), 0o644); err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseSchema(schemaPath, overridesPath)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	var found *Resource
	for i := range parsed.Resources {
		if parsed.Resources[i].Name == "bank-details" {
			found = &parsed.Resources[i]
			break
		}
	}
	if found == nil {
		var names []string
		for _, r := range parsed.Resources {
			names = append(names, r.Name)
		}
		t.Fatalf("expected 'bank-details' resource, got resources: %v", names)
	}

	if len(found.Mutations) != 1 {
		t.Fatalf("expected 1 mutation, got %d", len(found.Mutations))
	}
	if got := found.Mutations[0].CLIName; got != "store" {
		t.Errorf("storeBankDetails CLI name = %q, want %q", got, "store")
	}
	if found.Hoisted {
		t.Error("bank-details should not be hoisted (verb 'store' differs from resource name)")
	}
}

func TestDescriptionFallback(t *testing.T) {
	// Schema with a mutation-only resource — verify it gets the mutation's description.
	schema := `
type Query {
	viewer: User!
}

type Mutation {
	"Accept a bid from a freelancer."
	acceptBid(input: AcceptBidInput!): Bid!
}

type User {
	id: ID!
	name: String!
}

type Bid {
	id: ID!
	status: String!
}

input AcceptBidInput {
	id: ID!
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

	var found *Resource
	for i := range parsed.Resources {
		if parsed.Resources[i].Name == "accept-bid" {
			found = &parsed.Resources[i]
			break
		}
	}
	if found == nil {
		var names []string
		for _, r := range parsed.Resources {
			names = append(names, r.Name)
		}
		t.Fatalf("expected 'accept-bid' resource, got resources: %v", names)
	}

	if found.Description == "" {
		t.Error("expected non-empty description from mutation fallback")
	}
	if found.Description != "Accept a bid from a freelancer." {
		t.Errorf("expected description %q, got %q", "Accept a bid from a freelancer.", found.Description)
	}
}

func TestHoistedResource(t *testing.T) {
	// Schema where a resource has 1 mutation with matching CLI name -> hoisted.
	schema := `
type Query {
	viewer: User!
}

type Mutation {
	"Accept a bid from a freelancer."
	acceptBid(input: AcceptBidInput!): Bid!
}

type User {
	id: ID!
	name: String!
}

type Bid {
	id: ID!
	status: String!
}

input AcceptBidInput {
	id: ID!
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

	var found *Resource
	for i := range parsed.Resources {
		if parsed.Resources[i].Name == "accept-bid" {
			found = &parsed.Resources[i]
			break
		}
	}
	if found == nil {
		var names []string
		for _, r := range parsed.Resources {
			names = append(names, r.Name)
		}
		t.Fatalf("expected 'accept-bid' resource, got resources: %v", names)
	}

	if !found.Hoisted {
		t.Error("expected accept-bid to be hoisted (single mutation with matching CLI name)")
	}
	if found.GetQuery != nil {
		t.Error("hoisted resource should not have a GetQuery")
	}
	if found.ListQuery != nil {
		t.Error("hoisted resource should not have a ListQuery")
	}
	if len(found.Mutations) != 1 {
		t.Errorf("expected 1 mutation, got %d", len(found.Mutations))
	}
}

func TestSingularPluralMerge(t *testing.T) {
	// Schema with both batch(id:ID!): Batch and batches(first:Int): BatchPaginator
	// They should merge into one "batches" resource with both get and list queries.
	schema := `
type Query {
	"Get a specific batch."
	batch(id: ID!): Batch
	"List all batches."
	batches(first: Int! = 10, page: Int): BatchPaginator!
}

type Mutation {
	"Create a batch."
	createBatch(input: CreateBatchInput!): Batch!
}

type Batch {
	id: ID!
	name: String!
	status: String!
}

type BatchPaginator {
	paginatorInfo: PaginatorInfo!
	data: [Batch!]!
}

type PaginatorInfo {
	count: Int!
	currentPage: Int!
	hasMorePages: Boolean!
	lastPage: Int!
	perPage: Int!
	total: Int!
}

input CreateBatchInput {
	name: String!
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

	// Should have "batches" but not "batch" as a separate resource
	var batchesResource *Resource
	for i := range parsed.Resources {
		if parsed.Resources[i].Name == "batch" {
			t.Error("singular 'batch' resource should be merged into 'batches'")
		}
		if parsed.Resources[i].Name == "batches" {
			batchesResource = &parsed.Resources[i]
		}
	}

	if batchesResource == nil {
		var names []string
		for _, r := range parsed.Resources {
			names = append(names, r.Name)
		}
		t.Fatalf("expected 'batches' resource, got resources: %v", names)
	}

	if batchesResource.GetQuery == nil {
		t.Error("batches resource should have a GetQuery (merged from batch)")
	}
	if batchesResource.ListQuery == nil {
		t.Error("batches resource should have a ListQuery")
	}
	if len(batchesResource.Mutations) == 0 {
		t.Error("batches resource should have mutations (createBatch)")
	}
}

func TestToPlural(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"batch", "batches"},
		{"match", "matches"},
		{"dish", "dishes"},
		{"boss", "bosses"},
		{"box", "boxes"},
		{"buzz", "buzzes"},
		{"company", "companies"},
		{"category", "categories"},
		{"job", "jobs"},
		{"hire", "hires"},
		{"day", "days"},
		{"key", "keys"},
	}
	for _, tt := range tests {
		got := toPlural(tt.input)
		if got != tt.expected {
			t.Errorf("toPlural(%q) = %q, want %q", tt.input, got, tt.expected)
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

func TestUnionTypeTableColumns(t *testing.T) {
	// Schema with a multi-member union behind a paginator — table columns should
	// include a "Type" column for __typename and fields from the shared interface.
	schema := `
type Query {
	multiFactor(id: ID!): MultiFactor
	multiFactors(first: Int! = 10, page: Int): MultiFactorPaginator!
}

union MultiFactor = SmsMultiFactor | TotpMultiFactor

interface HasMultiFactorMetadata {
	id: ID!
	name: String!
	status: MultiFactorStatus!
}

type SmsMultiFactor implements HasMultiFactorMetadata {
	id: ID!
	name: String!
	phoneNumber: String!
	status: MultiFactorStatus!
}

type TotpMultiFactor implements HasMultiFactorMetadata {
	id: ID!
	name: String!
	status: MultiFactorStatus!
}

type MultiFactorPaginator {
	paginatorInfo: PaginatorInfo!
	data: [MultiFactor!]!
}

type PaginatorInfo {
	count: Int!
	currentPage: Int!
	hasMorePages: Boolean!
	lastPage: Int!
	perPage: Int!
	total: Int!
}

enum MultiFactorStatus {
	APPROVED
	PENDING
	CANCELLED
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

	// Find the multi-factors resource
	var mfResource *Resource
	for i := range parsed.Resources {
		if parsed.Resources[i].Name == "multi-factors" {
			mfResource = &parsed.Resources[i]
			break
		}
	}
	if mfResource == nil {
		var names []string
		for _, r := range parsed.Resources {
			names = append(names, r.Name)
		}
		t.Fatalf("expected 'multi-factors' resource, got resources: %v", names)
	}

	// Table columns should exist and start with "Type"
	if len(mfResource.TableColumns) == 0 {
		t.Fatal("expected table columns for multi-factors, got none")
	}
	if mfResource.TableColumns[0].Header != "Type" || mfResource.TableColumns[0].Field != "__typename" {
		t.Errorf("expected first column to be Type/__typename, got %q/%q",
			mfResource.TableColumns[0].Header, mfResource.TableColumns[0].Field)
	}

	// Should have ID column from the shared interface
	hasID := false
	for _, col := range mfResource.TableColumns {
		if col.Field == "id" {
			hasID = true
		}
	}
	if !hasID {
		t.Error("expected table columns to include 'id' from shared interface")
	}

	// Check the selection set includes __typename
	if mfResource.ListQuery != nil {
		sel := mfResource.ListQuery.SelectionSet
		if !strings.Contains(sel, "__typename") {
			t.Errorf("list query selection set should include __typename, got: %s", sel)
		}
	}
	if mfResource.GetQuery != nil {
		sel := mfResource.GetQuery.SelectionSet
		if !strings.Contains(sel, "__typename") {
			t.Errorf("get query selection set should include __typename, got: %s", sel)
		}
	}
}

func TestSingleMemberUnionNoTypeColumn(t *testing.T) {
	// A union with only one member should NOT get a "Type" column.
	schema := `
type Query {
	approvables(first: Int! = 10, page: Int): ApprovablePaginator!
}

union Approvable = Hire

type Hire {
	id: ID!
	status: String!
}

type ApprovablePaginator {
	paginatorInfo: PaginatorInfo!
	data: [Approvable!]!
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

	var appResource *Resource
	for i := range parsed.Resources {
		if parsed.Resources[i].Name == "approvables" {
			appResource = &parsed.Resources[i]
			break
		}
	}
	if appResource == nil {
		var names []string
		for _, r := range parsed.Resources {
			names = append(names, r.Name)
		}
		t.Fatalf("expected 'approvables' resource, got resources: %v", names)
	}

	// Single-member union: should have columns but no "Type" column
	for _, col := range appResource.TableColumns {
		if col.Field == "__typename" {
			t.Error("single-member union should NOT have a Type/__typename column")
		}
	}
}

// writeSchemaFile writes an arbitrary schema to a temp file for parsing.
func writeSchemaFile(t *testing.T, schema string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema.graphql")
	if err := os.WriteFile(path, []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// An unmapped scalar used to generate code referencing a Go type that does
// not exist, failing at `go build` with "undefined: DecimalFour" rather than
// at the point of the actual problem.
func TestParseSchemaRejectsUnmappedScalar(t *testing.T) {
	path := writeSchemaFile(t, `
scalar TotallyNewScalar

type Query {
	thing: TotallyNewScalar
}
`)

	_, err := ParseSchema(path, "")
	if err == nil {
		t.Fatal("expected an error for a scalar with no Go mapping")
	}
	if !strings.Contains(err.Error(), "TotallyNewScalar") {
		t.Errorf("error should name the offending scalar, got: %v", err)
	}
	if !strings.Contains(err.Error(), "scalarMap") {
		t.Errorf("error should say where to fix it, got: %v", err)
	}
}

func TestParseSchemaReportsEveryUnmappedScalar(t *testing.T) {
	path := writeSchemaFile(t, `
scalar AlphaScalar
scalar BetaScalar

type Query {
	a: AlphaScalar
	b: BetaScalar
}
`)

	_, err := ParseSchema(path, "")
	if err == nil {
		t.Fatal("expected an error")
	}
	// Listing all of them at once beats fixing one, regenerating, repeating.
	for _, want := range []string{"AlphaScalar", "BetaScalar"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should list %s, got: %v", want, err)
		}
	}
}

// Every scalar the production schema declares must have a Go mapping, or a
// schema sync breaks the build. Guards the whole scalarMap, not just the two
// entries that happened to be missing.
func TestVendoredSchemaScalarsAreAllMapped(t *testing.T) {
	schema, err := ParseSchema("../../schema/schema.graphql", "../../schema/overrides.yaml")
	if err != nil {
		t.Fatalf("parsing the vendored schema: %v", err)
	}
	for _, name := range schema.Scalars {
		if _, ok := scalarMap[name]; !ok {
			t.Errorf("scalar %q declared in the vendored schema has no Go mapping", name)
		}
	}
}

func TestScalarGoTypes(t *testing.T) {
	tests := map[string]string{
		"DecimalFour":     "float64", // added when the API introduced it
		"Email":           "string",  // was silently unmapped before
		"Decimal":         "float64",
		"DecimalTwo":      "float64",
		"E164PhoneNumber": "string",
		"JSON":            "map[string]any",
	}
	for scalar, want := range tests {
		if got := scalarMap[scalar]; got != want {
			t.Errorf("scalarMap[%q] = %q, want %q", scalar, got, want)
		}
	}
}

// writeSchemaAndOverrides writes both files for a parse that needs overrides.
func writeSchemaAndOverrides(t *testing.T, schema, overrides string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	sp := filepath.Join(dir, "schema.graphql")
	op := filepath.Join(dir, "overrides.yaml")
	if err := os.WriteFile(sp, []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(op, []byte(overrides), 0o644); err != nil {
		t.Fatal(err)
	}
	return sp, op
}

func TestResourceAliasesFromOverrides(t *testing.T) {
	sp, op := writeSchemaAndOverrides(t, minimalSchema, `
aliases:
  hires: ["hire-alias", "h"]
`)

	schema, err := ParseSchema(sp, op)
	if err != nil {
		t.Fatalf("ParseSchema: %v", err)
	}

	var found *Resource
	for i := range schema.Resources {
		if schema.Resources[i].Name == "hires" {
			found = &schema.Resources[i]
		}
	}
	if found == nil {
		t.Fatal("hires resource not generated")
	}
	if len(found.Aliases) != 2 || found.Aliases[0] != "hire-alias" || found.Aliases[1] != "h" {
		t.Errorf("Aliases = %v, want [hire-alias h]", found.Aliases)
	}
}

// A stale alias would otherwise generate a command nobody can reach.
func TestAliasTargetMustExist(t *testing.T) {
	sp, op := writeSchemaAndOverrides(t, minimalSchema, `
aliases:
  not-a-resource: ["whatever"]
`)

	_, err := ParseSchema(sp, op)
	if err == nil {
		t.Fatal("expected an error for an alias on a non-existent resource")
	}
	if !strings.Contains(err.Error(), "not-a-resource") {
		t.Errorf("error should name the bad target, got: %v", err)
	}
}

// An alias that shadows a real resource would hide it from the CLI.
func TestAliasMustNotCollideWithRealResource(t *testing.T) {
	sp, op := writeSchemaAndOverrides(t, minimalSchema, `
aliases:
  hires: ["viewer"]
`)

	_, err := ParseSchema(sp, op)
	if err == nil {
		t.Fatal("expected an error for an alias colliding with a real resource")
	}
	if !strings.Contains(err.Error(), "collides") {
		t.Errorf("error should mention the collision, got: %v", err)
	}
}

// The whole point of the alias: `worksome company get <id>` must keep working
// after the API folded the singular company query into the plural group.
func TestVendoredOverridesKeepCompanyAlias(t *testing.T) {
	schema, err := ParseSchema("../../schema/schema.graphql", "../../schema/overrides.yaml")
	if err != nil {
		t.Fatalf("parsing the vendored schema: %v", err)
	}
	for _, r := range schema.Resources {
		if r.Name != "companies" {
			continue
		}
		for _, a := range r.Aliases {
			if a == "company" {
				return
			}
		}
		t.Fatalf(`companies resource is missing the "company" alias; aliases = %v`, r.Aliases)
	}
	t.Fatal("companies resource not found in the vendored schema")
}

func TestInterfaceReturnTypeGetsSelectionSet(t *testing.T) {
	// A query returning an interface type must still get a selection set —
	// the server rejects a bare field with no subfield selection.
	schema := `
type Query {
	accounts: [Account!]!
}

interface Account {
	id: ID!
	name: String!
	avatar: String
}

type Company implements Account {
	id: ID!
	name: String!
	avatar: String
	market: String!
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

	var accounts *Resource
	for i := range parsed.Resources {
		if parsed.Resources[i].Name == "accounts" {
			accounts = &parsed.Resources[i]
			break
		}
	}
	if accounts == nil {
		t.Fatal("expected an 'accounts' resource")
	}

	op := accounts.ListQuery
	if op == nil {
		op = accounts.GetQuery
	}
	if op == nil {
		t.Fatal("expected a query operation for accounts")
	}

	sel := op.SelectionSet
	if strings.TrimSpace(sel) == "" {
		t.Fatal("interface return type produced an empty selection set; the server rejects this")
	}
	// __typename comes along: an interface selection is only the shared fields,
	// so without it a list of accounts gives no way to tell the implementors apart.
	for _, want := range []string{"__typename", "id", "name", "avatar"} {
		if !strings.Contains(sel, want) {
			t.Errorf("selection set should include interface field %q, got: %s", want, sel)
		}
	}
}

// innerSelection matches the innermost `name { a b c }` groups of a selection
// set. Nested object selections are always scalar-only, so the innermost groups
// are exactly the `parent { child ... }` pairs a "parent.child" column needs.
var innerSelection = regexp.MustCompile(`(\w+) \{ ([^{}]*) \}`)

// TestTableColumnsAreSelectedByTheQuery asserts that every generated nested
// table column is actually requested by the query that fills the table.
//
// The column list and the selection set are built by two different functions.
// When they disagree the cell renders blank, which reads as "this record has no
// compliances" rather than "the CLI never asked for that field" — a table that
// is quietly wrong instead of one that fails. That shipped once: `compliances.actor`
// and `approvalStates.state` were columns no query ever selected.
func TestTableColumnsAreSelectedByTheQuery(t *testing.T) {
	parsed, err := ParseSchema("../../schema/schema.graphql", "../../schema/overrides.yaml")
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	check := func(what string, columns []TableColumn, selectionSet string) {
		if selectionSet == "" {
			return
		}
		selected := make(map[string]map[string]bool)
		for _, m := range innerSelection.FindAllStringSubmatch(selectionSet, -1) {
			children := selected[m[1]]
			if children == nil {
				children = make(map[string]bool)
				selected[m[1]] = children
			}
			for _, f := range strings.Fields(m[2]) {
				children[f] = true
			}
		}

		for _, col := range columns {
			parent, child, nested := strings.Cut(col.Field, ".")
			if !nested {
				continue
			}
			if !selected[parent][child] {
				t.Errorf("%s: column %q (%s) is never selected by the query — it renders blank\nselection set: %s",
					what, col.Header, col.Field, selectionSet)
			}
		}
	}

	var checked int
	for _, res := range parsed.Resources {
		if len(res.TableColumns) == 0 {
			continue
		}
		for _, op := range []*Operation{res.GetQuery, res.ListQuery} {
			if op == nil {
				continue
			}
			check(res.Name, res.TableColumns, op.SelectionSet)
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no resources with table columns found — the test is covering nothing")
	}
	t.Logf("checked table columns for %d operations", checked)
}

// Nested objects are filtered through safeNestedFields to avoid requesting
// access-controlled fields. That allowlist was dropping the state fields that
// make a nested row interpretable: a hire's compliances came back as catalogue
// entries with no applicable/completed, reading as outstanding requirements
// when the true answer is zero.
func TestNestedSelectionKeepsStateFields(t *testing.T) {
	schema := `
type Query {
	hire(id: ID!): Hire
	hires(first: Int! = 10, page: Int): HirePaginator!
}

type Hire {
	id: ID!
	compliances: [Compliance!]!
	approvalStates: [ApprovalState!]!
	owner: User!
}

type Compliance {
	actor: ComplianceActorTypes!
	name: String!
	applicable: Boolean!
	completed: Boolean!
	completedAt: DateTime
	title: String!
}

type ApprovalState {
	id: ID!
	state: ApprovalApprovableState!
	createdAt: DateTime
}

type User {
	id: ID!
	name: String!
	canCreatePassword: Boolean!
	missingAuthentication: Boolean!
}

enum ComplianceActorTypes { WORKER CLIENT }
enum ApprovalApprovableState { REQUESTED APPROVED }

scalar DateTime

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

	parsed, err := ParseSchema(schemaPath, "")
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	var hires *Resource
	for i := range parsed.Resources {
		if parsed.Resources[i].Name == "hires" {
			hires = &parsed.Resources[i]
			break
		}
	}
	if hires == nil {
		t.Fatal("expected a 'hires' resource")
	}
	if hires.GetQuery == nil {
		t.Fatal("expected a get query for hires")
	}
	sel := hires.GetQuery.SelectionSet

	// The state fields that make a compliance row readable.
	for _, want := range []string{"applicable", "completed", "completedAt"} {
		if !strings.Contains(sel, want) {
			t.Errorf("selection set should include compliance state field %q, got: %s", want, sel)
		}
	}

	// Enums are state/classification — always worth selecting.
	if !strings.Contains(sel, "state") {
		t.Errorf("selection set should include the ApprovalState enum field 'state', got: %s", sel)
	}
	if !strings.Contains(sel, "actor") {
		t.Errorf("selection set should include the Compliance enum field 'actor', got: %s", sel)
	}

	// The access-controlled User booleans must stay out — that is what the
	// allowlist is for.
	for _, unwanted := range []string{"canCreatePassword", "missingAuthentication"} {
		if strings.Contains(sel, unwanted) {
			t.Errorf("selection set must not request access-controlled field %q, got: %s", unwanted, sel)
		}
	}
}

// pflag treats a back-quoted word in a usage string as the flag's value
// placeholder. Schema descriptions use backticks as code spans, so a flag whose
// description read "the `accounts` arg" rendered as `--viewer-role accounts`
// instead of `--viewer-role string`, with the quotes stripped from the text.
func TestCleanDescriptionNeutralisesBackticks(t *testing.T) {
	got := cleanDescription("Only filters within the scope set by the `accounts` arg.")
	want := "Only filters within the scope set by the 'accounts' arg."
	if got != want {
		t.Errorf("cleanDescription() = %q, want %q", got, want)
	}
	if strings.Contains(got, "`") {
		t.Errorf("cleanDescription() left a backtick in %q", got)
	}
}

// A field with a nullable enum argument and no default is valid to select
// bare, but the resolver has nothing to evaluate: `hire { triggersApproval }`
// came back "Internal server error" on every hires get/list that did not
// narrow --fields. Fields whose optional argument is a list, or has a default,
// are genuinely optional and must stay in the selection.
func TestSelectionSkipsFieldsWhoseOptionalArgumentIsAChoice(t *testing.T) {
	schema := `
type Query {
	"Get a hire."
	hire(id: ID!): Hire
	"List hires."
	hires(first: Int! = 10, page: Int): HirePaginator!
}

enum Trigger { CREATED UPDATED }
input Window { from: String to: String }

type Hire {
	id: ID!
	"Choice with no default: skipped."
	triggersApproval(trigger: Trigger): Boolean
	"Input object with no default: skipped."
	spendIn(window: Window): Float
	"Optional list: kept."
	compliances(names: [Trigger!]): [String!]!
	"Choice with a default: kept."
	stateFor(trigger: Trigger = CREATED): String
	"Optional scalar: kept."
	label(short: Boolean): String
	"No arguments: kept."
	hasPendingApproval: Boolean!
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
	parsed, err := ParseSchema(schemaPath, "")
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	var hires *Resource
	for i := range parsed.Resources {
		if parsed.Resources[i].Name == "hires" {
			hires = &parsed.Resources[i]
		}
	}
	if hires == nil || hires.ListQuery == nil {
		t.Fatalf("expected a hires resource with a list query, got %+v", parsed.Resources)
	}
	sel := hires.ListQuery.SelectionSet

	for _, kept := range []string{"compliances", "stateFor", "label", "hasPendingApproval", "id"} {
		if !strings.Contains(sel, kept) {
			t.Errorf("selection should keep %q: %s", kept, sel)
		}
	}
	for _, skipped := range []string{"triggersApproval", "spendIn"} {
		if strings.Contains(sel, skipped) {
			t.Errorf("selection must not include %q, its argument is a choice the CLI cannot make: %s", skipped, sel)
		}
	}
}

// Members of a nested union may declare the same field with different types
// (Note.notable: Job.status is JobStatus, TrustedContact.status is
// ContactStatus!). GraphQL rejects a document that selects both, so the
// fallback fragments keep only fields whose type agrees across members.
func TestNestedUnionFragmentsAvoidConflictingFields(t *testing.T) {
	schema := `
type Query {
	"List notes."
	notes(first: Int! = 10, page: Int): NotePaginator!
}
type Note { id: ID! body: String! notable: Notable }
union Notable = Job | Contact
enum JobStatus { OPEN CLOSED }
enum ContactStatus { ACTIVE BLOCKED }
type Job { id: ID! name: String! status: JobStatus createdAt: DateTime! }
type Contact { id: ID! email: String status: ContactStatus! createdAt: Date! }
scalar DateTime
scalar Date
type NotePaginator { paginatorInfo: PaginatorInfo! data: [Note!]! }
type PaginatorInfo { count: Int! currentPage: Int! hasMorePages: Boolean! lastPage: Int! perPage: Int! total: Int! }
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
	var sel string
	for _, r := range parsed.Resources {
		if r.Name == "notes" && r.ListQuery != nil {
			sel = r.ListQuery.SelectionSet
		}
	}
	want := "notable { __typename ... on Job { id name } ... on Contact { id email } }"
	if !strings.Contains(sel, want) {
		t.Errorf("selection = %s\nwant it to contain %s", sel, want)
	}
	for _, conflicting := range []string{"status", "createdAt"} {
		if strings.Contains(sel, conflicting) {
			t.Errorf("selection must not include %q, its type differs between members: %s", conflicting, sel)
		}
	}
}

// Paginated relations are offered on request rather than selected by default
// (one server query per row each); union-typed nested fields are selected by
// default, since they are a single object. Neither was reachable before.
func TestPaginatedRelationsAreOptionalAndUnionsSelected(t *testing.T) {
	schema := `
type Query {
	"List user groups."
	userGroups(first: Int! = 10, page: Int): UserGroupPaginator!
	"Get a user group."
	userGroup(id: ID!): UserGroup
	"List approval requests."
	approvalApprovables(first: Int! = 10, page: Int): ApprovalApprovablePaginator!
}

type UserGroup {
	id: ID!
	name: String!
	"Paginated relation with a defaulted first: on request."
	users(first: Int! = 10, page: Int): UserPaginator!
	"Paginated relation with a required argument: never."
	audits(since: String!, first: Int! = 10): UserPaginator!
}

type User { id: ID! name: String! email: String secret: String }

type ApprovalApprovable {
	id: ID!
	"Union: selected by default."
	approvable: Approvable
}

union Approvable = Hire
type Hire { id: ID! number: String! status: String! }

type UserPaginator { paginatorInfo: PaginatorInfo! data: [User!]! }
type UserGroupPaginator { paginatorInfo: PaginatorInfo! data: [UserGroup!]! }
type ApprovalApprovablePaginator { paginatorInfo: PaginatorInfo! data: [ApprovalApprovable!]! }

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
	byName := map[string]*Resource{}
	for i := range parsed.Resources {
		byName[parsed.Resources[i].Name] = &parsed.Resources[i]
	}

	groups := byName["user-groups"]
	if groups == nil || groups.ListQuery == nil || groups.GetQuery == nil {
		t.Fatalf("expected a user-groups resource with list and get, got %v", byName)
	}
	for _, op := range []*Operation{groups.ListQuery, groups.GetQuery} {
		if strings.Contains(op.SelectionSet, "users") {
			t.Errorf("%s: the default selection must not include the paginated relation: %s", op.Name, op.SelectionSet)
		}
		want := "users { paginatorInfo { total } data { id name email } }"
		if got := op.Optional["users"]; got != want {
			t.Errorf("%s: Optional[users] = %q, want %q", op.Name, got, want)
		}
		if _, ok := op.Optional["audits"]; ok {
			t.Errorf("%s: a relation with a required argument cannot be offered", op.Name)
		}
	}

	approvals := byName["approval-approvables"]
	if approvals == nil || approvals.ListQuery == nil {
		t.Fatalf("expected an approval-approvables resource, got %v", byName)
	}
	if want := "approvable { __typename ... on Hire { id number status } }"; !strings.Contains(approvals.ListQuery.SelectionSet, want) {
		t.Errorf("union field should be selected by default; selection = %s", approvals.ListQuery.SelectionSet)
	}
	if approvals.ListQuery.Optional != nil {
		t.Errorf("no paginated relations on ApprovalApprovable, Optional should be nil, got %v", approvals.ListQuery.Optional)
	}
}

// ignoreFieldsSchema exercises both selection depths and both keys of the
// Type.field form: a field ignored on one type, and a same-named field on
// another that must survive.
const ignoreFieldsSchema = `
type Query {
	"Get a specific company."
	company(id: ID!): Company
	"Get all companies."
	companies(first: Int! = 10, page: Int): CompanyPaginator!
}

type Company {
	id: ID!
	name: String!
	usedEngagementTypeSetups: [EngagementTypeSetup!]!
	owner: User
	contact: Contact
}

type User {
	id: ID!
	name: String!
	email: String!
}

type Contact {
	id: ID!
	email: String!
}

enum EngagementTypeSetup {
	W2
	PAYE
}

type CompanyPaginator {
	paginatorInfo: PaginatorInfo!
	data: [Company!]!
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

func parseWithOverrides(t *testing.T, schema, overrides string) (*Schema, error) {
	t.Helper()
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.graphql")
	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}
	overridesPath := filepath.Join(dir, "overrides.yaml")
	if err := os.WriteFile(overridesPath, []byte(overrides), 0o644); err != nil {
		t.Fatal(err)
	}
	return ParseSchema(schemaPath, overridesPath)
}

func TestIgnoreFieldsExcludesGuardedFields(t *testing.T) {
	parsed, err := parseWithOverrides(t, ignoreFieldsSchema, `
ignore_fields:
  - "Company.usedEngagementTypeSetups"
  - "User.email"
`)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	var companies *Resource
	for i := range parsed.Resources {
		if parsed.Resources[i].Name == "companies" {
			companies = &parsed.Resources[i]
			break
		}
	}
	if companies == nil || companies.GetQuery == nil || companies.ListQuery == nil {
		t.Fatal("expected a companies resource with both a get and a list query")
	}

	for _, op := range []*Operation{companies.GetQuery, companies.ListQuery} {
		if strings.Contains(op.SelectionSet, "usedEngagementTypeSetups") {
			t.Errorf("%s still selects an ignored field:\n%s", op.Name, op.SelectionSet)
		}
		if !strings.Contains(op.SelectionSet, "name") {
			t.Errorf("%s dropped more than the ignored field:\n%s", op.Name, op.SelectionSet)
		}
		if strings.Contains(op.SelectionSet, "owner { id name email }") {
			t.Errorf("%s selects the ignored field on a nested object:\n%s", op.Name, op.SelectionSet)
		}
		// Same field name, different type — the exclusion is type-scoped.
		if !strings.Contains(op.SelectionSet, "contact { id email }") {
			t.Errorf("%s dropped Contact.email, which is not ignored:\n%s", op.Name, op.SelectionSet)
		}
	}

	// A column for a field the query never selects renders blank.
	for _, c := range companies.TableColumns {
		if c.Field == "usedEngagementTypeSetups" || c.Field == "owner.email" {
			t.Errorf("table column %q survives for an ignored field", c.Field)
		}
	}
}

func TestIgnoreFieldsRejectsStaleEntry(t *testing.T) {
	for name, entry := range map[string]string{
		"unknown field":  "Company.thisFieldWasRenamed",
		"unknown type":   "ThisTypeIsGone.name",
		"not Type.field": "usedEngagementTypeSetups",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseWithOverrides(t, ignoreFieldsSchema, "ignore_fields:\n  - \""+entry+"\"\n")
			if err == nil {
				t.Fatalf("expected a stale ignore_fields entry %q to fail generation", entry)
			}
			if !strings.Contains(err.Error(), "ignore_fields") {
				t.Errorf("error should name the override that is stale, got: %v", err)
			}
		})
	}
}

func TestIgnoreFieldsRejectsMalformedEntry(t *testing.T) {
	for name, entry := range map[string]string{
		"nested path": "Company.owner.name",
		"empty type":  ".name",
		"empty field": "Company.",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseWithOverrides(t, ignoreFieldsSchema, "ignore_fields:\n  - \""+entry+"\"\n")
			if err == nil {
				t.Fatalf("expected a malformed ignore_fields entry %q to fail generation", entry)
			}
			if !strings.Contains(err.Error(), "not in Type.field form") {
				t.Errorf("a malformed key should be reported as malformed, not as a missing field, got: %v", err)
			}
		})
	}
}

// Ignoring every selectable field is a mistake, but it must not produce a
// document that either re-adds the ignored field or is invalid GraphQL.
func TestIgnoreFieldsLeavingNothingSelectable(t *testing.T) {
	parsed, err := parseWithOverrides(t, ignoreFieldsSchema, `
ignore_fields:
  - "Company.id"
  - "Company.name"
  - "Company.usedEngagementTypeSetups"
  - "Company.owner"
  - "Company.contact"
`)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	var companies *Resource
	for i := range parsed.Resources {
		if parsed.Resources[i].Name == "companies" {
			companies = &parsed.Resources[i]
			break
		}
	}
	if companies == nil || companies.GetQuery == nil || companies.ListQuery == nil {
		t.Fatal("expected a companies resource with both a get and a list query")
	}

	for _, op := range []*Operation{companies.GetQuery, companies.ListQuery} {
		if strings.Contains(op.SelectionSet, "id") {
			t.Errorf("%s re-adds Company.id after it was ignored:\n%s", op.Name, op.SelectionSet)
		}
		if !strings.Contains(op.SelectionSet, "__typename") {
			t.Errorf("%s has no selection to fall back on:\n%s", op.Name, op.SelectionSet)
		}
		if strings.Contains(op.SelectionSet, "{ }") || strings.Contains(op.SelectionSet, "{  }") {
			t.Errorf("%s emits an empty selection set, which is invalid GraphQL:\n%s", op.Name, op.SelectionSet)
		}
	}
}
