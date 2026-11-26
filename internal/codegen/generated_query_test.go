package codegen

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator/rules"
	"github.com/worksome/worksome-cli/internal/client"
	"github.com/worksome/worksome-cli/internal/generated/queries"
	"gopkg.in/yaml.v3"
)

// executeForCall matches the item type a generated querier hands to ExecuteFor,
// so the guard can validate the documents the client sends under --fields.
var executeForCall = regexp.MustCompile(`ExecuteFor\(ctx, query, FieldSchema, "(\w*)", vars, &result\)`)

// queryLiteral matches the backtick-quoted GraphQL documents emitted into the
// generated queries file.
var queryLiteral = regexp.MustCompile("(?s)`(query |mutation )(.*?)`")

// TestGeneratedQueriesValidateAgainstSchema validates every generated GraphQL
// document against the vendored schema.
//
// `make verify-generated` only proves the output is reproducible — it is blind
// to documents that are self-consistent but invalid against the server (a field
// selected with no subfields, a variable typed as String where the schema wants
// an input object). Those failures otherwise only surface as a server error at
// runtime, which is how the `accounts` interface bug shipped.
func TestGeneratedQueriesValidateAgainstSchema(t *testing.T) {
	src, err := os.ReadFile("../generated/queries/queries.go")
	if err != nil {
		t.Fatalf("reading generated queries: %v", err)
	}

	schemaSrc, err := os.ReadFile("../../schema/schema.graphql")
	if err != nil {
		t.Fatalf("reading schema: %v", err)
	}

	schema, gqlErr := gqlparser.LoadSchema(&ast.Source{
		Name:  "schema.graphql",
		Input: string(schemaSrc),
	})
	if gqlErr != nil {
		t.Fatalf("loading schema: %v", gqlErr)
	}

	matches := queryLiteral.FindAllStringSubmatch(string(src), -1)

	// A guard that silently stops covering things is worse than no guard. Every
	// generated operation assigns exactly one document, so the two counts must
	// agree — if the emitted shape ever changes, this fails instead of passing
	// on a shrinking subset.
	declared := strings.Count(string(src), "query := `")
	if len(matches) != declared {
		t.Fatalf("matched %d GraphQL documents but the file declares %d — queryLiteral is out of date",
			len(matches), declared)
	}
	if declared == 0 {
		t.Fatal("no GraphQL documents found in generated queries")
	}

	calls := executeForCall.FindAllStringSubmatch(string(src), -1)
	if declared := strings.Count(string(src), "ExecuteFor("); len(calls) != declared {
		t.Fatalf("matched %d ExecuteFor calls but the file declares %d — executeForCall is out of date",
			len(calls), declared)
	}

	expanded := 0
	for _, m := range matches {
		doc := m[1] + m[2]
		name := operationName(doc)

		if _, errs := gqlparser.LoadQueryWithRules(schema, doc, rules.NewDefaultRules()); errs != nil {
			t.Errorf("generated query %s is invalid against the schema:\n%v\n\n%s", name, errs, doc)
		}

		// Every field --fields can name on the printed type must also produce a
		// valid document, which is what goes over the wire under --fields.
		if full := expandedFor(t, string(src), doc); full != "" {
			if _, errs := gqlparser.LoadQueryWithRules(schema, full, rules.NewDefaultRules()); errs != nil {
				t.Errorf("generated query %s is invalid with every --fields field selected:\n%v\n\n%s", name, errs, full)
			}
			expanded++
		}
	}

	t.Logf("validated %d generated GraphQL documents (%d also with every --fields field)", len(matches), expanded)
}

