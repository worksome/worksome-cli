package codegen

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator/rules"
)

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
	if len(matches) == 0 {
		t.Fatal("no GraphQL documents found in generated queries — regex out of date?")
	}

	for _, m := range matches {
		doc := m[1] + m[2]
		name := operationName(doc)

		if _, errs := gqlparser.LoadQueryWithRules(schema, doc, rules.NewDefaultRules()); errs != nil {
			t.Errorf("generated query %s is invalid against the schema:\n%v\n\n%s", name, errs, doc)
		}
	}

	t.Logf("validated %d generated GraphQL documents", len(matches))
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
