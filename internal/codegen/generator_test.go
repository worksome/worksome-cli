package codegen

import (
	"fmt"
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

func TestGenerateEmptyInputGuard(t *testing.T) {
	// Mutations whose input type declares fields must refuse to run with an
	// empty input object instead of sending {} to the API. Mutations without
	// an input type must not get the guard.
	schema := `
type Query {
	jobs(first: Int! = 10, page: Int): JobPaginator!
}

type Mutation {
	"Create a job."
	createJob(input: CreateJobInput!): Job!
	"Accept a bid."
	acceptBid(input: AcceptBidInput!): Job!
	"Regenerate the report."
	generateReport: Job!
}

type Job {
	id: ID!
	title: String!
}

type JobPaginator {
	paginatorInfo: PaginatorInfo!
	data: [Job!]!
}

type PaginatorInfo {
	count: Int!
	currentPage: Int!
	hasMorePages: Boolean!
	lastPage: Int!
	perPage: Int!
	total: Int!
}

input CreateJobInput {
	title: String!
	description: String
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

	outputDir := t.TempDir()
	gen := NewGenerator(parsed, outputDir, "github.com/worksome/worksome-cli")

	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	cmdsBytes, err := os.ReadFile(filepath.Join(outputDir, "commands", "commands.go"))
	if err != nil {
		t.Fatalf("reading generated commands: %v", err)
	}
	cmds := string(cmdsBytes)

	// The shared helper and its error message must be present
	if !strings.Contains(cmds, "func requireInput(vars map[string]any) error") {
		t.Error("expected generated commands to define the requireInput helper")
	}
	if !strings.Contains(cmds, `no input provided: pass --input or set flags (see --help)`) {
		t.Error("expected generated commands to contain the empty-input error message")
	}

	// Both mutations with input types get the guard: createJob (subcommand)
	// and acceptBid (hoisted). generateReport has no input type — no guard.
	guardCalls := strings.Count(cmds, "if err := requireInput(vars); err != nil")
	if guardCalls != 2 {
		t.Errorf("expected 2 requireInput guard calls (createJob, acceptBid), got %d", guardCalls)
	}

	// The old non-fatal warning must be gone
	if strings.Contains(cmds, "Warning: no input provided") {
		t.Error("generated commands should error on empty input, not just warn")
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

// An argument whose GraphQL type is an input object (or a list of them) cannot
// be sent as a bare string — the server rejects it as a type mismatch. The
// generated command must decode the flag value as JSON instead.
func TestInputObjectArgsDecodeAsJSON(t *testing.T) {
	schema := `
type Query {
	paymentRequests(
		requestedDateRange: DateRangeInput
		orderBy: [OrderByClauseInput!]
		search: String
		first: Int! = 10
		page: Int
	): PaymentRequestPaginator!
}

input DateRangeInput {
	start: Date!
	end: Date!
}

input OrderByClauseInput {
	column: String!
	order: String!
}

scalar Date

type PaymentRequest {
	id: ID!
	number: String!
}

type PaymentRequestPaginator {
	paginatorInfo: PaginatorInfo!
	data: [PaymentRequest!]!
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

	cmdsBytes, err := os.ReadFile(filepath.Join(outputDir, "commands", "commands.go"))
	if err != nil {
		t.Fatalf("reading generated commands: %v", err)
	}
	cmds := string(cmdsBytes)

	if !strings.Contains(cmds, "func jsonArg(flag, gqlType, raw string) (any, error)") {
		t.Error("expected generated commands to define the jsonArg helper")
	}

	// The input object and the list of input objects both decode as JSON.
	if !strings.Contains(cmds, `jsonArg("requested-date-range", "DateRangeInput", raw)`) {
		t.Error("expected --requested-date-range to decode as JSON for DateRangeInput")
	}
	if !strings.Contains(cmds, `jsonArg("order-by", "[OrderByClauseInput!]", raw)`) {
		t.Error("expected --order-by to decode as JSON for a list of input objects")
	}

	// A plain scalar argument must keep its string handling.
	if !strings.Contains(cmds, `v, _ := cmd.Flags().GetString("search")`) {
		t.Error("expected --search to keep plain string handling")
	}
	if strings.Contains(cmds, `jsonArg("search"`) {
		t.Error("--search is a String, it must not be JSON-decoded")
	}

	// Help text should tell the user JSON is expected.
	if !strings.Contains(cmds, "(JSON for DateRangeInput)") {
		t.Error("expected the flag description to advertise JSON and name the type")
	}
}

func TestIsJSONArg(t *testing.T) {
	inputRef := TypeRef{Name: "DateRangeInput", IsInput: true}
	tests := []struct {
		name string
		t    TypeRef
		want bool
	}{
		{"input object", inputRef, true},
		{"list of input objects", TypeRef{Name: "DateRangeInput", IsList: true, ListItem: &inputRef}, true},
		{"scalar", TypeRef{Name: "String", IsScalar: true}, false},
		{"enum", TypeRef{Name: "Status", IsEnum: true}, false},
		{"list of scalars", TypeRef{Name: "ID", IsList: true, ListItem: &TypeRef{Name: "ID", IsScalar: true}}, false},
		{"list with no item", TypeRef{Name: "X", IsList: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isJSONArg(tt.t); got != tt.want {
				t.Errorf("isJSONArg(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestGqlTypeName(t *testing.T) {
	req := TypeRef{Name: "OrderByInput", IsInput: true, IsRequired: true}
	opt := TypeRef{Name: "OrderByInput", IsInput: true}
	tests := []struct {
		name string
		t    TypeRef
		want string
	}{
		{"optional input", opt, "OrderByInput"},
		{"required input", TypeRef{Name: "DateRangeInput", IsInput: true, IsRequired: true}, "DateRangeInput!"},
		{"list of required items", TypeRef{Name: "OrderByInput", IsList: true, ListItem: &req}, "[OrderByInput!]"},
		{"required list of required items", TypeRef{Name: "OrderByInput", IsList: true, IsRequired: true, ListItem: &req}, "[OrderByInput!]!"},
		{"required list of optional items", TypeRef{Name: "OrderByInput", IsList: true, IsRequired: true, ListItem: &opt}, "[OrderByInput]!"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gqlTypeName(tt.t); got != tt.want {
				t.Errorf("gqlTypeName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFlagHintJSONDescription(t *testing.T) {
	input := TypeRef{Name: "DateRangeInput", IsInput: true}

	// An argument with no description must not produce a leading space.
	for _, desc := range []string{"", "   ", "\t\n"} {
		if got := flagHint(desc, input); got != "(JSON for DateRangeInput)" {
			t.Errorf("flagHint(%q) = %q, want no leading space", desc, got)
		}
	}

	if got := flagHint("Filter by date.", input); got != "Filter by date. (JSON for DateRangeInput)" {
		t.Errorf("flagHint with description = %q", got)
	}

	// Non-JSON types keep their existing behaviour untouched.
	if got := flagHint("Plain.", TypeRef{Name: "String", IsScalar: true}); got != "Plain." {
		t.Errorf("scalar flagHint = %q, want %q", got, "Plain.")
	}
}

// flagHint truncates long enum lists for display. It must not do so in place:
// the slice aliases the shared IR, and enumValuesLiteral renders the same values
// into shell completions afterwards.
func TestFlagHintDoesNotMutateEnumValues(t *testing.T) {
	original := make([]string, maxEnumHint+3)
	for i := range original {
		original[i] = fmt.Sprintf("VALUE_%02d", i)
	}
	values := append([]string{}, original...)
	typ := TypeRef{Name: "ApprovalApprovableState", IsEnum: true, EnumValues: values}

	hint := flagHint("The state.", typ)
	if !strings.Contains(hint, "...") {
		t.Fatalf("expected a truncated hint, got %q", hint)
	}

	for i, want := range original {
		if typ.EnumValues[i] != want {
			t.Errorf("flagHint corrupted EnumValues[%d]: got %q, want %q", i, typ.EnumValues[i], want)
		}
	}
}

// A back-quoted word in a flag's usage string is read by pflag as the value
// placeholder, so a schema description containing `accounts` turned
// `--viewer-role string` into `--viewer-role accounts`. No generated usage
// string may carry a backtick.
func TestGeneratedFlagUsageHasNoBackticks(t *testing.T) {
	schema := `
type Query {
	"List payment requests."
	paymentRequests(first: Int! = 10, page: Int, viewerRole: String, "Only filters within the scope set by the ` + "`accounts`" + ` arg." accounts: [ID!]): PaymentRequestPaginator!
}

type Mutation {
	"Create a job."
	createJob(input: CreateJobInput!): Job!
}

input CreateJobInput {
	"Use ` + "`DRAFT`" + ` to keep the job unpublished."
	status: String
	title: String!
}

type Job {
	id: ID!
	title: String!
}

type PaymentRequest {
	id: ID!
}

type PaymentRequestPaginator {
	paginatorInfo: PaginatorInfo!
	data: [PaymentRequest!]!
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
	if err := NewGenerator(parsed, outputDir, "github.com/worksome/worksome-cli").Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	cmds, err := os.ReadFile(filepath.Join(outputDir, "commands", "commands.go"))
	if err != nil {
		t.Fatal(err)
	}

	var usages int
	for _, line := range strings.Split(string(cmds), "\n") {
		if !strings.Contains(line, "cmd.Flags().") {
			continue
		}
		usages++
		if strings.Contains(line, "`") {
			t.Errorf("flag usage string carries a backtick, pflag will read it as a placeholder:\n%s", strings.TrimSpace(line))
		}
	}
	if usages == 0 {
		t.Fatal("no flag definitions found in generated commands; the check tested nothing")
	}
	if !strings.Contains(string(cmds), "'accounts'") || !strings.Contains(string(cmds), "'DRAFT'") {
		t.Error("expected the backticked words to survive as quoted text")
	}
}

// Every value of an enum up to the cap is listed. ContractTerminationReason has
// 14 values; showing five and "..." left the other nine undiscoverable from
// --help — the only way to learn them was a rejected request.
func TestFlagHintListsWholeEnumUpToCap(t *testing.T) {
	values := make([]string, maxEnumHint)
	for i := range values {
		values[i] = fmt.Sprintf("REASON_%02d", i)
	}
	hint := flagHint("The reason.", TypeRef{Name: "Reason", IsEnum: true, EnumValues: values})
	for _, v := range values {
		if !strings.Contains(hint, v) {
			t.Errorf("hint is missing %s: %q", v, hint)
		}
	}
	if strings.Contains(hint, "...") {
		t.Errorf("an enum at the cap must not be truncated: %q", hint)
	}

	// One past the cap is truncated, and says how many values are hidden.
	over := append(append([]string{}, values...), "REASON_EXTRA")
	hint = flagHint("The reason.", TypeRef{Name: "Reason", IsEnum: true, EnumValues: over})
	want := fmt.Sprintf("... and %d more", len(over)-enumHintPreview)
	if !strings.Contains(hint, want) {
		t.Errorf("hint = %q, want it to end with %q", hint, want)
	}
	if strings.Contains(hint, "REASON_10") {
		t.Errorf("a truncated hint should show only the first %d values: %q", enumHintPreview, hint)
	}

	// Lists of enums (`--statuses`) take the same path.
	hint = flagHint("Statuses.", TypeRef{Name: "Reason", IsList: true, ListItem: &TypeRef{Name: "Reason", IsEnum: true, EnumValues: values}})
	if !strings.Contains(hint, "REASON_24") {
		t.Errorf("list-of-enum hint is truncated: %q", hint)
	}
}

// Mutation input fields declared non-null with no default must be checked
// before the request is sent. `hires terminate` without --date reached the
// server and came back as a 400 naming one field; the CLI knew from the schema
// that it was required and said nothing.
func TestGenerateRequiredInputFieldsCheck(t *testing.T) {
	schema := `
type Mutation {
	"Terminate a hire."
	terminateHire(input: TerminateHireInput!): Hire!
	"Update a job."
	updateJob(input: UpdateJobInput!): Job!
}

input TerminateHireInput {
	hire: ID!
	reason: String!
	comments: String
	date: String!
	notify: Boolean! = true
}

input UpdateJobInput {
	title: String
	description: String
}

type Hire {
	id: ID!
}

type Job {
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
	outputDir := t.TempDir()
	if err := NewGenerator(parsed, outputDir, "github.com/worksome/worksome-cli").Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	cmdsBytes, err := os.ReadFile(filepath.Join(outputDir, "commands", "commands.go"))
	if err != nil {
		t.Fatal(err)
	}
	cmds := string(cmdsBytes)

	// hire, reason and date are required; comments is nullable and notify has
	// a default, so neither may be demanded.
	want := `requireFields(inputObj, []requiredField{{"hire", "hire"}, {"reason", "reason"}, {"date", "date"}})`
	if !strings.Contains(cmds, want) {
		t.Errorf("generated commands should contain %s", want)
	}
	for _, forbidden := range []string{`{"comments"`, `{"notify"`} {
		if strings.Contains(cmds, forbidden) {
			t.Errorf("generated commands must not require %s", forbidden)
		}
	}

	// A mutation with no required fields emits no check at all.
	if n := strings.Count(cmds, "requireFields(inputObj"); n != 1 {
		t.Errorf("expected exactly one requireFields call (terminateHire only), found %d", n)
	}
}