// expandedFor returns doc as ExecuteFor sends it when --fields names every
// field of the printed type, or "" when the operation has no field schema.
func expandedFor(t *testing.T, src, doc string) string {
	t.Helper()
	idx := strings.Index(src, doc)
	if idx < 0 {
		return ""
	}
	rest := src[idx+len(doc):]
	if end := strings.Index(rest, "\nfunc "); end >= 0 {
		rest = rest[:end]
	}
	m := executeForCall.FindStringSubmatch(rest)
	if m == nil {
		return ""
	}
	info, ok := queries.FieldSchema[m[1]]
	if !ok || info.Fields == nil {
		return ""
	}
	var fields []string
	for name, f := range info.Fields {
		if !f.NeedsArgs {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	full, err := client.SelectFields(doc, queries.FieldSchema, m[1], fields)
	if err != nil {
		t.Errorf("selecting every field of %s: %v", m[1], err)
		return ""
	}
	return full
}

// operationName extracts the operation name from a GraphQL document for test output.
func operationName(doc string) string {
	fields := strings.FieldsFunc(doc, func(r rune) bool {
		return r == ' ' || r == '(' || r == '{' || r == '\n'
	})
	if len(fields) >= 2 {
		return fields[1]
	}
	return "<unnamed>"
}

// TestGeneratedQueriesOmitIgnoredFields is the regression guard for
// `ignore_fields`. Introspection strips applied directives, so the vendored
// schema cannot say which fields the server access-controls; a guarded field
// declared non-null nulls its whole parent when the guard denies, which fails
// the command rather than degrading it. Regeneration must not quietly put one
// back, so this checks the committed documents, not the parser.
//
// Field names are matched against the type they are selected on, which is what
// the validator resolves for us — a same-named field elsewhere is unaffected.
func TestGeneratedQueriesOmitIgnoredFields(t *testing.T) {
	overridesSrc, err := os.ReadFile("../../schema/overrides.yaml")
	if err != nil {
		t.Fatalf("reading overrides: %v", err)
	}
	var overrides Overrides
	if err := yaml.Unmarshal(overridesSrc, &overrides); err != nil {
		t.Fatalf("parsing overrides: %v", err)
	}
	if len(overrides.IgnoreFields) == 0 {
		t.Fatal("overrides declare no ignore_fields — this guard would pass vacuously")
	}
	ignored := make(map[string]bool, len(overrides.IgnoreFields))
	for _, entry := range overrides.IgnoreFields {
		ignored[entry] = true
	}

	src, err := os.ReadFile("../generated/queries/queries.go")
	if err != nil {
		t.Fatalf("reading generated queries: %v", err)
	}
	schemaSrc, err := os.ReadFile("../../schema/schema.graphql")
	if err != nil {
		t.Fatalf("reading schema: %v", err)
	}
	schema, gqlErr := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(schemaSrc)})
	if gqlErr != nil {
		t.Fatalf("loading schema: %v", gqlErr)
	}

	matches := queryLiteral.FindAllStringSubmatch(string(src), -1)
	if len(matches) == 0 {
		t.Fatal("no GraphQL documents found in generated queries")
	}

	for _, m := range matches {
		doc := m[1] + m[2]
		name := operationName(doc)

		documents := []string{doc}
		if full := expandedFor(t, string(src), doc); full != "" {
			documents = append(documents, full)
		}

		for _, d := range documents {
			parsed, errs := gqlparser.LoadQueryWithRules(schema, d, rules.NewDefaultRules())
			if errs != nil {
				continue // TestGeneratedQueriesValidateAgainstSchema reports this.
			}
			for _, op := range parsed.Operations {
				for _, selected := range selectedFields(op.SelectionSet) {
					if ignored[selected] {
						t.Errorf("generated query %s selects %q, which ignore_fields excludes", name, selected)
					}
				}
			}
		}
	}
}

// selectedFields walks a selection set and returns every field it selects as
// "Type.field", using the parent type the validator resolved for each field.
func selectedFields(set ast.SelectionSet) []string {
	var out []string
	for _, selection := range set {
		switch s := selection.(type) {
		case *ast.Field:
			if s.ObjectDefinition != nil {
				out = append(out, s.ObjectDefinition.Name+"."+s.Name)
			}
			out = append(out, selectedFields(s.SelectionSet)...)
		case *ast.InlineFragment:
			out = append(out, selectedFields(s.SelectionSet)...)
		case *ast.FragmentSpread:
			if s.Definition != nil {
				out = append(out, selectedFields(s.Definition.SelectionSet)...)
			}
		}
	}
	return out
}

// contactFields are personal contact details that default selections must not
// copy out of nested objects; --fields still selects them on request.
var contactFields = map[string]bool{"email": true, "phone": true}

// TestGeneratedQueriesOmitNestedContactFields guards the committed documents:
// the printed object may carry its own email, but a nested worker, owner or
// user must not bring theirs along by default.
func TestGeneratedQueriesOmitNestedContactFields(t *testing.T) {
	src, err := os.ReadFile("../generated/queries/queries.go")
	if err != nil {
		t.Fatalf("reading generated queries: %v", err)
	}
	schemaSrc, err := os.ReadFile("../../schema/schema.graphql")
	if err != nil {
		t.Fatalf("reading schema: %v", err)
	}
	schema, gqlErr := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(schemaSrc)})
	if gqlErr != nil {
		t.Fatalf("loading schema: %v", gqlErr)
	}

	for _, m := range queryLiteral.FindAllStringSubmatch(string(src), -1) {
		doc := m[1] + m[2]
		parsed, errs := gqlparser.LoadQueryWithRules(schema, doc, rules.NewDefaultRules())
		if errs != nil {
			continue // TestGeneratedQueriesValidateAgainstSchema reports this.
		}
		for _, op := range parsed.Operations {
			for _, path := range nestedContactFields(printedSelections(op.SelectionSet), "", false) {
				t.Errorf("generated query %s selects %s by default", operationName(doc), path)
			}
		}
	}
}

// printedSelections returns the selections of the object the CLI prints: the
// items of a paginated result, or the root field's object.
func printedSelections(set ast.SelectionSet) ast.SelectionSet {
	if len(set) != 1 {
		return set
	}
	root, ok := set[0].(*ast.Field)
	if !ok {
		return set
	}
	var data *ast.Field
	hasInfo := false
	for _, s := range root.SelectionSet {
		if f, ok := s.(*ast.Field); ok {
			switch f.Name {
			case "data":
				data = f
			case "paginatorInfo":
				hasInfo = true
			}
		}
	}
	if data != nil && hasInfo {
		return data.SelectionSet
	}
	return root.SelectionSet
}

func nestedContactFields(set ast.SelectionSet, prefix string, nested bool) []string {
	var out []string
	for _, selection := range set {
		switch s := selection.(type) {
		case *ast.Field:
			if nested && contactFields[s.Name] {
				out = append(out, prefix+s.Name)
			}
			out = append(out, nestedContactFields(s.SelectionSet, prefix+s.Name+".", true)...)
		case *ast.InlineFragment:
			out = append(out, nestedContactFields(s.SelectionSet, prefix, nested)...)
		}
	}
	return out
}
