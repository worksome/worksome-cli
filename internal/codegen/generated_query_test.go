package codegen

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator/rules"
	"github.com/worksome/worksome-cli/internal/client"
	"gopkg.in/yaml.v3"
)

// optionalCallLiteral matches the on-request selections a generated querier hands
// to ExecuteWithOptional, so the guard can validate the expanded documents the
// client sends when --fields names one — not only the lean default.
var optionalCallLiteral = regexp.MustCompile(`ExecuteWithOptional\(ctx, query, (nil|map\[string\]string\{.*?\}), vars, &result\)`)

// optionalEntry matches one `"key": "value"` pair inside that literal.
var optionalEntry = regexp.MustCompile(`("(?:[^"\\]|\\.)*"): ("(?:[^"\\]|\\.)*")`)

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

	optionals := optionalCallLiteral.FindAllStringSubmatch(string(src), -1)
	declaredOptional := strings.Count(string(src), "ExecuteWithOptional(")
	if len(optionals) != declaredOptional {
		t.Fatalf("matched %d ExecuteWithOptional calls but the file declares %d — optionalCallLiteral is out of date",
			len(optionals), declaredOptional)
	}

	expanded := 0
	for _, m := range matches {
		doc := m[1] + m[2]
		name := operationName(doc)

		if _, errs := gqlparser.LoadQueryWithRules(schema, doc, rules.NewDefaultRules()); errs != nil {
			t.Errorf("generated query %s is invalid against the schema:\n%v\n\n%s", name, errs, doc)
		}

		// Queries with on-request relations must also be valid with every
		// relation added, which is what goes over the wire under --fields.
		if optional := optionalFor(t, string(src), doc); len(optional) > 0 {
			full := client.AddOptionalSelections(doc, optional)
			if _, errs := gqlparser.LoadQueryWithRules(schema, full, rules.NewDefaultRules()); errs != nil {
				t.Errorf("generated query %s is invalid once its on-request relations are added:\n%v\n\n%s", name, errs, full)
			}
			expanded++
		}
	}

	t.Logf("validated %d generated GraphQL documents (%d also with on-request relations)", len(matches), expanded)
}

// optionalFor returns the on-request selections the querier passes for the
// operation whose document is doc, by reading the ExecuteWithOptional call that
// follows the document in the generated source.
func optionalFor(t *testing.T, src, doc string) map[string]string {
	t.Helper()
	idx := strings.Index(src, doc)
	if idx < 0 {
		return nil
	}
	rest := src[idx+len(doc):]
	end := strings.Index(rest, "\nfunc ")
	if end < 0 {
		end = len(rest)
	}
	m := optionalCallLiteral.FindStringSubmatch(rest[:end])
	if m == nil || m[1] == "nil" {
		return nil
	}
	out := make(map[string]string)
	for _, e := range optionalEntry.FindAllStringSubmatch(m[1], -1) {
		k, err := strconv.Unquote(e[1])
		if err != nil {
			t.Fatalf("unquoting optional key %s: %v", e[1], err)
		}
		v, err := strconv.Unquote(e[2])
		if err != nil {
			t.Fatalf("unquoting optional selection %s: %v", e[2], err)
		}
		out[k] = v
	}
	return out
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
		if optional := optionalFor(t, string(src), doc); len(optional) > 0 {
			documents = append(documents, client.AddOptionalSelections(doc, optional))
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
